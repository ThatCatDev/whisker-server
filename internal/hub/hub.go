// Package hub relays Yjs sync and awareness traffic between the clients of
// a board and persists updates as opaque blobs. It holds no CRDT state:
// memory per room is just the connection set plus cached presence payloads.
//
// Sync flow with each client (see ysync for the framing):
//
//	client → step1(its state vector)   server ignores the vector and replies
//	server → step2(blob) × N           with every stored blob, then asks for
//	server → step1(empty vector)       the client's FULL state,
//	client → step2(full state)         which becomes the new compact snapshot.
//
// Later incremental updates are appended and broadcast as-is. The step-2
// snapshot is safe as a replacement because the client provably merged all
// N blobs before answering (websocket messages are processed in order).
package hub

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"

	"whisker-server/internal/ysync"
)

// Compaction only replaces storage when the log has grown past this many
// blobs; below it the extra write/delete churn isn't worth it.
const compactThreshold = 8

// Doc persistence operations the hub needs (subset of store.Store).
type Doc interface {
	LoadUpdates(ctx context.Context, boardID string) ([][]byte, int64, error)
	AppendUpdate(ctx context.Context, boardID string, blob []byte) error
	Compact(ctx context.Context, boardID string, snapshot []byte, uptoSeq int64) error
}

type Hub struct {
	docs Doc
	log  *slog.Logger

	mu    sync.Mutex
	rooms map[string]*room
}

func New(docs Doc, log *slog.Logger) *Hub {
	return &Hub{docs: docs, log: log, rooms: map[string]*room{}}
}

type room struct {
	id string

	mu    sync.Mutex
	conns map[*conn]struct{}
	// Number of stored blobs; drives the compaction threshold.
	blobCount int
}

type conn struct {
	ws   *websocket.Conn
	send chan []byte
	user string

	// Sequence high-water mark at the moment the server asked this client
	// for its full state; the answer may compact everything up to here.
	highWater int64
	// The next step-2 from this client answers our empty-vector step-1 and
	// carries its full document state.
	awaitingSnapshot bool

	// Presence bookkeeping: the client's last awareness payload (replayed to
	// newcomers) and clientID→clock (to announce departure on disconnect).
	awarenessPayload []byte
	awarenessClients map[uint64]uint64
}

// Serve owns the websocket for one board connection until it closes.
func (h *Hub) Serve(ctx context.Context, ws *websocket.Conn, boardID, userID string) {
	ws.SetReadLimit(64 << 20) // whole boards travel as one message

	c := &conn{
		ws:               ws,
		send:             make(chan []byte, 64),
		user:             userID,
		awarenessClients: map[uint64]uint64{},
	}
	r := h.joinRoom(boardID, c)
	defer h.leaveRoom(r, c)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go c.writeLoop(ctx, cancel)
	go keepalive(ctx, ws)

	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageBinary {
			continue
		}
		if err := h.handle(ctx, r, c, data); err != nil {
			h.log.Warn("closing connection", "board", boardID, "user", userID, "err", err)
			return
		}
	}
}

func (h *Hub) handle(ctx context.Context, r *room, c *conn, data []byte) error {
	msg, err := ysync.Parse(data)
	if err != nil {
		return err
	}
	switch msg.Type {
	case ysync.MessageSync:
		return h.handleSync(ctx, r, c, msg, data)
	case ysync.MessageAwareness:
		h.handleAwareness(r, c, msg, data)
	case ysync.MessageQueryAwareness:
		r.replayAwareness(c)
	}
	return nil
}

func (h *Hub) handleSync(ctx context.Context, r *room, c *conn, msg ysync.Message, raw []byte) error {
	switch msg.SubType {
	case ysync.SyncStep1:
		// The client wants to sync. Send everything we have, then request
		// its full state (which doubles as our compaction pass).
		blobs, maxSeq, err := h.docs.LoadUpdates(ctx, r.id)
		if err != nil {
			return err
		}
		for _, b := range blobs {
			c.trySend(ysync.EncodeSyncStep2(b))
		}
		c.trySend(ysync.EncodeSyncStep1Empty())
		c.highWater = maxSeq
		c.awaitingSnapshot = true
		r.mu.Lock()
		r.blobCount = len(blobs)
		r.mu.Unlock()
		r.replayAwareness(c)
		return nil

	case ysync.SyncStep2, ysync.SyncUpdate:
		update := msg.Payload
		if c.awaitingSnapshot && msg.SubType == ysync.SyncStep2 {
			c.awaitingSnapshot = false
			r.mu.Lock()
			worthCompacting := r.blobCount >= compactThreshold
			r.mu.Unlock()
			if worthCompacting {
				if err := h.docs.Compact(ctx, r.id, update, c.highWater); err != nil {
					// Never lose data over a failed optimization.
					h.log.Warn("compact failed, appending instead", "board", r.id, "err", err)
					if err := h.docs.AppendUpdate(ctx, r.id, update); err != nil {
						return err
					}
				} else {
					r.mu.Lock()
					r.blobCount = 1
					r.mu.Unlock()
				}
				r.broadcast(c, ysync.EncodeUpdate(update))
				return nil
			}
			// Small logs: an empty-doc client answers with a ~2-byte state;
			// storing that adds nothing.
			if len(update) <= 2 {
				return nil
			}
		}
		if err := h.docs.AppendUpdate(ctx, r.id, update); err != nil {
			return err
		}
		r.mu.Lock()
		r.blobCount++
		r.mu.Unlock()
		r.broadcast(c, ysync.EncodeUpdate(update))
		return nil
	}
	return nil
}

func (h *Hub) handleAwareness(r *room, c *conn, msg ysync.Message, raw []byte) {
	if entries, err := ysync.ParseAwareness(msg.Payload); err == nil {
		for _, e := range entries {
			if e.State == "null" {
				delete(c.awarenessClients, e.ClientID)
			} else {
				c.awarenessClients[e.ClientID] = e.Clock
			}
		}
	}
	c.awarenessPayload = append([]byte(nil), msg.Payload...)
	r.broadcast(c, append([]byte(nil), raw...))
}

func (h *Hub) joinRoom(boardID string, c *conn) *room {
	h.mu.Lock()
	r, ok := h.rooms[boardID]
	if !ok {
		r = &room{id: boardID, conns: map[*conn]struct{}{}}
		h.rooms[boardID] = r
	}
	h.mu.Unlock()
	r.mu.Lock()
	r.conns[c] = struct{}{}
	r.mu.Unlock()
	return r
}

func (h *Hub) leaveRoom(r *room, c *conn) {
	r.mu.Lock()
	delete(r.conns, c)
	empty := len(r.conns) == 0
	r.mu.Unlock()
	// Tell peers this client's cursors are gone (instead of the 30s timeout).
	if len(c.awarenessClients) > 0 {
		r.broadcast(c, ysync.EncodeAwarenessRemoval(c.awarenessClients))
	}
	if empty {
		h.mu.Lock()
		r.mu.Lock()
		if len(r.conns) == 0 {
			delete(h.rooms, r.id)
		}
		r.mu.Unlock()
		h.mu.Unlock()
	}
}

// broadcast queues a frame to every room member except origin.
func (r *room) broadcast(origin *conn, frame []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for peer := range r.conns {
		if peer != origin {
			peer.trySend(frame)
		}
	}
}

// replayAwareness sends every peer's cached presence to one connection.
func (r *room) replayAwareness(c *conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for peer := range r.conns {
		if peer != c && peer.awarenessPayload != nil {
			c.trySend(ysync.EncodeAwareness(peer.awarenessPayload))
		}
	}
}

// trySend queues without blocking; a full buffer means a dead/stalled peer,
// which the write loop resolves by closing.
func (c *conn) trySend(frame []byte) {
	select {
	case c.send <- frame:
	default:
		c.ws.Close(websocket.StatusPolicyViolation, "send buffer overflow")
	}
}

func (c *conn) writeLoop(ctx context.Context, cancel context.CancelFunc) {
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-c.send:
			if err := c.ws.Write(ctx, websocket.MessageBinary, frame); err != nil {
				return
			}
		}
	}
}

// keepalive pings so idle connections survive proxies and load balancers.
func keepalive(ctx context.Context, ws *websocket.Conn) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := ws.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

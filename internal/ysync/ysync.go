// Package ysync speaks the y-websocket wire protocol: framing of sync,
// awareness and query-awareness messages. The server is a RELAY — it never
// materializes CRDT documents. It stores updates as opaque blobs and answers
// a client's sync-step-1 with the stored blobs plus its own sync-step-1
// carrying an EMPTY state vector, which makes the client respond with its
// full document state (stored as a fresh, compacted snapshot).
package ysync

import (
	"errors"
	"fmt"

	"whisker-server/internal/lib0"
)

// Top-level message types (y-websocket).
const (
	MessageSync           = 0
	MessageAwareness      = 1
	MessageAuth           = 2
	MessageQueryAwareness = 3
)

// Sync sub-types (y-protocols/sync).
const (
	SyncStep1 = 0 // payload: state vector
	SyncStep2 = 1 // payload: update (diff against a received state vector)
	SyncUpdate = 2 // payload: incremental update
)

// Message is one parsed top-level protocol message.
type Message struct {
	Type    uint64
	SubType uint64 // meaningful for MessageSync only
	Payload []byte // state vector / update / awareness update
}

var ErrUnknownMessage = errors.New("ysync: unknown message type")

// Parse decodes one websocket frame. Payload aliases data.
func Parse(data []byte) (Message, error) {
	d := lib0.NewDecoder(data)
	t, err := d.VarUint()
	if err != nil {
		return Message{}, err
	}
	switch t {
	case MessageSync:
		sub, err := d.VarUint()
		if err != nil {
			return Message{}, err
		}
		payload, err := d.VarUint8Array()
		if err != nil {
			return Message{}, err
		}
		return Message{Type: t, SubType: sub, Payload: payload}, nil
	case MessageAwareness:
		payload, err := d.VarUint8Array()
		if err != nil {
			return Message{}, err
		}
		return Message{Type: t, Payload: payload}, nil
	case MessageQueryAwareness, MessageAuth:
		return Message{Type: t}, nil
	default:
		return Message{}, fmt.Errorf("%w: %d", ErrUnknownMessage, t)
	}
}

// EncodeSyncStep2 frames a stored update blob as a sync-step-2 message.
func EncodeSyncStep2(update []byte) []byte {
	e := lib0.NewEncoder()
	e.WriteVarUint(MessageSync)
	e.WriteVarUint(SyncStep2)
	e.WriteVarUint8Array(update)
	return e.Bytes()
}

// EncodeSyncStep1Empty frames a sync-step-1 with an empty state vector: "I
// have nothing — send me everything". The client's answer is its complete
// document state as one update.
func EncodeSyncStep1Empty() []byte {
	e := lib0.NewEncoder()
	e.WriteVarUint(MessageSync)
	e.WriteVarUint(SyncStep1)
	e.WriteVarUint8Array([]byte{0}) // state vector of an empty doc
	return e.Bytes()
}

// EncodeUpdate frames an update blob as an incremental sync update.
func EncodeUpdate(update []byte) []byte {
	e := lib0.NewEncoder()
	e.WriteVarUint(MessageSync)
	e.WriteVarUint(SyncUpdate)
	e.WriteVarUint8Array(update)
	return e.Bytes()
}

// EncodeAwareness frames a raw awareness-update payload.
func EncodeAwareness(payload []byte) []byte {
	e := lib0.NewEncoder()
	e.WriteVarUint(MessageAwareness)
	e.WriteVarUint8Array(payload)
	return e.Bytes()
}

// AwarenessEntry is one client's presence record inside an awareness update.
type AwarenessEntry struct {
	ClientID uint64
	Clock    uint64
	State    string // JSON; "null" means "left"
}

// ParseAwareness decodes an awareness-update payload
// (y-protocols/awareness encodeAwarenessUpdate).
func ParseAwareness(payload []byte) ([]AwarenessEntry, error) {
	d := lib0.NewDecoder(payload)
	n, err := d.VarUint()
	if err != nil {
		return nil, err
	}
	out := make([]AwarenessEntry, 0, n)
	for i := uint64(0); i < n; i++ {
		id, err := d.VarUint()
		if err != nil {
			return nil, err
		}
		clock, err := d.VarUint()
		if err != nil {
			return nil, err
		}
		state, err := d.VarString()
		if err != nil {
			return nil, err
		}
		out = append(out, AwarenessEntry{ClientID: id, Clock: clock, State: state})
	}
	return out, nil
}

// EncodeAwarenessRemoval builds a full awareness message announcing that the
// given clients left (state null, clock bumped past their last known one) —
// sent on disconnect so peers drop cursors immediately instead of after the
// 30s awareness timeout.
func EncodeAwarenessRemoval(clients map[uint64]uint64) []byte {
	e := lib0.NewEncoder()
	e.WriteVarUint(uint64(len(clients)))
	for id, clock := range clients {
		e.WriteVarUint(id)
		e.WriteVarUint(clock + 1)
		e.WriteVarString("null")
	}
	return EncodeAwareness(e.Bytes())
}

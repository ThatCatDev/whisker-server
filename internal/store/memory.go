package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Memory is a process-local Store for development: no database required,
// nothing survives a restart.
type Memory struct {
	mu     sync.Mutex
	boards map[string]Board
	docs   map[string]*memDoc
}

type memDoc struct {
	blobs []memBlob
	seq   int64
}

type memBlob struct {
	seq  int64
	data []byte
}

func NewMemory() *Memory {
	return &Memory{
		boards: map[string]Board{},
		docs:   map[string]*memDoc{},
	}
}

func (m *Memory) Close() {}

func (m *Memory) ListBoards(_ context.Context, userID string) ([]Board, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Board{}
	for _, b := range m.boards {
		if b.OwnerID == userID {
			out = append(out, b)
		}
	}
	return out, nil
}

func (m *Memory) CreateBoard(_ context.Context, userID, name string) (Board, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	b := Board{ID: newID(), OwnerID: userID, Name: name, CreatedAt: now, UpdatedAt: now}
	m.boards[b.ID] = b
	return b, nil
}

func (m *Memory) RenameBoard(_ context.Context, boardID, userID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.boards[boardID]
	if !ok {
		return ErrNotFound
	}
	if b.OwnerID != userID {
		return ErrForbidden
	}
	b.Name = name
	b.UpdatedAt = time.Now()
	m.boards[boardID] = b
	return nil
}

func (m *Memory) DeleteBoard(_ context.Context, boardID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.boards[boardID]
	if !ok {
		return ErrNotFound
	}
	if b.OwnerID != userID {
		return ErrForbidden
	}
	delete(m.boards, boardID)
	delete(m.docs, boardID)
	return nil
}

func (m *Memory) CanAccess(_ context.Context, boardID, userID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.boards[boardID]
	if !ok {
		return false, nil
	}
	return b.OwnerID == userID, nil
}

func (m *Memory) doc(boardID string) *memDoc {
	d, ok := m.docs[boardID]
	if !ok {
		d = &memDoc{}
		m.docs[boardID] = d
	}
	return d
}

func (m *Memory) LoadUpdates(_ context.Context, boardID string) ([][]byte, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.doc(boardID)
	blobs := make([][]byte, len(d.blobs))
	for i, b := range d.blobs {
		blobs[i] = b.data
	}
	return blobs, d.seq, nil
}

func (m *Memory) AppendUpdate(_ context.Context, boardID string, blob []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.doc(boardID)
	d.seq++
	d.blobs = append(d.blobs, memBlob{seq: d.seq, data: append([]byte(nil), blob...)})
	return nil
}

func (m *Memory) Compact(_ context.Context, boardID string, snapshot []byte, uptoSeq int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.doc(boardID)
	kept := []memBlob{}
	for _, b := range d.blobs {
		if b.seq > uptoSeq {
			kept = append(kept, b)
		}
	}
	// The snapshot logically replaces everything up to uptoSeq, so it takes
	// seq uptoSeq itself (now free) and sorts BEFORE any update that raced
	// in past the high-water mark.
	d.blobs = append(
		[]memBlob{{seq: uptoSeq, data: append([]byte(nil), snapshot...)}},
		kept...,
	)
	if d.seq < uptoSeq {
		d.seq = uptoSeq
	}
	return nil
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return hex.EncodeToString(b[:])
}

// Package store persists board metadata and Yjs document updates. Updates
// are opaque blobs — the server never interprets CRDT contents.
package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound  = errors.New("store: not found")
	ErrForbidden = errors.New("store: forbidden")
)

type Board struct {
	ID        string    `json:"id"`
	OwnerID   string    `json:"ownerId"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Store is implemented by the in-memory dev store and the Postgres store.
type Store interface {
	// Board metadata (REST API).
	ListBoards(ctx context.Context, userID string) ([]Board, error)
	CreateBoard(ctx context.Context, userID, name string) (Board, error)
	RenameBoard(ctx context.Context, boardID, userID, name string) error
	DeleteBoard(ctx context.Context, boardID, userID string) error
	// CanAccess reports whether the user may open the board's document.
	CanAccess(ctx context.Context, boardID, userID string) (bool, error)

	// Document sync. LoadUpdates returns stored blobs in append order plus
	// the sequence high-water mark. Compact atomically replaces every blob
	// up to uptoSeq with one snapshot (blobs appended after uptoSeq stay).
	LoadUpdates(ctx context.Context, boardID string) (blobs [][]byte, maxSeq int64, err error)
	AppendUpdate(ctx context.Context, boardID string, blob []byte) error
	Compact(ctx context.Context, boardID string, snapshot []byte, uptoSeq int64) error

	Close()
}

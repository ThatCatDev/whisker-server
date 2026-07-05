package store

import (
	"bytes"
	"context"
	"testing"
)

func TestMemoryDocLog(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()

	for _, b := range [][]byte{{1}, {2}, {3}} {
		if err := m.AppendUpdate(ctx, "b1", b); err != nil {
			t.Fatal(err)
		}
	}
	blobs, maxSeq, err := m.LoadUpdates(ctx, "b1")
	if err != nil || len(blobs) != 3 || maxSeq != 3 {
		t.Fatalf("blobs=%d maxSeq=%d err=%v", len(blobs), maxSeq, err)
	}

	// Compact everything ≤2 into a snapshot; blob 3 must survive AFTER it.
	if err := m.Compact(ctx, "b1", []byte{9, 9}, 2); err != nil {
		t.Fatal(err)
	}
	blobs, _, err = m.LoadUpdates(ctx, "b1")
	if err != nil || len(blobs) != 2 {
		t.Fatalf("after compact: blobs=%d err=%v", len(blobs), err)
	}
	if !bytes.Equal(blobs[0], []byte{9, 9}) || !bytes.Equal(blobs[1], []byte{3}) {
		t.Fatalf("order wrong: %v", blobs)
	}
}

func TestMemoryBoardOwnership(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	b, err := m.CreateBoard(ctx, "alice", "Plan")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RenameBoard(ctx, b.ID, "mallory", "Hacked"); err != ErrForbidden {
		t.Fatalf("rename by non-owner: %v", err)
	}
	ok, _ := m.CanAccess(ctx, b.ID, "alice")
	if !ok {
		t.Fatal("owner must access")
	}
	ok, _ = m.CanAccess(ctx, b.ID, "mallory")
	if ok {
		t.Fatal("stranger must not access")
	}
	if err := m.DeleteBoard(ctx, b.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ListBoards(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
}

package ysync

import (
	"bytes"
	"testing"
)

func TestParseSyncStep1(t *testing.T) {
	// [messageSync, syncStep1, len=1, sv=0x00] — what y-websocket sends on
	// connect against an empty local doc.
	msg, err := Parse([]byte{0x00, 0x00, 0x01, 0x00})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Type != MessageSync || msg.SubType != SyncStep1 || !bytes.Equal(msg.Payload, []byte{0}) {
		t.Fatalf("got %+v", msg)
	}
}

func TestEncodeParseRoundTrip(t *testing.T) {
	update := []byte{9, 8, 7, 6}
	for _, frame := range [][]byte{
		EncodeSyncStep2(update),
		EncodeUpdate(update),
	} {
		msg, err := Parse(frame)
		if err != nil {
			t.Fatal(err)
		}
		if msg.Type != MessageSync || !bytes.Equal(msg.Payload, update) {
			t.Fatalf("got %+v", msg)
		}
	}

	msg, err := Parse(EncodeSyncStep1Empty())
	if err != nil {
		t.Fatal(err)
	}
	if msg.SubType != SyncStep1 || !bytes.Equal(msg.Payload, []byte{0}) {
		t.Fatalf("empty step1: %+v", msg)
	}
}

func TestAwarenessRoundTrip(t *testing.T) {
	// Encode an awareness update the way y-protocols/awareness does, then
	// parse it back.
	frame := EncodeAwarenessRemoval(map[uint64]uint64{42: 7})
	msg, err := Parse(frame)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Type != MessageAwareness {
		t.Fatalf("type: %d", msg.Type)
	}
	entries, err := ParseAwareness(msg.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ClientID != 42 || entries[0].Clock != 8 || entries[0].State != "null" {
		t.Fatalf("entries: %+v", entries)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte{0x63}); err == nil {
		t.Fatal("unknown message type must error")
	}
	if _, err := Parse([]byte{0x00, 0x01, 0x05, 0x01}); err == nil {
		t.Fatal("truncated payload must error")
	}
}

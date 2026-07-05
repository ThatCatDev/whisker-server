package lib0

import (
	"bytes"
	"testing"
)

func TestVarUintRoundTrip(t *testing.T) {
	cases := []uint64{0, 1, 127, 128, 300, 16383, 16384, 1<<32 - 1, 1<<63 - 1}
	for _, v := range cases {
		e := NewEncoder()
		e.WriteVarUint(v)
		got, err := NewDecoder(e.Bytes()).VarUint()
		if err != nil || got != v {
			t.Fatalf("roundtrip %d: got %d err %v", v, got, err)
		}
	}
}

// Reference bytes produced by lib0/encoding.js writeVarUint.
func TestVarUintMatchesLib0(t *testing.T) {
	cases := map[uint64][]byte{
		0:   {0x00},
		127: {0x7f},
		128: {0x80, 0x01},
		300: {0xac, 0x02},
	}
	for v, want := range cases {
		e := NewEncoder()
		e.WriteVarUint(v)
		if !bytes.Equal(e.Bytes(), want) {
			t.Fatalf("encode %d: got %x want %x", v, e.Bytes(), want)
		}
	}
}

func TestVarUint8ArrayAndString(t *testing.T) {
	e := NewEncoder()
	e.WriteVarUint8Array([]byte{1, 2, 3})
	e.WriteVarString("héllo")
	d := NewDecoder(e.Bytes())
	arr, err := d.VarUint8Array()
	if err != nil || !bytes.Equal(arr, []byte{1, 2, 3}) {
		t.Fatalf("array: %x err %v", arr, err)
	}
	s, err := d.VarString()
	if err != nil || s != "héllo" {
		t.Fatalf("string: %q err %v", s, err)
	}
	if d.Remaining() != 0 {
		t.Fatalf("remaining: %d", d.Remaining())
	}
}

func TestDecoderTruncated(t *testing.T) {
	if _, err := NewDecoder([]byte{0x80}).VarUint(); err == nil {
		t.Fatal("continuation byte at EOF must error")
	}
	if _, err := NewDecoder([]byte{0x05, 1, 2}).VarUint8Array(); err == nil {
		t.Fatal("short array must error")
	}
}

// Package lib0 implements the subset of the lib0 binary encoding that the
// Yjs wire protocols use: variable-length unsigned integers, length-prefixed
// byte arrays, and strings. Encoding matches lib0/encoding.js exactly.
package lib0

import (
	"errors"
	"math/bits"
)

var (
	ErrUnexpectedEOF = errors.New("lib0: unexpected end of buffer")
	ErrOverflow      = errors.New("lib0: varint overflows uint64")
)

// Decoder reads lib0-encoded values from a byte slice.
type Decoder struct {
	buf []byte
	pos int
}

func NewDecoder(buf []byte) *Decoder {
	return &Decoder{buf: buf}
}

func (d *Decoder) Remaining() int {
	return len(d.buf) - d.pos
}

// VarUint reads a 7-bit-group little-endian varint (lib0 readVarUint).
func (d *Decoder) VarUint() (uint64, error) {
	var num uint64
	var shift uint
	for {
		if d.pos >= len(d.buf) {
			return 0, ErrUnexpectedEOF
		}
		b := d.buf[d.pos]
		d.pos++
		if shift >= 64 || (shift == 63 && b > 1) {
			return 0, ErrOverflow
		}
		num |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return num, nil
		}
		shift += 7
	}
}

// VarUint8Array reads a varUint length followed by that many bytes. The
// returned slice aliases the decoder's buffer; copy it to retain it.
func (d *Decoder) VarUint8Array() ([]byte, error) {
	n, err := d.VarUint()
	if err != nil {
		return nil, err
	}
	if uint64(d.Remaining()) < n {
		return nil, ErrUnexpectedEOF
	}
	out := d.buf[d.pos : d.pos+int(n)]
	d.pos += int(n)
	return out, nil
}

// VarString reads a varUint length followed by UTF-8 bytes.
func (d *Decoder) VarString() (string, error) {
	b, err := d.VarUint8Array()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Encoder appends lib0-encoded values to a growing buffer.
type Encoder struct {
	buf []byte
}

func NewEncoder() *Encoder {
	return &Encoder{}
}

func (e *Encoder) Bytes() []byte {
	return e.buf
}

func (e *Encoder) WriteVarUint(v uint64) {
	for v > 0x7f {
		e.buf = append(e.buf, byte(0x80|(v&0x7f)))
		v >>= 7
	}
	e.buf = append(e.buf, byte(v))
}

func (e *Encoder) WriteVarUint8Array(b []byte) {
	e.WriteVarUint(uint64(len(b)))
	e.buf = append(e.buf, b...)
}

func (e *Encoder) WriteVarString(s string) {
	e.WriteVarUint8Array([]byte(s))
}

// VarUintLen returns the encoded size of v, useful for preallocation.
func VarUintLen(v uint64) int {
	if v == 0 {
		return 1
	}
	return (bits.Len64(v) + 6) / 7
}

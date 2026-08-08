package postcard

import "math"

// Writer accumulates bytes in the postcard wire format. Every model type in
// the models package hand-implements an Encode(*Writer) method (there is no
// reflection-based path) so that field order exactly matches the Rust
// aidoku SDK's struct/enum layout.
type Writer struct {
	buf []byte
}

func NewWriter() *Writer {
	return &Writer{}
}

// Bytes returns the accumulated buffer. The Writer must not be used after
// calling Bytes.
func (w *Writer) Bytes() []byte {
	return w.buf
}

func (w *Writer) WriteBool(v bool) {
	if v {
		w.buf = append(w.buf, 1)
	} else {
		w.buf = append(w.buf, 0)
	}
}

func (w *Writer) WriteU8(v uint8) {
	w.buf = append(w.buf, v)
}

func (w *Writer) WriteI8(v int8) {
	w.buf = append(w.buf, uint8(v))
}

func (w *Writer) WriteU16(v uint16) {
	w.writeVarintU64(uint64(v))
}

func (w *Writer) WriteI16(v int16) {
	w.writeVarintU64(uint64(zigzag16(v)))
}

func (w *Writer) WriteU32(v uint32) {
	w.writeVarintU64(uint64(v))
}

func (w *Writer) WriteI32(v int32) {
	w.writeVarintU64(uint64(zigzag32(v)))
}

func (w *Writer) WriteU64(v uint64) {
	w.writeVarintU64(v)
}

func (w *Writer) WriteI64(v int64) {
	w.writeVarintU64(zigzag64(v))
}

// WriteLen writes a sequence/string/map length prefix. Postcard always
// varint-encodes lengths (Rust `usize`) as if they were u64.
func (w *Writer) WriteLen(n int) {
	w.writeVarintU64(uint64(n))
}

func (w *Writer) WriteF32(v float32) {
	bits := math.Float32bits(v)
	w.buf = append(w.buf, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24))
}

func (w *Writer) WriteF64(v float64) {
	bits := math.Float64bits(v)
	w.buf = append(w.buf,
		byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24),
		byte(bits>>32), byte(bits>>40), byte(bits>>48), byte(bits>>56),
	)
}

func (w *Writer) WriteString(v string) {
	w.WriteLen(len(v))
	w.buf = append(w.buf, v...)
}

// WriteBytes writes a length-prefixed byte slice (postcard's Vec<u8>/&[u8]).
func (w *Writer) WriteBytes(b []byte) {
	w.WriteLen(len(b))
	w.buf = append(w.buf, b...)
}

// WriteRaw appends bytes with no framing at all. Only for use by callers
// that already wrote their own length/tag.
func (w *Writer) WriteRaw(b []byte) {
	w.buf = append(w.buf, b...)
}

// WriteNone writes the 0-tag for an absent Option<T>.
func (w *Writer) WriteNone() {
	w.buf = append(w.buf, 0)
}

// WriteSome writes the 1-tag that precedes a present Option<T>'s value.
func (w *Writer) WriteSome() {
	w.buf = append(w.buf, 1)
}

func (w *Writer) writeVarintU64(v uint64) {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			w.buf = append(w.buf, b|0x80)
		} else {
			w.buf = append(w.buf, b)
			return
		}
	}
}

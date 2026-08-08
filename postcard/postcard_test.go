package postcard

import (
	"bytes"
	"math"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	values := []uint64{0, 1, 127, 128, 255, 300, 16384, 1 << 20, 1 << 40, math.MaxUint64}
	for _, v := range values {
		w := NewWriter()
		w.WriteU64(v)
		r := NewReader(w.Bytes())
		got, err := r.ReadU64()
		if err != nil {
			t.Fatalf("ReadU64(%d): %v", v, err)
		}
		if got != v {
			t.Fatalf("ReadU64: want %d got %d", v, got)
		}
		if r.Remaining() != 0 {
			t.Fatalf("ReadU64(%d): %d bytes left over", v, r.Remaining())
		}
	}
}

func TestZigZagSignedRoundTrip(t *testing.T) {
	values := []int64{0, 1, -1, 2, -2, math.MaxInt32, math.MinInt32, math.MaxInt64, math.MinInt64}
	for _, v := range values {
		w := NewWriter()
		w.WriteI64(v)
		r := NewReader(w.Bytes())
		got, err := r.ReadI64()
		if err != nil {
			t.Fatalf("ReadI64(%d): %v", v, err)
		}
		if got != v {
			t.Fatalf("ReadI64: want %d got %d", v, got)
		}
	}
}

// TestKnownEncodings pins the wire format against hand-computed bytes per
// https://postcard.jamesmunns.com/wire-format, to guard against silent
// drift from the Rust format.
func TestKnownEncodings(t *testing.T) {
	// u32(300) -> varint: 300 = 0b1_0010_1100 -> low7=0101100(0x2C)|cont,
	// next7=0000010(0x02) => bytes [0xAC, 0x02]
	w := NewWriter()
	w.WriteU32(300)
	if !bytes.Equal(w.Bytes(), []byte{0xAC, 0x02}) {
		t.Fatalf("u32(300) = % x, want ac 02", w.Bytes())
	}

	// i32(-1) zigzags to 1 -> single byte 0x01
	w = NewWriter()
	w.WriteI32(-1)
	if !bytes.Equal(w.Bytes(), []byte{0x01}) {
		t.Fatalf("i32(-1) = % x, want 01", w.Bytes())
	}

	// i32(1) zigzags to 2 -> single byte 0x02
	w = NewWriter()
	w.WriteI32(1)
	if !bytes.Equal(w.Bytes(), []byte{0x02}) {
		t.Fatalf("i32(1) = % x, want 02", w.Bytes())
	}

	// bool
	w = NewWriter()
	w.WriteBool(true)
	w.WriteBool(false)
	if !bytes.Equal(w.Bytes(), []byte{0x01, 0x00}) {
		t.Fatalf("bools = % x, want 01 00", w.Bytes())
	}

	// string "hi" -> len(2) varint + bytes
	w = NewWriter()
	w.WriteString("hi")
	if !bytes.Equal(w.Bytes(), []byte{0x02, 'h', 'i'}) {
		t.Fatalf("string(hi) = % x, want 02 68 69", w.Bytes())
	}
}

func TestOptionRoundTrip(t *testing.T) {
	w := NewWriter()
	w.WriteNone()

	w2 := NewWriter()
	w2.WriteSome()
	w2.WriteString("value")

	r := NewReader(w.Bytes())
	present, err := r.ReadOptionTag()
	if err != nil || present {
		t.Fatalf("expected absent option, got present=%v err=%v", present, err)
	}

	r2 := NewReader(w2.Bytes())
	present2, err := r2.ReadOptionTag()
	if err != nil || !present2 {
		t.Fatalf("expected present option, got present=%v err=%v", present2, err)
	}
	s, err := r2.ReadString()
	if err != nil || s != "value" {
		t.Fatalf("expected \"value\", got %q err=%v", s, err)
	}
}

func TestFloatRoundTrip(t *testing.T) {
	w := NewWriter()
	w.WriteF32(3.14159)
	w.WriteF64(2.718281828459045)

	r := NewReader(w.Bytes())
	f32, err := r.ReadF32()
	if err != nil || f32 != float32(3.14159) {
		t.Fatalf("f32 round trip: %v err=%v", f32, err)
	}
	f64, err := r.ReadF64()
	if err != nil || f64 != 2.718281828459045 {
		t.Fatalf("f64 round trip: %v err=%v", f64, err)
	}
}

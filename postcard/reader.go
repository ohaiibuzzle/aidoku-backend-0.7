package postcard

import (
	"errors"
	"math"
)

var (
	// ErrUnexpectedEOF is returned when the buffer runs out of bytes
	// mid-value.
	ErrUnexpectedEOF = errors.New("postcard: unexpected end of buffer")
	// ErrVarintOverflow is returned when a varint doesn't terminate within
	// the maximum number of bytes for its target width.
	ErrVarintOverflow = errors.New("postcard: varint overflow")
	// ErrInvalidBool is returned when a bool byte is neither 0 nor 1.
	ErrInvalidBool = errors.New("postcard: invalid bool byte")
)

// Reader consumes bytes in the postcard wire format. Every model type in
// the models package hand-implements a Decode(*Reader) method mirroring its
// Encode counterpart.
type Reader struct {
	buf []byte
	pos int
}

func NewReader(b []byte) *Reader {
	return &Reader{buf: b}
}

// Remaining reports how many unread bytes are left in the buffer.
func (r *Reader) Remaining() int {
	return len(r.buf) - r.pos
}

func (r *Reader) readByte() (byte, error) {
	if r.pos >= len(r.buf) {
		return 0, ErrUnexpectedEOF
	}
	b := r.buf[r.pos]
	r.pos++
	return b, nil
}

func (r *Reader) ReadBool() (bool, error) {
	b, err := r.readByte()
	if err != nil {
		return false, err
	}
	switch b {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, ErrInvalidBool
	}
}

func (r *Reader) ReadU8() (uint8, error) {
	return r.readByte()
}

func (r *Reader) ReadI8() (int8, error) {
	b, err := r.readByte()
	return int8(b), err
}

func (r *Reader) ReadU16() (uint16, error) {
	v, err := r.readVarintU64(3)
	return uint16(v), err
}

func (r *Reader) ReadI16() (int16, error) {
	v, err := r.readVarintU64(3)
	return unzigzag16(uint16(v)), err
}

func (r *Reader) ReadU32() (uint32, error) {
	v, err := r.readVarintU64(5)
	return uint32(v), err
}

func (r *Reader) ReadI32() (int32, error) {
	v, err := r.readVarintU64(5)
	return unzigzag32(uint32(v)), err
}

func (r *Reader) ReadU64() (uint64, error) {
	return r.readVarintU64(10)
}

func (r *Reader) ReadI64() (int64, error) {
	v, err := r.readVarintU64(10)
	return unzigzag64(v), err
}

// ReadLen reads a sequence/string/map length prefix.
func (r *Reader) ReadLen() (int, error) {
	v, err := r.readVarintU64(10)
	return int(v), err
}

func (r *Reader) ReadF32() (float32, error) {
	if r.Remaining() < 4 {
		return 0, ErrUnexpectedEOF
	}
	b := r.buf[r.pos : r.pos+4]
	r.pos += 4
	bits := uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
	return math.Float32frombits(bits), nil
}

func (r *Reader) ReadF64() (float64, error) {
	if r.Remaining() < 8 {
		return 0, ErrUnexpectedEOF
	}
	b := r.buf[r.pos : r.pos+8]
	r.pos += 8
	bits := uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
	return math.Float64frombits(bits), nil
}

func (r *Reader) ReadString() (string, error) {
	n, err := r.ReadLen()
	if err != nil {
		return "", err
	}
	if n < 0 || r.Remaining() < n {
		return "", ErrUnexpectedEOF
	}
	s := string(r.buf[r.pos : r.pos+n])
	r.pos += n
	return s, nil
}

// ReadBytes reads a length-prefixed byte slice (postcard's Vec<u8>/&[u8]).
func (r *Reader) ReadBytes() ([]byte, error) {
	n, err := r.ReadLen()
	if err != nil {
		return nil, err
	}
	if n < 0 || r.Remaining() < n {
		return nil, ErrUnexpectedEOF
	}
	b := make([]byte, n)
	copy(b, r.buf[r.pos:r.pos+n])
	r.pos += n
	return b, nil
}

// ReadRaw reads exactly n unframed bytes.
func (r *Reader) ReadRaw(n int) ([]byte, error) {
	if n < 0 || r.Remaining() < n {
		return nil, ErrUnexpectedEOF
	}
	b := r.buf[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}

// ReadOptionTag reads an Option<T> presence byte: true means a value
// follows.
func (r *Reader) ReadOptionTag() (bool, error) {
	return r.ReadBool()
}

func (r *Reader) readVarintU64(maxBytes int) (uint64, error) {
	var result uint64
	var shift uint
	for i := 0; i < maxBytes; i++ {
		b, err := r.readByte()
		if err != nil {
			return 0, err
		}
		result |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return result, nil
		}
		shift += 7
	}
	return 0, ErrVarintOverflow
}

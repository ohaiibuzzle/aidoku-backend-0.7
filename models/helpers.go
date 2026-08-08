package models

import (
	"time"

	"github.com/ohaiibuzzle/aidokurunner-go/postcard"
)

// decodeOptionalEpoch/encodeOptionalEpoch mirror Swift's @EpochDate
// property wrapper: Option<i64> unix-seconds on the wire.
func decodeOptionalEpoch(r *postcard.Reader) (*time.Time, error) {
	present, err := r.ReadOptionTag()
	if err != nil || !present {
		return nil, err
	}
	sec, err := r.ReadI64()
	if err != nil {
		return nil, err
	}
	t := time.Unix(sec, 0).UTC()
	return &t, nil
}

func encodeOptionalEpoch(w *postcard.Writer, t *time.Time) {
	if t == nil {
		w.WriteNone()
		return
	}
	w.WriteSome()
	w.WriteI64(t.Unix())
}

// This file holds small Encode/Decode helpers shared across model types for
// Option<T> and Vec<T> of primitive types. Every model hand-implements its
// own EncodePostcard/DecodePostcard (no reflection) so that field order
// exactly matches the Rust aidoku SDK's struct/enum layout; these helpers
// just keep that code from repeating the same Option/Vec framing everywhere.

func decodeOptionalString(r *postcard.Reader) (*string, error) {
	present, err := r.ReadOptionTag()
	if err != nil || !present {
		return nil, err
	}
	s, err := r.ReadString()
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func encodeOptionalString(w *postcard.Writer, s *string) {
	if s == nil {
		w.WriteNone()
		return
	}
	w.WriteSome()
	w.WriteString(*s)
}

func decodeOptionalBool(r *postcard.Reader) (*bool, error) {
	present, err := r.ReadOptionTag()
	if err != nil || !present {
		return nil, err
	}
	v, err := r.ReadBool()
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func encodeOptionalBool(w *postcard.Writer, v *bool) {
	if v == nil {
		w.WriteNone()
		return
	}
	w.WriteSome()
	w.WriteBool(*v)
}

func decodeOptionalI64(r *postcard.Reader) (*int64, error) {
	present, err := r.ReadOptionTag()
	if err != nil || !present {
		return nil, err
	}
	v, err := r.ReadI64()
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func encodeOptionalI64(w *postcard.Writer, v *int64) {
	if v == nil {
		w.WriteNone()
		return
	}
	w.WriteSome()
	w.WriteI64(*v)
}

func decodeOptionalI32(r *postcard.Reader) (*int32, error) {
	present, err := r.ReadOptionTag()
	if err != nil || !present {
		return nil, err
	}
	v, err := r.ReadI32()
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func encodeOptionalI32(w *postcard.Writer, v *int32) {
	if v == nil {
		w.WriteNone()
		return
	}
	w.WriteSome()
	w.WriteI32(*v)
}

func decodeOptionalF32(r *postcard.Reader) (*float32, error) {
	present, err := r.ReadOptionTag()
	if err != nil || !present {
		return nil, err
	}
	v, err := r.ReadF32()
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func encodeOptionalF32(w *postcard.Writer, v *float32) {
	if v == nil {
		w.WriteNone()
		return
	}
	w.WriteSome()
	w.WriteF32(*v)
}

func decodeOptionalF64(r *postcard.Reader) (*float64, error) {
	present, err := r.ReadOptionTag()
	if err != nil || !present {
		return nil, err
	}
	v, err := r.ReadF64()
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func encodeOptionalF64(w *postcard.Writer, v *float64) {
	if v == nil {
		w.WriteNone()
		return
	}
	w.WriteSome()
	w.WriteF64(*v)
}

func decodeStringSlice(r *postcard.Reader) ([]string, error) {
	n, err := r.ReadLen()
	if err != nil {
		return nil, err
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		if out[i], err = r.ReadString(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func encodeStringSlice(w *postcard.Writer, s []string) {
	w.WriteLen(len(s))
	for _, v := range s {
		w.WriteString(v)
	}
}

func decodeOptionalStringSlice(r *postcard.Reader) ([]string, error) {
	present, err := r.ReadOptionTag()
	if err != nil || !present {
		return nil, err
	}
	return decodeStringSlice(r)
}

func encodeOptionalStringSlice(w *postcard.Writer, s []string) {
	if s == nil {
		w.WriteNone()
		return
	}
	w.WriteSome()
	encodeStringSlice(w, s)
}

func decodeOptionalStringMap(r *postcard.Reader) (map[string]string, error) {
	present, err := r.ReadOptionTag()
	if err != nil || !present {
		return nil, err
	}
	return decodeStringMap(r)
}

func encodeOptionalStringMap(w *postcard.Writer, m map[string]string) {
	if m == nil {
		w.WriteNone()
		return
	}
	w.WriteSome()
	encodeStringMap(w, m)
}

func decodeStringMap(r *postcard.Reader) (map[string]string, error) {
	n, err := r.ReadLen()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, n)
	for i := 0; i < n; i++ {
		k, err := r.ReadString()
		if err != nil {
			return nil, err
		}
		v, err := r.ReadString()
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

func encodeStringMap(w *postcard.Writer, m map[string]string) {
	w.WriteLen(len(m))
	for k, v := range m {
		w.WriteString(k)
		w.WriteString(v)
	}
}

package models

import "github.com/ohaiibuzzle/aidokurunner-go/postcard"

type FilterValueKind uint8

const (
	FilterValueKindText FilterValueKind = iota
	FilterValueKindSort
	FilterValueKindCheck
	FilterValueKindSelect
	FilterValueKindMultiselect
	FilterValueKindRange
)

// FilterValue mirrors AidokuRunner's FilterValue enum (FilterValue.swift).
// Only the fields relevant to Kind are populated. Sent host->guest when
// searching/filtering, and appears nested inside Home's FilterItem.
type FilterValue struct {
	Kind FilterValueKind
	ID   string

	// Kind == Text || Kind == Select
	Value string

	// Kind == Sort
	SortIndex     int32
	SortAscending bool

	// Kind == Check (Swift's generic `Int` -> i64 on the wire)
	CheckValue int64

	// Kind == Multiselect
	Included []string
	Excluded []string

	// Kind == Range
	From *float32
	To   *float32
}

func (f *FilterValue) DecodePostcard(r *postcard.Reader) error {
	kind, err := r.ReadU8()
	if err != nil {
		return err
	}
	f.Kind = FilterValueKind(kind)
	if f.ID, err = r.ReadString(); err != nil {
		return err
	}
	switch f.Kind {
	case FilterValueKindText, FilterValueKindSelect:
		if f.Value, err = r.ReadString(); err != nil {
			return err
		}
	case FilterValueKindSort:
		if f.SortIndex, err = r.ReadI32(); err != nil {
			return err
		}
		if f.SortAscending, err = r.ReadBool(); err != nil {
			return err
		}
	case FilterValueKindCheck:
		if f.CheckValue, err = r.ReadI64(); err != nil {
			return err
		}
	case FilterValueKindMultiselect:
		if f.Included, err = decodeStringSlice(r); err != nil {
			return err
		}
		if f.Excluded, err = decodeStringSlice(r); err != nil {
			return err
		}
	case FilterValueKindRange:
		if f.From, err = decodeOptionalF32(r); err != nil {
			return err
		}
		if f.To, err = decodeOptionalF32(r); err != nil {
			return err
		}
	}
	return nil
}

func (f *FilterValue) EncodePostcard(w *postcard.Writer) {
	w.WriteU8(uint8(f.Kind))
	w.WriteString(f.ID)
	switch f.Kind {
	case FilterValueKindText, FilterValueKindSelect:
		w.WriteString(f.Value)
	case FilterValueKindSort:
		w.WriteI32(f.SortIndex)
		w.WriteBool(f.SortAscending)
	case FilterValueKindCheck:
		w.WriteI64(f.CheckValue)
	case FilterValueKindMultiselect:
		encodeStringSlice(w, f.Included)
		encodeStringSlice(w, f.Excluded)
	case FilterValueKindRange:
		encodeOptionalF32(w, f.From)
		encodeOptionalF32(w, f.To)
	}
}

// EncodeFilterValueSlice/DecodeFilterValueSlice handle the []FilterValue
// framing used when sending search filters and decoding Home FilterItems.
func EncodeFilterValueSlice(w *postcard.Writer, values []FilterValue) {
	w.WriteLen(len(values))
	for i := range values {
		values[i].EncodePostcard(w)
	}
}

func DecodeFilterValueSlice(r *postcard.Reader) ([]FilterValue, error) {
	n, err := r.ReadLen()
	if err != nil {
		return nil, err
	}
	out := make([]FilterValue, n)
	for i := 0; i < n; i++ {
		if err := out[i].DecodePostcard(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

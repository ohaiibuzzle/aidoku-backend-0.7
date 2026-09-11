package models

import (
	"encoding/json"
	"fmt"

	"github.com/ohaiibuzzle/aidokurunner-go/postcard"
)

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
	default:
		return fmt.Errorf("postcard: unknown filter value kind %d", kind)
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

// --- JSON ---
// Used by cmd/aidoku-run's `search` command to accept filter selections
// from a caller (the KOReader plugin's filter picker) as a JSON file, using
// the same string vocabulary as FilterKind's own JSON (models/filter.go)
// rather than leaking FilterValueKind's iota ordering as an implicit,
// silently-breakable wire contract.

func (k FilterValueKind) jsonType() string {
	switch k {
	case FilterValueKindText:
		return "text"
	case FilterValueKindSort:
		return "sort"
	case FilterValueKindCheck:
		return "check"
	case FilterValueKindSelect:
		return "select"
	case FilterValueKindMultiselect:
		return "multi-select"
	case FilterValueKindRange:
		return "range"
	default:
		return ""
	}
}

func filterValueKindFromJSONType(s string) (FilterValueKind, error) {
	switch s {
	case "text":
		return FilterValueKindText, nil
	case "sort":
		return FilterValueKindSort, nil
	case "check":
		return FilterValueKindCheck, nil
	case "select":
		return FilterValueKindSelect, nil
	case "multi-select":
		return FilterValueKindMultiselect, nil
	case "range":
		return FilterValueKindRange, nil
	default:
		return 0, fmt.Errorf("models: unknown filter value type %q", s)
	}
}

type filterValueJSON struct {
	ID            string   `json:"id"`
	Type          string   `json:"type"`
	Value         *string  `json:"value,omitempty"`
	SortIndex     *int32   `json:"sortIndex,omitempty"`
	SortAscending *bool    `json:"sortAscending,omitempty"`
	CheckValue    *int64   `json:"checkValue,omitempty"`
	Included      []string `json:"included,omitempty"`
	Excluded      []string `json:"excluded,omitempty"`
	From          *float32 `json:"from,omitempty"`
	To            *float32 `json:"to,omitempty"`
}

func (f FilterValue) MarshalJSON() ([]byte, error) {
	raw := filterValueJSON{ID: f.ID, Type: f.Kind.jsonType()}
	switch f.Kind {
	case FilterValueKindText, FilterValueKindSelect:
		raw.Value = &f.Value
	case FilterValueKindSort:
		raw.SortIndex = &f.SortIndex
		raw.SortAscending = &f.SortAscending
	case FilterValueKindCheck:
		raw.CheckValue = &f.CheckValue
	case FilterValueKindMultiselect:
		raw.Included = f.Included
		raw.Excluded = f.Excluded
	case FilterValueKindRange:
		raw.From = f.From
		raw.To = f.To
	}
	return json.Marshal(raw)
}

func (f *FilterValue) UnmarshalJSON(data []byte) error {
	var raw filterValueJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	kind, err := filterValueKindFromJSONType(raw.Type)
	if err != nil {
		return err
	}
	f.Kind = kind
	f.ID = raw.ID
	switch kind {
	case FilterValueKindText, FilterValueKindSelect:
		if raw.Value != nil {
			f.Value = *raw.Value
		}
	case FilterValueKindSort:
		if raw.SortIndex != nil {
			f.SortIndex = *raw.SortIndex
		}
		if raw.SortAscending != nil {
			f.SortAscending = *raw.SortAscending
		}
	case FilterValueKindCheck:
		if raw.CheckValue != nil {
			f.CheckValue = *raw.CheckValue
		}
	case FilterValueKindMultiselect:
		f.Included = raw.Included
		f.Excluded = raw.Excluded
	case FilterValueKindRange:
		f.From = raw.From
		f.To = raw.To
	}
	return nil
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

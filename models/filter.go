package models

import (
	"encoding/json"
	"fmt"

	"github.com/ohaiibuzzle/aidokurunner-go/postcard"
)

type FilterKind string

const (
	FilterKindText        FilterKind = "text"
	FilterKindSort        FilterKind = "sort"
	FilterKindCheck       FilterKind = "check"
	FilterKindSelect      FilterKind = "select"
	FilterKindMultiselect FilterKind = "multi-select"
	FilterKindNote        FilterKind = "note"
	FilterKindRange       FilterKind = "range"
)

// SortDefault mirrors Filter.SortDefault. Always two required fields on
// the wire (no Option wrapping), even though `ascending` is decoded with a
// `try?`-and-default-false fallback in Swift.
type SortDefault struct {
	Index     int64
	Ascending bool
}

func (s *SortDefault) DecodePostcard(r *postcard.Reader) error {
	var err error
	if s.Index, err = r.ReadI64(); err != nil {
		return err
	}
	if s.Ascending, err = r.ReadBool(); err != nil {
		return err
	}
	return nil
}

func (s *SortDefault) EncodePostcard(w *postcard.Writer) {
	w.WriteI64(s.Index)
	w.WriteBool(s.Ascending)
}

type SelectFilter struct {
	IsGenre      bool
	UsesTagStyle bool
	Options      []string
	IDs          []string
	DefaultValue *string
}

type MultiSelectFilter struct {
	IsGenre         bool
	CanExclude      bool
	UsesTagStyle    bool
	Options         []string
	IDs             []string
	DefaultIncluded []string
	DefaultExcluded []string
}

// Filter mirrors AidokuRunner's Filter.swift. Its Value fields are a manual
// tagged union in Swift reused verbatim for both JSON (filters.json) and
// postcard (dynamic get_filters) — only the fields relevant to Kind are
// populated here.
type Filter struct {
	ID             string
	Title          *string
	HideFromHeader *bool
	Kind           FilterKind

	// Kind == Text
	Placeholder *string

	// Kind == Sort
	CanAscend   bool
	SortOptions []string
	SortDefault *SortDefault

	// Kind == Check
	CheckName         *string
	CheckCanExclude   bool
	CheckDefaultValue *bool

	// Kind == Select
	Select SelectFilter

	// Kind == Multiselect
	MultiSelect MultiSelectFilter

	// Kind == Note
	Note string

	// Kind == Range
	RangeMin     *float32
	RangeMax     *float32
	RangeDecimal bool
}

// --- Postcard ---
// Field order matches Filter's Codable init(from:)/encode(to:) driven
// positionally (postcard ignores CodingKeys, just reads/writes in call
// order).

func (f *Filter) DecodePostcard(r *postcard.Reader) error {
	rawID, err := decodeOptionalString(r)
	if err != nil {
		return err
	}
	if f.Title, err = decodeOptionalString(r); err != nil {
		return err
	}
	if f.HideFromHeader, err = decodeOptionalBool(r); err != nil {
		return err
	}
	typeStr, err := r.ReadString()
	if err != nil {
		return err
	}
	f.Kind = FilterKind(typeStr)
	// Swift: `self.id = id ?? title ?? type`
	switch {
	case rawID != nil:
		f.ID = *rawID
	case f.Title != nil:
		f.ID = *f.Title
	default:
		f.ID = typeStr
	}

	switch f.Kind {
	case FilterKindText:
		if f.Placeholder, err = decodeOptionalString(r); err != nil {
			return err
		}
	case FilterKindSort:
		canAscend, err := decodeOptionalBool(r)
		if err != nil {
			return err
		}
		f.CanAscend = canAscend == nil || *canAscend
		if f.SortOptions, err = decodeStringSlice(r); err != nil {
			return err
		}
		present, err := r.ReadOptionTag()
		if err != nil {
			return err
		}
		if present {
			f.SortDefault = &SortDefault{}
			if err := f.SortDefault.DecodePostcard(r); err != nil {
				return err
			}
		}
	case FilterKindCheck:
		if f.CheckName, err = decodeOptionalString(r); err != nil {
			return err
		}
		canExclude, err := decodeOptionalBool(r)
		if err != nil {
			return err
		}
		f.CheckCanExclude = canExclude != nil && *canExclude
		if f.CheckDefaultValue, err = decodeOptionalBool(r); err != nil {
			return err
		}
	case FilterKindSelect:
		if err := f.Select.decodePostcard(r); err != nil {
			return err
		}
	case FilterKindMultiselect:
		if err := f.MultiSelect.decodePostcard(r); err != nil {
			return err
		}
	case FilterKindNote:
		if f.Note, err = r.ReadString(); err != nil {
			return err
		}
	case FilterKindRange:
		if f.RangeMin, err = decodeOptionalF32(r); err != nil {
			return err
		}
		if f.RangeMax, err = decodeOptionalF32(r); err != nil {
			return err
		}
		decimal, err := decodeOptionalBool(r)
		if err != nil {
			return err
		}
		f.RangeDecimal = decimal != nil && *decimal
	default:
		return fmt.Errorf("postcard: unknown filter type %q", typeStr)
	}
	return nil
}

func (s *SelectFilter) decodePostcard(r *postcard.Reader) error {
	isGenre, err := decodeOptionalBool(r)
	if err != nil {
		return err
	}
	s.IsGenre = isGenre != nil && *isGenre
	usesTagStyle, err := decodeOptionalBool(r)
	if err != nil {
		return err
	}
	s.UsesTagStyle = usesTagStyle != nil && *usesTagStyle || (usesTagStyle == nil && s.IsGenre)
	if s.Options, err = decodeStringSlice(r); err != nil {
		return err
	}
	if s.IDs, err = decodeOptionalStringSlice(r); err != nil {
		return err
	}
	if s.DefaultValue, err = decodeOptionalString(r); err != nil {
		return err
	}
	return nil
}

func (m *MultiSelectFilter) decodePostcard(r *postcard.Reader) error {
	isGenre, err := decodeOptionalBool(r)
	if err != nil {
		return err
	}
	m.IsGenre = isGenre != nil && *isGenre
	canExclude, err := decodeOptionalBool(r)
	if err != nil {
		return err
	}
	m.CanExclude = canExclude != nil && *canExclude
	usesTagStyle, err := decodeOptionalBool(r)
	if err != nil {
		return err
	}
	m.UsesTagStyle = usesTagStyle != nil && *usesTagStyle || (usesTagStyle == nil && m.IsGenre)
	if m.Options, err = decodeStringSlice(r); err != nil {
		return err
	}
	if m.IDs, err = decodeOptionalStringSlice(r); err != nil {
		return err
	}
	if m.DefaultIncluded, err = decodeOptionalStringSlice(r); err != nil {
		return err
	}
	if m.DefaultExcluded, err = decodeOptionalStringSlice(r); err != nil {
		return err
	}
	return nil
}

func DecodeFilterSlice(r *postcard.Reader) ([]Filter, error) {
	n, err := r.ReadLen()
	if err != nil {
		return nil, err
	}
	out := make([]Filter, n)
	for i := 0; i < n; i++ {
		if err := out[i].DecodePostcard(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// --- JSON (filters.json) ---

func (f *Filter) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID              *string         `json:"id"`
		Title           *string         `json:"title"`
		HideFromHeader  *bool           `json:"hideFromHeader"`
		Type            string          `json:"type"`
		Placeholder     *string         `json:"placeholder"`
		CanAscend       *bool           `json:"canAscend"`
		Options         []string        `json:"options"`
		DefaultValue    json.RawMessage `json:"default"`
		Name            *string         `json:"name"`
		CanExclude      *bool           `json:"canExclude"`
		IsGenre         *bool           `json:"isGenre"`
		UsesTagStyle    *bool           `json:"usesTagStyle"`
		IDs             []string        `json:"ids"`
		DefaultIncluded []string        `json:"defaultIncluded"`
		DefaultExcluded []string        `json:"defaultExcluded"`
		Text            *string         `json:"text"`
		Min             *float32        `json:"min"`
		Max             *float32        `json:"max"`
		Decimal         *bool           `json:"decimal"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	f.Title = raw.Title
	f.HideFromHeader = raw.HideFromHeader
	if raw.ID != nil {
		f.ID = *raw.ID
	} else if raw.Title != nil {
		f.ID = *raw.Title
	} else {
		f.ID = raw.Type
	}
	f.Kind = FilterKind(raw.Type)

	switch f.Kind {
	case FilterKindText:
		f.Placeholder = raw.Placeholder
	case FilterKindSort:
		f.CanAscend = raw.CanAscend == nil || *raw.CanAscend
		f.SortOptions = raw.Options
		if len(raw.DefaultValue) > 0 && string(raw.DefaultValue) != "null" {
			var sd struct {
				Index     int64 `json:"index"`
				Ascending bool  `json:"ascending"`
			}
			if err := json.Unmarshal(raw.DefaultValue, &sd); err != nil {
				return err
			}
			f.SortDefault = &SortDefault{Index: sd.Index, Ascending: sd.Ascending}
		}
	case FilterKindCheck:
		f.CheckName = raw.Name
		f.CheckCanExclude = raw.CanExclude != nil && *raw.CanExclude
		if len(raw.DefaultValue) > 0 && string(raw.DefaultValue) != "null" {
			var b bool
			if err := json.Unmarshal(raw.DefaultValue, &b); err != nil {
				return err
			}
			f.CheckDefaultValue = &b
		}
	case FilterKindSelect:
		f.Select.IsGenre = raw.IsGenre != nil && *raw.IsGenre
		f.Select.UsesTagStyle = boolOrDefault(raw.UsesTagStyle, f.Select.IsGenre)
		f.Select.Options = raw.Options
		f.Select.IDs = raw.IDs
		if len(raw.DefaultValue) > 0 && string(raw.DefaultValue) != "null" {
			var s string
			if err := json.Unmarshal(raw.DefaultValue, &s); err != nil {
				return err
			}
			f.Select.DefaultValue = &s
		}
	case FilterKindMultiselect:
		f.MultiSelect.IsGenre = raw.IsGenre != nil && *raw.IsGenre
		f.MultiSelect.CanExclude = raw.CanExclude != nil && *raw.CanExclude
		f.MultiSelect.UsesTagStyle = boolOrDefault(raw.UsesTagStyle, f.MultiSelect.IsGenre)
		f.MultiSelect.Options = raw.Options
		f.MultiSelect.IDs = raw.IDs
		f.MultiSelect.DefaultIncluded = raw.DefaultIncluded
		f.MultiSelect.DefaultExcluded = raw.DefaultExcluded
	case FilterKindNote:
		if raw.Text != nil {
			f.Note = *raw.Text
		}
	case FilterKindRange:
		f.RangeMin = raw.Min
		f.RangeMax = raw.Max
		f.RangeDecimal = raw.Decimal != nil && *raw.Decimal
	default:
		return fmt.Errorf("models: unknown filter type %q", raw.Type)
	}
	return nil
}

func boolOrDefault(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

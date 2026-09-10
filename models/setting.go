package models

import (
	"encoding/json"
	"fmt"

	"github.com/ohaiibuzzle/aidokurunner-go/postcard"
)

type SettingType string

const (
	SettingTypeGroup        SettingType = "group"
	SettingTypeSelect       SettingType = "select"
	SettingTypeMultiselect  SettingType = "multi-select"
	SettingTypeToggle       SettingType = "switch"
	SettingTypeStepper      SettingType = "stepper"
	SettingTypeSegment      SettingType = "segment"
	SettingTypeText         SettingType = "text"
	SettingTypeButton       SettingType = "button"
	SettingTypeLink         SettingType = "link"
	SettingTypeLogin        SettingType = "login"
	SettingTypePage         SettingType = "page"
	SettingTypeEditableList SettingType = "editable-list"
	SettingTypePicker       SettingType = "picker"
	SettingTypeCustom       SettingType = "custom"
)

func (t SettingType) byteValue() uint8 {
	switch t {
	case SettingTypeGroup:
		return 0
	case SettingTypeSelect:
		return 1
	case SettingTypeMultiselect:
		return 2
	case SettingTypeToggle:
		return 3
	case SettingTypeStepper:
		return 4
	case SettingTypeSegment:
		return 5
	case SettingTypeText:
		return 6
	case SettingTypeButton:
		return 7
	case SettingTypeLink:
		return 8
	case SettingTypeLogin:
		return 9
	case SettingTypePage:
		return 10
	case SettingTypeEditableList:
		return 11
	case SettingTypeCustom:
		return 12
	case SettingTypePicker:
		return 13
	default:
		return 0
	}
}

// Setting mirrors AidokuRunner's Setting.swift, dual-purpose (JSON
// settings.json manifest + postcard-decoded dynamic get_settings). Only
// fields relevant to Type are populated.
//
// NOTE on `Key`: Swift's Setting.encode(to:) encodes `key` through
// Optional's stdlib Encodable conformance rather than this codebase's usual
// tag+value Option encoding, and we couldn't verify byte-for-byte whether
// that double-consumes a presence marker against this decoder. Key is
// treated as a plain required postcard string (empty and "absent" are
// indistinguishable anyway) -- UNVERIFIED, validate against a real
// compiled source with a dynamic, keyed setting before shipping.
type Setting struct {
	Key           string
	Title         string
	Notification  *string
	Requires      *string
	RequiresFalse *string
	Refreshes     []string
	Type          SettingType

	Group        *GroupSetting
	Select       *SelectSetting
	Multiselect  *MultiSelectSetting
	Toggle       *ToggleSetting
	Stepper      *StepperSetting
	Segment      *SegmentSetting
	Text         *TextSetting
	Button       *ButtonSetting
	Link         *LinkSetting
	Login        *LoginSetting
	Page         *PageSetting
	EditableList *EditableListSetting
	Picker       *PickerSetting
}

type GroupSetting struct {
	Footer *string
	Items  []Setting
}

type SelectSetting struct {
	Values       []string
	Titles       []string
	AuthToOpen   *bool
	DefaultValue *string
}

type MultiSelectSetting struct {
	Values       []string
	Titles       []string
	AuthToOpen   *bool
	DefaultValue []string
}

type ToggleSetting struct {
	Subtitle      *string
	AuthToDisable *bool
	DefaultValue  bool
}

type StepperSetting struct {
	MinimumValue float64
	MaximumValue float64
	StepValue    *float64
	DefaultValue *float64
}

type SegmentSetting struct {
	Options      []string
	DefaultValue *int64
}

type TextSetting struct {
	Placeholder            *string
	AutocapitalizationType *int64
	KeyboardType           *int64
	ReturnKeyType          *int64
	AutocorrectionDisabled *bool
	Secure                 *bool
	DefaultValue           *string
}

type ButtonSetting struct {
	Destructive  *bool
	ConfirmTitle *string
	ConfirmText  *string
}

type LinkSetting struct {
	URL      string
	External *bool
}

type LoginMethod string

const (
	LoginMethodBasic LoginMethod = "basic"
	LoginMethodOAuth LoginMethod = "oauth"
	LoginMethodWeb   LoginMethod = "web"
)

type LoginSetting struct {
	Method           LoginMethod
	URL              *string
	URLKey           *string
	LogoutTitle      *string
	PKCE             *bool
	TokenURL         *string
	CallbackScheme   *string
	UseEmail         *bool
	LocalStorageKeys []string
}

type PageSettingIconKind uint8

const (
	PageSettingIconKindSystem PageSettingIconKind = iota
	PageSettingIconKindURL
)

type PageSettingIcon struct {
	Kind  PageSettingIconKind
	Name  string
	Color string
	Inset int64
	URL   string
}

type PageSetting struct {
	Items       []Setting
	InlineTitle *bool
	AuthToOpen  *bool
	Icon        *PageSettingIcon
	Info        *string
}

type EditableListSetting struct {
	LineLimit    *int64
	Inline       *bool
	Placeholder  *string
	DefaultValue []string
}

type PickerSetting struct {
	Values       []string
	Titles       []string
	DefaultValue *string
}

// --- Postcard ---

func (s *Setting) DecodePostcard(r *postcard.Reader) error {
	typeStr, err := r.ReadString()
	if err != nil {
		return err
	}
	s.Type = SettingType(typeStr)
	if s.Key, err = r.ReadString(); err != nil {
		return err
	}
	if s.Title, err = r.ReadString(); err != nil {
		return err
	}
	if s.Notification, err = decodeOptionalString(r); err != nil {
		return err
	}
	if s.Requires, err = decodeOptionalString(r); err != nil {
		return err
	}
	if s.RequiresFalse, err = decodeOptionalString(r); err != nil {
		return err
	}
	present, err := r.ReadOptionTag()
	if err != nil {
		return err
	}
	if present {
		if s.Refreshes, err = decodeStringSlice(r); err != nil {
			return err
		}
	}
	if _, err = r.ReadU8(); err != nil { // enumValue, discarded (mirrors Swift's `_ = try? ...`)
		return err
	}

	switch s.Type {
	case SettingTypeGroup:
		v := &GroupSetting{}
		if v.Footer, err = decodeOptionalString(r); err != nil {
			return err
		}
		n, err := r.ReadLen()
		if err != nil {
			return err
		}
		v.Items = make([]Setting, n)
		for i := 0; i < n; i++ {
			if err := v.Items[i].DecodePostcard(r); err != nil {
				return err
			}
		}
		s.Group = v
	case SettingTypeSelect:
		v := &SelectSetting{}
		if v.Values, err = decodeStringSlice(r); err != nil {
			return err
		}
		if v.Titles, err = decodeOptionalStringSlice(r); err != nil {
			return err
		}
		if v.AuthToOpen, err = decodeOptionalBool(r); err != nil {
			return err
		}
		if v.DefaultValue, err = decodeOptionalString(r); err != nil {
			return err
		}
		s.Select = v
	case SettingTypeMultiselect:
		v := &MultiSelectSetting{}
		if v.Values, err = decodeStringSlice(r); err != nil {
			return err
		}
		if v.Titles, err = decodeOptionalStringSlice(r); err != nil {
			return err
		}
		if v.AuthToOpen, err = decodeOptionalBool(r); err != nil {
			return err
		}
		if v.DefaultValue, err = decodeOptionalStringSlice(r); err != nil {
			return err
		}
		s.Multiselect = v
	case SettingTypeToggle:
		v := &ToggleSetting{}
		if v.Subtitle, err = decodeOptionalString(r); err != nil {
			return err
		}
		if v.AuthToDisable, err = decodeOptionalBool(r); err != nil {
			return err
		}
		if v.DefaultValue, err = r.ReadBool(); err != nil {
			return err
		}
		s.Toggle = v
	case SettingTypeStepper:
		v := &StepperSetting{}
		if v.MinimumValue, err = r.ReadF64(); err != nil {
			return err
		}
		if v.MaximumValue, err = r.ReadF64(); err != nil {
			return err
		}
		if v.StepValue, err = decodeOptionalF64(r); err != nil {
			return err
		}
		if v.DefaultValue, err = decodeOptionalF64(r); err != nil {
			return err
		}
		s.Stepper = v
	case SettingTypeSegment:
		v := &SegmentSetting{}
		if v.Options, err = decodeStringSlice(r); err != nil {
			return err
		}
		if v.DefaultValue, err = decodeOptionalI64(r); err != nil {
			return err
		}
		s.Segment = v
	case SettingTypeText:
		v := &TextSetting{}
		if v.Placeholder, err = decodeOptionalString(r); err != nil {
			return err
		}
		if v.AutocapitalizationType, err = decodeOptionalI64(r); err != nil {
			return err
		}
		if v.KeyboardType, err = decodeOptionalI64(r); err != nil {
			return err
		}
		if v.ReturnKeyType, err = decodeOptionalI64(r); err != nil {
			return err
		}
		if v.AutocorrectionDisabled, err = decodeOptionalBool(r); err != nil {
			return err
		}
		if v.Secure, err = decodeOptionalBool(r); err != nil {
			return err
		}
		if v.DefaultValue, err = decodeOptionalString(r); err != nil {
			return err
		}
		s.Text = v
	case SettingTypeButton:
		v := &ButtonSetting{}
		if v.Destructive, err = decodeOptionalBool(r); err != nil {
			return err
		}
		if v.ConfirmTitle, err = decodeOptionalString(r); err != nil {
			return err
		}
		if v.ConfirmText, err = decodeOptionalString(r); err != nil {
			return err
		}
		s.Button = v
	case SettingTypeLink:
		v := &LinkSetting{}
		if v.URL, err = r.ReadString(); err != nil {
			return err
		}
		if v.External, err = decodeOptionalBool(r); err != nil {
			return err
		}
		s.Link = v
	case SettingTypeLogin:
		v := &LoginSetting{}
		method, err := r.ReadString()
		if err != nil {
			return err
		}
		v.Method = LoginMethod(method)
		if v.URL, err = decodeOptionalString(r); err != nil {
			return err
		}
		if v.URLKey, err = decodeOptionalString(r); err != nil {
			return err
		}
		if v.LogoutTitle, err = decodeOptionalString(r); err != nil {
			return err
		}
		if v.PKCE, err = decodeOptionalBool(r); err != nil {
			return err
		}
		if v.TokenURL, err = decodeOptionalString(r); err != nil {
			return err
		}
		if v.CallbackScheme, err = decodeOptionalString(r); err != nil {
			return err
		}
		if v.UseEmail, err = decodeOptionalBool(r); err != nil {
			return err
		}
		if v.LocalStorageKeys, err = decodeOptionalStringSlice(r); err != nil {
			return err
		}
		s.Login = v
	case SettingTypePage:
		v := &PageSetting{}
		n, err := r.ReadLen()
		if err != nil {
			return err
		}
		v.Items = make([]Setting, n)
		for i := 0; i < n; i++ {
			if err := v.Items[i].DecodePostcard(r); err != nil {
				return err
			}
		}
		if v.InlineTitle, err = decodeOptionalBool(r); err != nil {
			return err
		}
		if v.AuthToOpen, err = decodeOptionalBool(r); err != nil {
			return err
		}
		hasIcon, err := r.ReadOptionTag()
		if err != nil {
			return err
		}
		if hasIcon {
			icon := &PageSettingIcon{}
			iconType, err := r.ReadString()
			if err != nil {
				return err
			}
			switch iconType {
			case "system":
				icon.Kind = PageSettingIconKindSystem
				if icon.Name, err = r.ReadString(); err != nil {
					return err
				}
				if icon.Color, err = r.ReadString(); err != nil {
					return err
				}
				inset, err := decodeOptionalI64(r)
				if err != nil {
					return err
				}
				if inset != nil {
					icon.Inset = *inset
				} else {
					icon.Inset = 5
				}
			case "url":
				icon.Kind = PageSettingIconKindURL
				if icon.URL, err = r.ReadString(); err != nil {
					return err
				}
			default:
				return fmt.Errorf("postcard: unknown page setting icon type %q", iconType)
			}
			v.Icon = icon
		}
		if v.Info, err = decodeOptionalString(r); err != nil {
			return err
		}
		s.Page = v
	case SettingTypeEditableList:
		v := &EditableListSetting{}
		if v.LineLimit, err = decodeOptionalI64(r); err != nil {
			return err
		}
		if v.Inline, err = decodeOptionalBool(r); err != nil {
			return err
		}
		if v.Placeholder, err = decodeOptionalString(r); err != nil {
			return err
		}
		if v.DefaultValue, err = decodeOptionalStringSlice(r); err != nil {
			return err
		}
		s.EditableList = v
	case SettingTypePicker:
		v := &PickerSetting{}
		if v.Values, err = decodeStringSlice(r); err != nil {
			return err
		}
		if v.Titles, err = decodeOptionalStringSlice(r); err != nil {
			return err
		}
		if v.DefaultValue, err = decodeOptionalString(r); err != nil {
			return err
		}
		s.Picker = v
	case SettingTypeCustom:
		// no associated data
	default:
		return fmt.Errorf("postcard: unknown setting type %q", typeStr)
	}
	return nil
}

func DecodeSettingSlice(r *postcard.Reader) ([]Setting, error) {
	n, err := r.ReadLen()
	if err != nil {
		return nil, err
	}
	out := make([]Setting, n)
	for i := 0; i < n; i++ {
		if err := out[i].DecodePostcard(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// --- JSON (settings.json) ---

func (s *Setting) UnmarshalJSON(data []byte) error {
	var raw struct {
		Key           string      `json:"key"`
		Title         string      `json:"title"`
		Notification  *string     `json:"notification"`
		Requires      *string     `json:"requires"`
		RequiresFalse *string     `json:"requiresFalse"`
		Refreshes     []string    `json:"refreshes"`
		Type          SettingType `json:"type"`

		Footer *string   `json:"footer"`
		Items  []Setting `json:"items"`

		Values     []string `json:"values"`
		Titles     []string `json:"titles"`
		AuthToOpen *bool    `json:"authToOpen"`

		DefaultValue json.RawMessage `json:"default"`

		Subtitle      *string `json:"subtitle"`
		AuthToDisable *bool   `json:"authToDisable"`

		MinimumValue *float64 `json:"minimumValue"`
		MaximumValue *float64 `json:"maximumValue"`
		StepValue    *float64 `json:"stepValue"`

		Options *[]string `json:"options"`

		Placeholder            *string `json:"placeholder"`
		AutocapitalizationType *int64  `json:"autocapitalizationType"`
		KeyboardType           *int64  `json:"keyboardType"`
		ReturnKeyType          *int64  `json:"returnKeyType"`
		AutocorrectionDisabled *bool   `json:"autocorrectionDisabled"`
		Secure                 *bool   `json:"secure"`

		Destructive  *bool   `json:"destructive"`
		ConfirmTitle *string `json:"confirmTitle"`
		ConfirmText  *string `json:"confirmText"`

		URL      *string `json:"url"`
		External *bool   `json:"external"`

		Method           *LoginMethod `json:"method"`
		URLKey           *string      `json:"urlKey"`
		LogoutTitle      *string      `json:"logoutTitle"`
		PKCE             *bool        `json:"pkce"`
		TokenURL         *string      `json:"tokenUrl"`
		CallbackScheme   *string      `json:"callbackScheme"`
		UseEmail         *bool        `json:"useEmail"`
		LocalStorageKeys []string     `json:"localStorageKeys"`

		InlineTitle *bool                `json:"inlineTitle"`
		Icon        *pageSettingIconJSON `json:"icon"`
		Info        *string              `json:"info"`

		LineLimit *int64 `json:"lineLimit"`
		Inline    *bool  `json:"inline"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	s.Key = raw.Key
	s.Title = raw.Title
	s.Notification = raw.Notification
	s.Requires = raw.Requires
	s.RequiresFalse = raw.RequiresFalse
	if raw.Refreshes != nil {
		s.Refreshes = raw.Refreshes
	} else {
		s.Refreshes = []string{}
	}
	if raw.Type == "" {
		return fmt.Errorf("models: setting missing type")
	}
	s.Type = raw.Type

	hasDefault := len(raw.DefaultValue) > 0 && string(raw.DefaultValue) != "null"

	switch s.Type {
	case SettingTypeGroup:
		s.Group = &GroupSetting{Footer: raw.Footer, Items: raw.Items}
	case SettingTypeSelect:
		v := &SelectSetting{Values: raw.Values, Titles: raw.Titles, AuthToOpen: raw.AuthToOpen}
		if hasDefault {
			var s string
			if err := json.Unmarshal(raw.DefaultValue, &s); err != nil {
				return err
			}
			v.DefaultValue = &s
		}
		s.Select = v
	case SettingTypeMultiselect:
		v := &MultiSelectSetting{Values: raw.Values, Titles: raw.Titles, AuthToOpen: raw.AuthToOpen}
		if hasDefault {
			if err := json.Unmarshal(raw.DefaultValue, &v.DefaultValue); err != nil {
				return err
			}
		}
		s.Multiselect = v
	case SettingTypeToggle:
		v := &ToggleSetting{Subtitle: raw.Subtitle, AuthToDisable: raw.AuthToDisable}
		if hasDefault {
			if err := json.Unmarshal(raw.DefaultValue, &v.DefaultValue); err != nil {
				return err
			}
		}
		s.Toggle = v
	case SettingTypeStepper:
		v := &StepperSetting{StepValue: raw.StepValue}
		if raw.MinimumValue != nil {
			v.MinimumValue = *raw.MinimumValue
		}
		if raw.MaximumValue != nil {
			v.MaximumValue = *raw.MaximumValue
		}
		if hasDefault {
			if err := json.Unmarshal(raw.DefaultValue, &v.DefaultValue); err != nil {
				return err
			}
		}
		s.Stepper = v
	case SettingTypeSegment:
		v := &SegmentSetting{}
		if raw.Options != nil {
			v.Options = *raw.Options
		}
		if hasDefault {
			if err := json.Unmarshal(raw.DefaultValue, &v.DefaultValue); err != nil {
				return err
			}
		}
		s.Segment = v
	case SettingTypeText:
		v := &TextSetting{
			Placeholder:            raw.Placeholder,
			AutocapitalizationType: raw.AutocapitalizationType,
			KeyboardType:           raw.KeyboardType,
			ReturnKeyType:          raw.ReturnKeyType,
			AutocorrectionDisabled: raw.AutocorrectionDisabled,
			Secure:                 raw.Secure,
		}
		if hasDefault {
			if err := json.Unmarshal(raw.DefaultValue, &v.DefaultValue); err != nil {
				return err
			}
		}
		s.Text = v
	case SettingTypeButton:
		s.Button = &ButtonSetting{Destructive: raw.Destructive, ConfirmTitle: raw.ConfirmTitle, ConfirmText: raw.ConfirmText}
	case SettingTypeLink:
		v := &LinkSetting{External: raw.External}
		if raw.URL != nil {
			v.URL = *raw.URL
		}
		s.Link = v
	case SettingTypeLogin:
		v := &LoginSetting{
			URL: raw.URL, URLKey: raw.URLKey, LogoutTitle: raw.LogoutTitle, PKCE: raw.PKCE,
			TokenURL: raw.TokenURL, CallbackScheme: raw.CallbackScheme, UseEmail: raw.UseEmail,
			LocalStorageKeys: raw.LocalStorageKeys,
		}
		if raw.Method != nil {
			v.Method = *raw.Method
		}
		s.Login = v
	case SettingTypePage:
		v := &PageSetting{Items: raw.Items, InlineTitle: raw.InlineTitle, AuthToOpen: raw.AuthToOpen, Info: raw.Info}
		if raw.Icon != nil {
			icon := &PageSettingIcon{}
			switch raw.Icon.Type {
			case "system":
				icon.Kind = PageSettingIconKindSystem
				icon.Name = raw.Icon.Name
				icon.Color = raw.Icon.Color
				if raw.Icon.Inset != nil {
					icon.Inset = *raw.Icon.Inset
				} else {
					icon.Inset = 5
				}
			case "url":
				icon.Kind = PageSettingIconKindURL
				icon.URL = raw.Icon.URL
			default:
				return fmt.Errorf("models: unknown page setting icon type %q", raw.Icon.Type)
			}
			v.Icon = icon
		}
		s.Page = v
	case SettingTypeEditableList:
		v := &EditableListSetting{LineLimit: raw.LineLimit, Inline: raw.Inline, Placeholder: raw.Placeholder}
		if hasDefault {
			if err := json.Unmarshal(raw.DefaultValue, &v.DefaultValue); err != nil {
				return err
			}
		}
		s.EditableList = v
	case SettingTypePicker:
		v := &PickerSetting{Values: raw.Values, Titles: raw.Titles}
		if hasDefault {
			var s string
			if err := json.Unmarshal(raw.DefaultValue, &s); err != nil {
				return err
			}
			v.DefaultValue = &s
		}
		s.Picker = v
	case SettingTypeCustom:
		// no associated data
	default:
		return fmt.Errorf("models: unknown setting type %q", raw.Type)
	}
	return nil
}

type pageSettingIconJSON struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Inset *int64 `json:"inset"`
	URL   string `json:"url"`
}

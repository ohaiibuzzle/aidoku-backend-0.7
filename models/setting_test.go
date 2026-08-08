package models

import (
	"encoding/json"
	"testing"

	"github.com/ohaiibuzzle/aidokurunner-go/postcard"
)

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }

func TestSettingJSONToggle(t *testing.T) {
	data := []byte(`{
		"key": "switch1",
		"title": "Switch 1",
		"type": "switch",
		"default": true
	}`)
	var s Setting
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Key != "switch1" || s.Title != "Switch 1" || s.Type != SettingTypeToggle {
		t.Fatalf("unexpected setting: %+v", s)
	}
	if s.Toggle == nil || !s.Toggle.DefaultValue {
		t.Fatalf("expected toggle default true, got %+v", s.Toggle)
	}
}

func TestSettingJSONGroupNested(t *testing.T) {
	data := []byte(`{
		"title": "Settings",
		"type": "group",
		"items": [
			{"key": "switch1", "title": "Switch 1", "type": "switch"},
			{"key": "stepper", "title": "Stepper", "type": "stepper", "minimumValue": 0, "maximumValue": 100, "stepValue": 5, "default": 50}
		]
	}`)
	var s Setting
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Group == nil || len(s.Group.Items) != 2 {
		t.Fatalf("expected 2 nested items, got %+v", s.Group)
	}
	if s.Group.Items[1].Stepper == nil || *s.Group.Items[1].Stepper.DefaultValue != 50 {
		t.Fatalf("unexpected stepper: %+v", s.Group.Items[1].Stepper)
	}
}

func TestSettingPostcardRoundTrip(t *testing.T) {
	original := Setting{
		Key:          "mySelect",
		Title:        "My Select",
		Notification: strp("note"),
		Refreshes:    []string{"content"},
		Type:         SettingTypeSelect,
		Select: &SelectSetting{
			Values:       []string{"a", "b", "c"},
			Titles:       []string{"A", "B", "C"},
			DefaultValue: strp("b"),
		},
	}

	w := postcard.NewWriter()
	encodeSettingForTest(w, &original)

	r := postcard.NewReader(w.Bytes())
	var decoded Setting
	if err := decoded.DecodePostcard(r); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("%d bytes left over after decode", r.Remaining())
	}
	if decoded.Key != original.Key || decoded.Title != original.Title {
		t.Fatalf("key/title mismatch: %+v", decoded)
	}
	if decoded.Notification == nil || *decoded.Notification != "note" {
		t.Fatalf("notification mismatch: %+v", decoded.Notification)
	}
	if len(decoded.Refreshes) != 1 || decoded.Refreshes[0] != "content" {
		t.Fatalf("refreshes mismatch: %+v", decoded.Refreshes)
	}
	if decoded.Select == nil || len(decoded.Select.Values) != 3 || *decoded.Select.DefaultValue != "b" {
		t.Fatalf("select mismatch: %+v", decoded.Select)
	}
}

// encodeSettingForTest hand-encodes a Setting on the wire using the same
// field order DecodePostcard expects, standing in for a not-yet-written
// EncodePostcard (Setting is guest->host only in practice, so production
// code never needs to encode it — this exists purely to exercise the
// decoder in tests).
func encodeSettingForTest(w *postcard.Writer, s *Setting) {
	w.WriteString(string(s.Type))
	w.WriteString(s.Key)
	w.WriteString(s.Title)
	encodeOptionalString(w, s.Notification)
	encodeOptionalString(w, s.Requires)
	encodeOptionalString(w, s.RequiresFalse)
	w.WriteSome()
	encodeStringSlice(w, s.Refreshes)
	w.WriteU8(s.Type.byteValue())

	switch s.Type {
	case SettingTypeSelect:
		encodeStringSlice(w, s.Select.Values)
		encodeOptionalStringSlice(w, s.Select.Titles)
		encodeOptionalBool(w, s.Select.AuthToOpen)
		encodeOptionalString(w, s.Select.DefaultValue)
	case SettingTypeToggle:
		encodeOptionalString(w, s.Toggle.Subtitle)
		encodeOptionalBool(w, s.Toggle.AuthToDisable)
		w.WriteBool(s.Toggle.DefaultValue)
	}
}

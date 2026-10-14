package models

import (
	"encoding/json"
	"testing"

	"github.com/ohaiibuzzle/aidokurunner-go/postcard"
)

// These tests hand-encode the *guest's* exact postcard bytes, i.e. the field
// order of Aidoku/aidoku-rs crates/lib/src/structs/setting.rs
// (postcard::to_allocvec over the Rust structs, declaration order), rather
// than the order of AidokuRunner's Swift structs.
//
// Setting { key, title, notification, requires, requires_false, refreshes,
//           value: SettingValue (varint enum discriminant + payload) }
//
// Text        { placeholder, autocapitalization_type, autocorrection_disabled,
//               keyboard_type, return_key_type, secure, default }
// Button      { } (unit variant, no payload)
// Login       { method, url, url_key, logout_title, pkce(bool), token_url,
//               callback_scheme, use_email(bool), local_storage_keys,
//               clear_cookies_on_log_out(bool) }
// EditableList{ line_limit, inline(bool), placeholder, default }

// writeGuestHeader writes the shared Setting fields followed by the
// SettingValue enum discriminant, all of them absent except the required
// strings.
func writeGuestHeader(w *postcard.Writer, typ SettingType, key, title string) {
	w.WriteString(string(typ))
	w.WriteString(key)
	w.WriteString(title)
	w.WriteNone() // notification
	w.WriteNone() // requires
	w.WriteNone() // requires_false
	w.WriteNone() // refreshes
	w.WriteU8(typ.byteValue())
}

// checkWireBool asserts a decoded boolean field equals want. It accepts both
// a plain bool and an *bool so the same assertion source is meaningful
// whether the model type represents the wire value as a non-optional bool
// (what the guest actually sends) or as an Option<bool> sentinel.
func checkWireBool(t *testing.T, name string, got any, want bool) {
	t.Helper()
	var actual bool
	ok := false
	switch v := got.(type) {
	case bool:
		actual, ok = v, true
	case *bool:
		if v != nil {
			actual, ok = *v, true
		}
	}
	if !ok {
		t.Fatalf("%s: got %#v, want plain bool %v", name, got, want)
	}
	if actual != want {
		t.Fatalf("%s = %v, want %v", name, actual, want)
	}
}

func decodeGuestSetting(t *testing.T, w *postcard.Writer) Setting {
	t.Helper()
	r := postcard.NewReader(w.Bytes())
	var s Setting
	if err := s.DecodePostcard(r); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("%d bytes left over after decode", r.Remaining())
	}
	return s
}

// Text { placeholder: Some("ph"), autocapitalization_type: Some(2),
//
//	autocorrection_disabled: Some(true), keyboard_type: None,
//	return_key_type: None, secure: None, default: None }
func TestSettingGuestOrderTextPartial(t *testing.T) {
	w := postcard.NewWriter()
	writeGuestHeader(w, SettingTypeText, "t", "T")
	w.WriteSome()
	w.WriteString("ph") // placeholder
	w.WriteSome()
	w.WriteI32(2) // autocapitalization_type
	w.WriteSome()
	w.WriteBool(true) // autocorrection_disabled
	w.WriteNone()     // keyboard_type
	w.WriteNone()     // return_key_type
	w.WriteNone()     // secure
	w.WriteNone()     // default

	s := decodeGuestSetting(t, w)
	if s.Text == nil {
		t.Fatal("expected Text setting")
	}
	if s.Text.Placeholder == nil || *s.Text.Placeholder != "ph" {
		t.Fatalf("placeholder = %v, want \"ph\"", s.Text.Placeholder)
	}
	if s.Text.AutocapitalizationType == nil || *s.Text.AutocapitalizationType != 2 {
		t.Fatalf("autocapitalization_type = %v, want 2", s.Text.AutocapitalizationType)
	}
	if s.Text.AutocorrectionDisabled == nil || !*s.Text.AutocorrectionDisabled {
		t.Fatalf("autocorrection_disabled = %v, want true", s.Text.AutocorrectionDisabled)
	}
	if s.Text.KeyboardType != nil {
		t.Fatalf("keyboard_type = %v, want nil", *s.Text.KeyboardType)
	}
	if s.Text.ReturnKeyType != nil {
		t.Fatalf("return_key_type = %v, want nil", *s.Text.ReturnKeyType)
	}
	if s.Text.Secure != nil {
		t.Fatalf("secure = %v, want nil", *s.Text.Secure)
	}
	if s.Text.DefaultValue != nil {
		t.Fatalf("default = %v, want nil", *s.Text.DefaultValue)
	}
}

// Text with every optional present; the widest desync case.
func TestSettingGuestOrderTextAllFields(t *testing.T) {
	w := postcard.NewWriter()
	writeGuestHeader(w, SettingTypeText, "t", "T")
	w.WriteSome()
	w.WriteString("ph") // placeholder
	w.WriteSome()
	w.WriteI32(2) // autocapitalization_type
	w.WriteSome()
	w.WriteBool(true) // autocorrection_disabled
	w.WriteSome()
	w.WriteI32(3) // keyboard_type
	w.WriteSome()
	w.WriteI32(4) // return_key_type
	w.WriteSome()
	w.WriteBool(true) // secure
	w.WriteSome()
	w.WriteString("d") // default

	s := decodeGuestSetting(t, w)
	if s.Text == nil {
		t.Fatal("expected Text setting")
	}
	if s.Text.Placeholder == nil || *s.Text.Placeholder != "ph" {
		t.Fatalf("placeholder = %v, want \"ph\"", s.Text.Placeholder)
	}
	if s.Text.AutocapitalizationType == nil || *s.Text.AutocapitalizationType != 2 {
		t.Fatalf("autocapitalization_type = %v, want 2", s.Text.AutocapitalizationType)
	}
	if s.Text.AutocorrectionDisabled == nil || !*s.Text.AutocorrectionDisabled {
		t.Fatalf("autocorrection_disabled = %v, want true", s.Text.AutocorrectionDisabled)
	}
	if s.Text.KeyboardType == nil || *s.Text.KeyboardType != 3 {
		t.Fatalf("keyboard_type = %v, want 3", s.Text.KeyboardType)
	}
	if s.Text.ReturnKeyType == nil || *s.Text.ReturnKeyType != 4 {
		t.Fatalf("return_key_type = %v, want 4", s.Text.ReturnKeyType)
	}
	if s.Text.Secure == nil || !*s.Text.Secure {
		t.Fatalf("secure = %v, want true", s.Text.Secure)
	}
	if s.Text.DefaultValue == nil || *s.Text.DefaultValue != "d" {
		t.Fatalf("default = %v, want \"d\"", s.Text.DefaultValue)
	}
}

// A unit-variant Button must consume no payload and must not eat the bytes
// of the setting that follows it.
func TestSettingGuestOrderButtonInList(t *testing.T) {
	w := postcard.NewWriter()
	w.WriteLen(2)
	writeGuestHeader(w, SettingTypeButton, "b", "B")
	writeGuestHeader(w, SettingTypeToggle, "sw", "SW")
	w.WriteNone()     // subtitle
	w.WriteNone()     // auth_to_disable
	w.WriteBool(true) // default_value

	r := postcard.NewReader(w.Bytes())
	settings, err := DecodeSettingSlice(r)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("%d bytes left over after decode", r.Remaining())
	}
	if len(settings) != 2 {
		t.Fatalf("got %d settings, want 2", len(settings))
	}
	if settings[0].Type != SettingTypeButton || settings[0].Button == nil {
		t.Fatalf("first setting = %+v, want a button", settings[0])
	}
	if settings[1].Type != SettingTypeToggle || settings[1].Toggle == nil {
		t.Fatalf("second setting = %+v, want a toggle", settings[1])
	}
	if !settings[1].Toggle.DefaultValue {
		t.Fatalf("toggle default_value = false, want true")
	}
}

func writeGuestLogin(w *postcard.Writer, pkce, useEmail, clearCookies bool) {
	writeGuestHeader(w, SettingTypeLogin, "l", "L")
	w.WriteString("oauth") // method
	w.WriteNone()          // url
	w.WriteNone()          // url_key
	w.WriteNone()          // logout_title
	w.WriteBool(pkce)      // pkce
	w.WriteNone()          // token_url
	w.WriteNone()          // callback_scheme
	w.WriteBool(useEmail)  // use_email
	w.WriteNone()          // local_storage_keys
	w.WriteBool(clearCookies)
}

// The three Login bools are plain bytes on the wire, not tag+value Options.
func TestSettingGuestOrderLoginFlagsFalse(t *testing.T) {
	w := postcard.NewWriter()
	writeGuestLogin(w, false, false, false)

	s := decodeGuestSetting(t, w)
	if s.Login == nil {
		t.Fatal("expected Login setting")
	}
	if s.Login.Method != LoginMethodOAuth {
		t.Fatalf("method = %q, want %q", s.Login.Method, LoginMethodOAuth)
	}
	if s.Login.URL != nil || s.Login.URLKey != nil || s.Login.LogoutTitle != nil {
		t.Fatalf("optional strings decoded as present: %+v", s.Login)
	}
	if s.Login.TokenURL != nil || s.Login.CallbackScheme != nil || s.Login.LocalStorageKeys != nil {
		t.Fatalf("optional trailing fields decoded as present: %+v", s.Login)
	}
	checkWireBool(t, "pkce", s.Login.PKCE, false)
	checkWireBool(t, "use_email", s.Login.UseEmail, false)
	checkWireBool(t, "clear_cookies_on_log_out", s.Login.ClearCookiesOnLogout, false)
}

func TestSettingGuestOrderLoginPKCETrue(t *testing.T) {
	w := postcard.NewWriter()
	writeGuestLogin(w, true, false, false)

	s := decodeGuestSetting(t, w)
	if s.Login == nil {
		t.Fatal("expected Login setting")
	}
	checkWireBool(t, "pkce", s.Login.PKCE, true)
	checkWireBool(t, "use_email", s.Login.UseEmail, false)
	checkWireBool(t, "clear_cookies_on_log_out", s.Login.ClearCookiesOnLogout, false)
}

func writeGuestEditableList(w *postcard.Writer, inline bool) {
	writeGuestHeader(w, SettingTypeEditableList, "el", "EL")
	w.WriteNone()       // line_limit
	w.WriteBool(inline) // inline
	w.WriteNone()       // placeholder
	w.WriteNone()       // default
}

func TestSettingGuestOrderEditableListInlineTrue(t *testing.T) {
	w := postcard.NewWriter()
	writeGuestEditableList(w, true)

	s := decodeGuestSetting(t, w)
	if s.EditableList == nil {
		t.Fatal("expected EditableList setting")
	}
	if s.EditableList.LineLimit != nil {
		t.Fatalf("line_limit = %v, want nil", *s.EditableList.LineLimit)
	}
	checkWireBool(t, "inline", s.EditableList.Inline, true)
	if s.EditableList.Placeholder != nil {
		t.Fatalf("placeholder = %v, want nil", *s.EditableList.Placeholder)
	}
	if s.EditableList.DefaultValue != nil {
		t.Fatalf("default = %v, want nil", s.EditableList.DefaultValue)
	}
}

func TestSettingGuestOrderEditableListInlineFalse(t *testing.T) {
	w := postcard.NewWriter()
	writeGuestEditableList(w, false)

	s := decodeGuestSetting(t, w)
	if s.EditableList == nil {
		t.Fatal("expected EditableList setting")
	}
	checkWireBool(t, "inline", s.EditableList.Inline, false)
}

// The JSON manifest path must agree with the postcard path about which
// fields exist and that the login flags are plain booleans.
func TestSettingGuestOrderLoginJSONBools(t *testing.T) {
	data := []byte(`{
		"key": "l",
		"title": "Login",
		"type": "login",
		"method": "oauth",
		"pkce": true,
		"useEmail": false,
		"clearCookiesOnLogOut": false
	}`)
	var s Setting
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Login == nil {
		t.Fatal("expected Login setting")
	}
	if s.Login.Method != LoginMethodOAuth {
		t.Fatalf("method = %q, want %q", s.Login.Method, LoginMethodOAuth)
	}
	checkWireBool(t, "pkce", s.Login.PKCE, true)
	checkWireBool(t, "use_email", s.Login.UseEmail, false)
	checkWireBool(t, "clear_cookies_on_log_out", s.Login.ClearCookiesOnLogout, false)
}

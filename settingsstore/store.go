// Package settingsstore is a small JSON-file-backed key/value store used
// as this project's UserDefaults equivalent (AidokuRunner's
// SettingsStore.swift wraps UserDefaults, which has no direct Go/Kindle
// analogue). Keys are namespaced by callers as "sourceKey.settingKey".
package settingsstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	mu       sync.Mutex
	path     string
	values   map[string]any
	defaults map[string]any
}

// Open loads an existing store from path, or starts empty if the file
// doesn't exist yet. Pass "" for an in-memory-only store (mainly useful in
// tests).
func Open(path string) (*Store, error) {
	s := &Store{
		path:     path,
		values:   make(map[string]any),
		defaults: make(map[string]any),
	}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return s, nil
	}
	var raw map[string]taggedValue
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	for k, tv := range raw {
		s.values[k] = tv.untag()
	}
	return s, nil
}

func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	tagged := make(map[string]taggedValue, len(s.values))
	for k, v := range s.values {
		tv, ok := tag(v)
		if !ok {
			continue // unsupported type; silently skip persistence
		}
		tagged[k] = tv
	}
	data, err := json.MarshalIndent(tagged, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

// taggedValue preserves the exact Go type of a stored value across a JSON
// round trip. Plain `map[string]any` JSON unmarshaling collapses every
// number to float64, which would corrupt defaults.get's type switch
// (bool/int32/float32/string/[]string/[]byte) after a process restart.
type taggedValue struct {
	Kind     string   `json:"kind"`
	Bool     bool     `json:"bool,omitempty"`
	Int32    int32    `json:"int32,omitempty"`
	Float32  float32  `json:"float32,omitempty"`
	Str      string   `json:"str,omitempty"`
	StrArray []string `json:"strArray,omitempty"`
	Bytes    []byte   `json:"bytes,omitempty"`
}

func tag(v any) (taggedValue, bool) {
	switch x := v.(type) {
	case bool:
		return taggedValue{Kind: "bool", Bool: x}, true
	case int32:
		return taggedValue{Kind: "int32", Int32: x}, true
	case float32:
		return taggedValue{Kind: "float32", Float32: x}, true
	case string:
		return taggedValue{Kind: "string", Str: x}, true
	case []string:
		return taggedValue{Kind: "strArray", StrArray: x}, true
	case []byte:
		return taggedValue{Kind: "bytes", Bytes: x}, true
	default:
		return taggedValue{}, false
	}
}

func (tv taggedValue) untag() any {
	switch tv.Kind {
	case "bool":
		return tv.Bool
	case "int32":
		return tv.Int32
	case "float32":
		return tv.Float32
	case "string":
		return tv.Str
	case "strArray":
		return tv.StrArray
	case "bytes":
		return tv.Bytes
	default:
		return nil
	}
}

// RegisterDefault sets the fallback value Get returns when key hasn't been
// explicitly set, matching UserDefaults.register(defaults:).
func (s *Store) RegisterDefault(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaults[key] = value
}

// Object returns the stored value for key, or its registered default, or
// nil if neither is set.
func (s *Store) Object(key string) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.values[key]; ok {
		return v
	}
	return s.defaults[key]
}

// SetValue stores value for key (nil removes it), matching
// UserDefaults.setValue(_:forKey:).
func (s *Store) SetValue(key string, value any) error {
	s.mu.Lock()
	if value == nil {
		delete(s.values, key)
	} else {
		s.values[key] = value
	}
	s.mu.Unlock()
	return s.save()
}

func (s *Store) Remove(key string) error {
	return s.SetValue(key, nil)
}

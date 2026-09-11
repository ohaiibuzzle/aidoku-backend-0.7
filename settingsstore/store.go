// Package settingsstore is a small JSON-file-backed key/value store used
// as this project's UserDefaults equivalent (AidokuRunner's
// SettingsStore.swift wraps UserDefaults, which has no direct Go/Kindle
// analogue). Keys are namespaced by callers as "sourceKey.settingKey".
package settingsstore

import (
	"encoding/json"
	"fmt"
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
	// Snapshot values under the lock, then do JSON marshal + disk I/O
	// unlocked -- SetValue releases s.mu before calling save(), so two
	// concurrent SetValues (e.g. net.send_all's per-descriptor goroutines
	// each hitting defaults.set) would otherwise range s.values here while
	// another goroutine mutates it, which is a fatal, unrecoverable
	// concurrent map read/write in Go.
	s.mu.Lock()
	tagged := make(map[string]taggedValue, len(s.values))
	for k, v := range s.values {
		tv, ok := tag(v)
		if !ok {
			// A caller passed a type SetValue's contract doesn't support
			// (bool/int32/float32/string/[]string/[]byte, see tag()) --
			// surface it rather than just dropping the value on the floor,
			// so this doesn't look like a successful SetValue that quietly
			// never persists.
			fmt.Fprintf(os.Stderr, "settingsstore: %q has unsupported value type %T, not persisted\n", k, v)
			continue
		}
		tagged[k] = tv
	}
	s.mu.Unlock()
	data, err := json.Marshal(tagged)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// Write to a temp file in the same directory and rename into place
	// (same pattern as cbz.Writer.Close), so a crash or power loss
	// partway through a write never leaves settings.json truncated or
	// corrupt -- Open would otherwise fail to unmarshal it and abort
	// every subsequent command until someone manually deletes the file.
	tmp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	// os.CreateTemp always creates with 0600, not the 0644 os.WriteFile
	// used previously; chmod explicitly so the rename doesn't silently
	// tighten settings.json's permissions as a side effect of this change.
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
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

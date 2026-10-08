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
	"syscall"
)

type Store struct {
	mu       sync.Mutex
	path     string
	values   map[string]any
	defaults map[string]any
	// dirty is every key this process has set/removed since its last
	// successful save -- the only keys save() writes over what's on disk.
	dirty map[string]bool
}

// Open loads an existing store from path, or starts empty if the file
// doesn't exist yet. Pass "" for an in-memory-only store (mainly useful in
// tests).
func Open(path string) (*Store, error) {
	s := &Store{
		path:     path,
		values:   make(map[string]any),
		defaults: make(map[string]any),
		dirty:    make(map[string]bool),
	}
	if path == "" {
		return s, nil
	}
	raw, err := readTagged(path)
	if err != nil {
		return nil, err
	}
	for k, tv := range raw {
		s.values[k] = tv.untag()
	}
	return s, nil
}

// readTagged reads the on-disk store, treating a missing or empty file as
// empty.
func readTagged(path string) (map[string]taggedValue, error) {
	raw := map[string]taggedValue{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return raw, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return raw, nil
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// Locked runs fn while holding an exclusive flock(2) on path+".lock", so
// separate aidoku-run processes doing read-modify-write on the same file
// (settings.json here, cookies.json in cmd/aidoku-run) can't interleave and
// drop each other's changes. The lock file is never deleted, for the same
// reason as downloads.AcquireLock's marker files.
func Locked(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	return Locked(s.path, s.saveLocked)
}

// saveLocked merges this process's dirty keys into whatever is on disk now
// (not the snapshot Open read): another aidoku-run process -- e.g. a
// background prefetch download -- may have saved since, and writing the whole
// in-memory map back would silently revert its changes.
func (s *Store) saveLocked() error {
	onDisk, err := readTagged(s.path)
	if err != nil {
		// Corrupt file: nothing to merge with, so fall back to rewriting it
		// from memory rather than failing every save forever.
		onDisk = map[string]taggedValue{}
		s.mu.Lock()
		for k := range s.values {
			s.dirty[k] = true
		}
		s.mu.Unlock()
	}

	// The map is read and the dirty set cleared under s.mu; marshal + disk
	// I/O happen unlocked. SetValue releases s.mu before calling save(), so
	// ranging s.values without it is a fatal concurrent map read/write.
	s.mu.Lock()
	written := s.dirty
	s.dirty = make(map[string]bool)
	for k := range written {
		v, ok := s.values[k]
		if !ok {
			delete(onDisk, k)
			continue
		}
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
		onDisk[k] = tv
	}
	s.mu.Unlock()

	err = s.write(onDisk)
	if err != nil {
		s.mu.Lock()
		for k := range written {
			s.dirty[k] = true
		}
		s.mu.Unlock()
	}
	return err
}

func (s *Store) write(tagged map[string]taggedValue) error {
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
	s.dirty[key] = true
	s.mu.Unlock()
	return s.save()
}

func (s *Store) Remove(key string) error {
	return s.SetValue(key, nil)
}

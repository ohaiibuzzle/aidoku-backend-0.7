package host

import (
	"context"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	"github.com/ohaiibuzzle/aidokurunner-go/postcard"
)

type defaultsResult int32

const (
	defaultsSuccess        defaultsResult = 0
	defaultsInvalidKey     defaultsResult = -1
	defaultsInvalidValue   defaultsResult = -2
	defaultsFailedEncoding defaultsResult = -3
	defaultsFailedDecoding defaultsResult = -4
)

type defaultKind uint8

const (
	defaultKindData        defaultKind = 0
	defaultKindBool        defaultKind = 1
	defaultKindInt         defaultKind = 2
	defaultKindFloat       defaultKind = 3
	defaultKindString      defaultKind = 4
	defaultKindStringArray defaultKind = 5
	defaultKindNull        defaultKind = 6
)

// SettingsStore is the subset of settingsstore.Store that the `defaults`
// namespace needs, kept as an interface so this package doesn't depend on
// settingsstore directly.
type SettingsStore interface {
	Object(key string) any
	SetValue(key string, value any) error
}

// Defaults implements the `defaults` namespace (Imports/Defaults.swift):
// get, set. Keys are namespaced as "<DefaultNamespace>.<key>", where
// DefaultNamespace is the source's key (matches Defaults.swift's
// `defaultNamespace: String`, set to the source key in Interpreter.swift).
type Defaults struct {
	Store            *Store
	Settings         SettingsStore
	DefaultNamespace string
}

// LinkDefaults registers the `defaults` namespace onto the given host
// module builder.
func LinkDefaults(builder wazero.HostModuleBuilder, d *Defaults) wazero.HostModuleBuilder {
	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, keyPointer, length int32) int32 {
			return int32(d.get(m, keyPointer, length))
		}).
		Export("get")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, keyPointer, length, valueKind, valuePointer int32) int32 {
			return int32(d.set(m, keyPointer, length, valueKind, valuePointer))
		}).
		Export("set")

	return builder
}

func (d *Defaults) get(m api.Module, keyPointer, length int32) int32 {
	if keyPointer < 0 || length < 0 {
		return int32(defaultsInvalidKey)
	}
	keyBytes, ok := m.Memory().Read(uint32(keyPointer), uint32(length))
	if !ok {
		return int32(defaultsInvalidKey)
	}
	object := d.Settings.Object(d.DefaultNamespace + "." + string(keyBytes))

	switch v := object.(type) {
	case bool:
		w := postcard.NewWriter()
		w.WriteBool(v)
		return d.Store.Store(w.Bytes())
	case int32:
		w := postcard.NewWriter()
		w.WriteI32(v)
		return d.Store.Store(w.Bytes())
	case float32:
		w := postcard.NewWriter()
		w.WriteF32(v)
		return d.Store.Store(w.Bytes())
	case string:
		w := postcard.NewWriter()
		w.WriteString(v)
		return d.Store.Store(w.Bytes())
	case []string:
		w := postcard.NewWriter()
		w.WriteLen(len(v))
		for _, s := range v {
			w.WriteString(s)
		}
		return d.Store.Store(w.Bytes())
	case []byte:
		return d.Store.Store(v)
	default:
		return int32(defaultsInvalidValue)
	}
}

func (d *Defaults) set(m api.Module, keyPointer, length, valueKindRaw, valuePointer int32) int32 {
	if keyPointer < 0 || length < 0 {
		return int32(defaultsInvalidKey)
	}
	keyBytes, ok := m.Memory().Read(uint32(keyPointer), uint32(length))
	if !ok {
		return int32(defaultsInvalidKey)
	}
	key := d.DefaultNamespace + "." + string(keyBytes)

	kind := defaultKind(uint8(valueKindRaw))

	if kind == defaultKindNull {
		if err := d.Settings.SetValue(key, nil); err != nil {
			return int32(defaultsFailedDecoding)
		}
		return int32(defaultsSuccess)
	}

	// The guest passes its own memory pointer here (not a Store
	// descriptor): a [length:u32][pad:u32][data] buffer it allocated
	// itself, same convention as guest export return values.
	data, ok := readGuestBuffer(m, valuePointer)
	if !ok {
		return int32(defaultsFailedDecoding)
	}
	r := postcard.NewReader(data)

	var value any
	switch kind {
	case defaultKindData:
		value = data
	case defaultKindBool:
		v, err := r.ReadBool()
		if err != nil {
			return int32(defaultsFailedDecoding)
		}
		value = v
	case defaultKindInt:
		v, err := r.ReadI32()
		if err != nil {
			return int32(defaultsFailedDecoding)
		}
		value = v
	case defaultKindFloat:
		v, err := r.ReadF32()
		if err != nil {
			return int32(defaultsFailedDecoding)
		}
		value = v
	case defaultKindString:
		v, err := r.ReadString()
		if err != nil {
			return int32(defaultsFailedDecoding)
		}
		value = v
	case defaultKindStringArray:
		n, err := r.ReadLen()
		if err != nil {
			return int32(defaultsFailedDecoding)
		}
		arr := make([]string, n)
		for i := 0; i < n; i++ {
			if arr[i], err = r.ReadString(); err != nil {
				return int32(defaultsFailedDecoding)
			}
		}
		value = arr
	default:
		return int32(defaultsInvalidValue)
	}

	if err := d.Settings.SetValue(key, value); err != nil {
		return int32(defaultsFailedDecoding)
	}
	return int32(defaultsSuccess)
}

// readGuestBuffer reads a [length:u32][pad:u32][data] buffer directly from
// guest linear memory at pointer, matching the same convention used for
// guest export return values (Interpreter.swift's handleResult).
func readGuestBuffer(m api.Module, pointer int32) ([]byte, bool) {
	if pointer < 0 {
		return nil, false
	}
	p := uint32(pointer)
	length, ok := m.Memory().ReadUint32Le(p)
	if !ok || length < 8 {
		return nil, false
	}
	data, ok := m.Memory().Read(p+8, length-8)
	if !ok {
		return nil, false
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	return cp, true
}

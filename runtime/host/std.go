package host

import (
	"context"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

type stdResult int32

const (
	stdSuccess           stdResult = 0
	stdInvalidDescriptor stdResult = -1
	stdInvalidBufferSize stdResult = -2
	stdFailedMemoryWrite stdResult = -3
	stdInvalidString     stdResult = -4
	stdInvalidDateString stdResult = -5
)

// Std implements the `std` namespace (Imports/Std.swift): destroy,
// buffer_len, read_buffer, current_date, utc_offset, parse_date.
type Std struct {
	Store *Store

	// PrintHandler, if set, additionally registers `std.print`/`std.abort`
	// aliases of the `env` namespace's functions. Current Aidoku sources
	// import print/abort from `env` only (see Env.swift); some older
	// compiled sources — including AidokuRunner's own bundled test fixture
	// — still import them from `std`. Wasm3 resolves imports lazily and
	// never notices an unused stale import; wazero requires every import
	// to resolve at instantiation time, so we fill both in when needed.
	PrintHandler func(string)
}

func (s *Std) bytesFor(descriptor int32) ([]byte, bool) {
	item := s.Store.Fetch(descriptor)
	switch v := item.(type) {
	case []byte:
		return v, true
	case string:
		return []byte(v), true
	default:
		return nil, false
	}
}

// LinkStd registers the `std` namespace onto the given host module builder.
func LinkStd(builder wazero.HostModuleBuilder, s *Std) wazero.HostModuleBuilder {
	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) {
			s.Store.Remove(descriptor)
		}).
		Export("destroy")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			data, ok := s.bytesFor(descriptor)
			if !ok {
				return int32(stdInvalidDescriptor)
			}
			return int32(len(data))
		}).
		Export("buffer_len")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor int32, buffer, size uint32) int32 {
			data, ok := s.bytesFor(descriptor)
			if !ok {
				return int32(stdInvalidDescriptor)
			}
			if int(size) > len(data) {
				return int32(stdInvalidBufferSize)
			}
			if !m.Memory().Write(buffer, data[:size]) {
				return int32(stdFailedMemoryWrite)
			}
			return int32(stdSuccess)
		}).
		Export("read_buffer")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context) float64 {
			return float64(time.Now().UnixNano()) / 1e9
		}).
		Export("current_date")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context) int64 {
			_, offset := time.Now().Zone()
			return -int64(offset)
		}).
		Export("utc_offset")

	builder = builder.NewFunctionBuilder().
		WithFunc(parseDate).
		Export("parse_date")

	if s.PrintHandler != nil {
		builder = builder.NewFunctionBuilder().
			WithFunc(func(context.Context) {
				s.PrintHandler("Aborted")
			}).
			Export("abort")

		builder = builder.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, offset, length int32) {
				if offset < 0 || length < 0 {
					return
				}
				b, ok := m.Memory().Read(uint32(offset), uint32(length))
				if !ok {
					return
				}
				s.PrintHandler(string(b))
			}).
			Export("print")
	}

	return builder
}

func parseDate(
	ctx context.Context,
	m api.Module,
	stringPtr, stringLength, formatPtr, formatLength, localePtr, localeLength, timeZonePtr, timeZoneLength int32,
) float64 {
	if stringPtr < 0 || formatPtr < 0 {
		return float64(stdInvalidString)
	}
	strBytes, ok := m.Memory().Read(uint32(stringPtr), uint32(stringLength))
	if !ok {
		return float64(stdInvalidString)
	}
	formatBytes, ok := m.Memory().Read(uint32(formatPtr), uint32(formatLength))
	if !ok {
		return float64(stdInvalidString)
	}

	loc := time.UTC
	if timeZoneLength > 0 && timeZonePtr >= 0 {
		tzBytes, ok := m.Memory().Read(uint32(timeZonePtr), uint32(timeZoneLength))
		if ok {
			tz := string(tzBytes)
			if tz == "current" {
				loc = time.Local
			} else if l, err := time.LoadLocation(tz); err == nil {
				loc = l
			}
		}
	}
	_ = localePtr
	_ = localeLength // locale-aware month/day-name parsing is unsupported; see ldmlToGoLayout

	layout := ldmlToGoLayout(string(formatBytes))
	t, err := time.ParseInLocation(layout, string(strBytes), loc)
	if err != nil {
		return float64(stdInvalidDateString)
	}
	return float64(t.UnixNano()) / 1e9
}

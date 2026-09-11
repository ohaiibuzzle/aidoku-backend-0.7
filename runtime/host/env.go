package host

import (
	"context"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// Env implements the `env` namespace (Imports/Env.swift): abort, print,
// sleep, send_partial_result.
type Env struct {
	PrintHandler func(string)
	// OnPartialResult is invoked synchronously whenever the guest calls
	// send_partial_result, with the raw postcard-encoded payload. Set by
	// the runtime package for calls that stream partial results (get_home,
	// get_manga_update).
	OnPartialResult func(data []byte)
}

// maxEnvSleep caps env.sleep -- see envSleep's doc comment for why an
// unbounded duration can't just pass through to time.Sleep.
const maxEnvSleep = 30 * time.Second

func (e *Env) print(s string) {
	if e.PrintHandler != nil {
		e.PrintHandler(s)
	}
}

// capSleepDuration bounds a raw guest-supplied seconds value to at most
// maxEnvSleep. seconds <= 0 is the caller's responsibility to filter
// (envSleep does); this only handles the upper bound.
func capSleepDuration(seconds int32) time.Duration {
	d := time.Duration(seconds) * time.Second
	if d > maxEnvSleep {
		return maxEnvSleep
	}
	return d
}

// envSleep implements env.sleep. Guest calls are serialized behind
// Interpreter's single mutex (see runtime/interpreter.go), so an unbounded,
// ctx-blind time.Sleep here would block every other command against this
// source for as long as a huge or garbage seconds value asked -- capping
// the duration and honoring ctx cancellation avoids both.
func envSleep(ctx context.Context, seconds int32) {
	if seconds <= 0 {
		return
	}
	t := time.NewTimer(capSleepDuration(seconds))
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// LinkEnv registers the `env` namespace onto the given host module builder.
func LinkEnv(builder wazero.HostModuleBuilder, e *Env) wazero.HostModuleBuilder {
	builder = builder.NewFunctionBuilder().
		WithFunc(func(context.Context) {
			e.print("Aborted")
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
			e.print(string(b))
		}).
		Export("print")

	builder = builder.NewFunctionBuilder().
		WithFunc(envSleep).
		Export("sleep")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, valuePointer int32) {
			if valuePointer < 0 || e.OnPartialResult == nil {
				return
			}
			pointer := uint32(valuePointer)
			length, ok := m.Memory().ReadUint32Le(pointer)
			if !ok || length < 8 {
				return
			}
			// Reference implementation reads `length + 8` bytes here
			// (Env.swift's sendPartialResult), which appears to be a
			// copy/paste slip against the `length - 8` convention used
			// everywhere else results are decoded (Interpreter.swift's
			// handleResult). We use the consistent, safer `length - 8`:
			// trailing garbage either way is harmless since postcard
			// decoding stops once every field is read.
			data, ok := m.Memory().Read(pointer+8, length-8)
			if !ok {
				return
			}
			cp := make([]byte, len(data))
			copy(cp, data)
			e.OnPartialResult(cp)
		}).
		Export("send_partial_result")

	return builder
}

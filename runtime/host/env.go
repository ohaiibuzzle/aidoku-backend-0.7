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

func (e *Env) print(s string) {
	if e.PrintHandler != nil {
		e.PrintHandler(s)
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
		WithFunc(func(ctx context.Context, seconds int32) {
			if seconds <= 0 {
				return
			}
			time.Sleep(time.Duration(seconds) * time.Second)
		}).
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

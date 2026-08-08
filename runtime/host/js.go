package host

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"modernc.org/quickjs"
)

type jsResult int32

const (
	jsSuccess         jsResult = 0
	jsMissingResult   jsResult = -1
	jsInvalidContext  jsResult = -2
	jsInvalidString   jsResult = -3
	jsInvalidHandler  jsResult = -4
	jsInvalidRequest  jsResult = -5
	jsInvalidRuleList jsResult = -6
)

// JS implements the `js` namespace's context_* functions
// (Imports/JavaScript.swift), backed by modernc.org/quickjs (a pure-Go,
// ES2023-compliant QuickJS build), matching Swift's IsolatedJSContext (a
// fresh JS context per context_create call). webview_* — the rest of the
// `js` namespace — is implemented separately by WebView (see webview.go): a
// hand-rolled DOM binding over the same quickjs engine, since there's no
// real browser engine available on a headless cgo-free target. It covers
// straightforward JS/DOM work and simple timed challenges; it cannot clear
// interactive CAPTCHA/Cloudflare-Turnstile-style challenges that expect real
// browser capabilities (canvas rendering, etc.) — nothing short of a real
// browser or a CAPTCHA-solving service can, so those sources stay
// unsupported.
type JS struct {
	Store *Store
}

// LinkJS registers the `js` namespace onto the given host module builder.
func LinkJS(builder wazero.HostModuleBuilder, j *JS) wazero.HostModuleBuilder {
	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context) int32 {
			vm, err := quickjs.NewVM()
			if err != nil {
				return int32(jsInvalidContext)
			}
			return j.Store.Store(vm)
		}).
		Export("context_create")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, stringPointer, length int32) int32 {
			vm, ok := j.Store.Fetch(descriptor).(*quickjs.VM)
			if !ok {
				return int32(jsInvalidContext)
			}
			script, ok := readMemString(m, stringPointer, length)
			if !ok {
				return int32(jsInvalidString)
			}
			// EvalValue, not Eval: Eval's any-conversion eagerly
			// JSON-serializes an object completion value (see
			// modernc.org/quickjs's VM.newObject), which throws on a
			// circular object (e.g. a script that ends in `window` or
			// `document`, or one that builds a self-referencing structure)
			// even though the script itself ran fine. EvalValue defers that
			// conversion to safeStringify below, which tolerates it.
			result, err := vm.EvalValue(script, quickjs.EvalGlobal)
			if err != nil {
				return int32(jsMissingResult)
			}
			defer result.Free()
			if result.IsUndefined() {
				return int32(jsMissingResult)
			}
			return j.Store.Store(safeStringify(result))
		}).
		Export("context_eval")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, stringPointer, length int32) int32 {
			vm, ok := j.Store.Fetch(descriptor).(*quickjs.VM)
			if !ok {
				return int32(jsInvalidContext)
			}
			script, ok := readMemString(m, stringPointer, length)
			if !ok {
				return int32(jsInvalidString)
			}
			s, err := evalAsync(vm, script)
			if err != nil {
				return int32(jsMissingResult)
			}
			return j.Store.Store(s)
		}).
		Export("context_eval_async")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, stringPointer, length int32) int32 {
			vm, ok := j.Store.Fetch(descriptor).(*quickjs.VM)
			if !ok {
				return int32(jsInvalidContext)
			}
			key, ok := readMemString(m, stringPointer, length)
			if !ok {
				return int32(jsInvalidString)
			}
			atom, err := vm.NewAtom(key)
			if err != nil {
				return int32(jsMissingResult)
			}
			global := vm.GlobalObject()
			defer global.Free()
			// GetPropertyValue, not GetProperty — same EvalValue-vs-Eval
			// reasoning as context_eval above.
			v, err := vm.GetPropertyValue(global, atom)
			if err != nil {
				return int32(jsMissingResult)
			}
			defer v.Free()
			if v.IsUndefined() {
				return int32(jsMissingResult)
			}
			return j.Store.Store(safeStringify(v))
		}).
		Export("context_get")

	return builder
}

// stringify renders a quickjs.Eval/GetProperty result (any of string, int,
// bool, float64, *big.Int, *quickjs.Object, quickjs.Undefined, ...) the way
// Swift's IsolatedJSContext / goja's Value.String() did, for callers that
// only ever consumed the string form.
func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}

// safeStringify renders v as a string via stringify, but never lets a
// conversion the engine can't perform turn an otherwise-successful script
// run into a reported failure. Value.Any() (and VM.Eval's own any-
// conversion) JSON-serializes object results under the hood, which throws
// for a circular object or one with a BigInt-valued property; that failure
// means only "couldn't render this particular value as a string", not "the
// script threw" — a genuine JS exception is already reported via a separate
// Go error from Eval/EvalValue/Then before this is ever called — so it
// falls back to a plain "[object Object]" (matching what a real ToString on
// such an object would give) instead of propagating the error.
func safeStringify(v quickjs.Value) string {
	got, err := v.Any()
	if err != nil {
		return "[object Object]"
	}
	return stringify(got)
}

// evalAsync mirrors IsolatedJSContext.evaluateAsyncScript: wraps script in
// an async IIFE and awaits it. Since this sandbox has no real async I/O (no
// timers, no fetch), any await only ever waits on immediately-resolvable
// values, so draining the job queue once after Eval is always enough to
// settle the returned promise — no event loop needed.
func evalAsync(vm *quickjs.VM, script string) (string, error) {
	wrapped := fmt.Sprintf("(async () => { return await (%s); })()", script)
	result, err := vm.EvalValue(wrapped, quickjs.EvalGlobal)
	if err != nil {
		return "", err
	}
	defer result.Free()

	var settled string
	var settleErr error
	haveResult := false
	then, err := result.Then(
		func(v quickjs.Value) { settled = safeStringify(v); haveResult = true },
		func(v quickjs.Value) {
			settleErr = fmt.Errorf("js: promise rejected: %s", safeStringify(v))
			haveResult = true
		},
	)
	if err != nil {
		// Not a promise — evaluate to whatever the await produced directly.
		if result.IsUndefined() {
			return "", fmt.Errorf("js: async eval produced no result")
		}
		return safeStringify(result), nil
	}
	defer then.Free()

	for i := 0; i < 1000 && !haveResult; i++ {
		n, jobErr := vm.ExecutePendingJobs()
		if jobErr != nil {
			return "", jobErr
		}
		if n == 0 {
			break
		}
	}
	if !haveResult {
		return "", fmt.Errorf("js: promise still pending (no event loop available)")
	}
	if settleErr != nil {
		return "", settleErr
	}
	return settled, nil
}

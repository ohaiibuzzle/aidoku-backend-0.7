package host

import (
	"testing"

	"modernc.org/quickjs"
)

func TestJSEvalSync(t *testing.T) {
	vm, err := quickjs.NewVM()
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	defer vm.Close()
	result, err := vm.Eval("1+1", quickjs.EvalGlobal)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if stringify(result) != "2" {
		t.Fatalf("expected \"2\", got %q", stringify(result))
	}
}

func TestJSEvalAsyncResolve(t *testing.T) {
	vm, err := quickjs.NewVM()
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	defer vm.Close()
	s, err := evalAsync(vm, `new Promise((resolve) => { resolve("async test"); })`)
	if err != nil {
		t.Fatalf("evalAsync: %v", err)
	}
	if s != "async test" {
		t.Fatalf("expected \"async test\", got %q", s)
	}
}

func TestJSEvalAsyncReject(t *testing.T) {
	vm, err := quickjs.NewVM()
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	defer vm.Close()
	_, err = evalAsync(vm, `new Promise((_, reject) => { reject(new Error("boom")); })`)
	if err == nil {
		t.Fatalf("expected an error from a rejected promise")
	}
}

func TestJSEvalAsyncPlainValue(t *testing.T) {
	vm, err := quickjs.NewVM()
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	defer vm.Close()
	// Awaiting a non-promise value should just resolve to that value.
	s, err := evalAsync(vm, `42`)
	if err != nil {
		t.Fatalf("evalAsync: %v", err)
	}
	if s != "42" {
		t.Fatalf("expected \"42\", got %q", s)
	}
}

func TestJSContextGet(t *testing.T) {
	vm, err := quickjs.NewVM()
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	defer vm.Close()
	if _, err := vm.Eval(`globalThis.myGlobal = "hello";`, quickjs.EvalGlobal); err != nil {
		t.Fatalf("Eval: %v", err)
	}
	atom, err := vm.NewAtom("myGlobal")
	if err != nil {
		t.Fatalf("NewAtom: %v", err)
	}
	global := vm.GlobalObject()
	defer global.Free()
	v, err := vm.GetProperty(global, atom)
	if err != nil || v == nil || stringify(v) != "hello" {
		t.Fatalf("GetProperty(myGlobal): %v, err=%v", v, err)
	}
}

// A script whose completion value is a circular object (the classic case
// being `globalThis`/`window` itself) must not be reported as a script
// failure: Value.Any()/VM.Eval's any-conversion JSON-serializes object
// results under the hood, which throws on a cycle even though the script
// ran fine. safeStringify is what stands between that throw and callers.
func TestSafeStringifyCircular(t *testing.T) {
	vm, err := quickjs.NewVM()
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	defer vm.Close()

	v, err := vm.EvalValue(`(() => { const a = {}; a.self = a; return a; })()`, quickjs.EvalGlobal)
	if err != nil {
		t.Fatalf("EvalValue: %v", err)
	}
	defer v.Free()
	if v.IsUndefined() {
		t.Fatalf("expected a defined circular object, got undefined")
	}
	if got := safeStringify(v); got != "[object Object]" {
		t.Fatalf("expected a safe fallback string, got %q", got)
	}
}

func TestJSEvalAsyncCircularResult(t *testing.T) {
	vm, err := quickjs.NewVM()
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	defer vm.Close()
	s, err := evalAsync(vm, `(() => { const a = {}; a.self = a; return a; })()`)
	if err != nil {
		t.Fatalf("evalAsync: %v", err)
	}
	if s != "[object Object]" {
		t.Fatalf("expected a safe fallback string, got %q", s)
	}
}

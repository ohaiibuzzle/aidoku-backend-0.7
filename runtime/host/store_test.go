package host

import (
	"testing"

	"modernc.org/quickjs"
)

type fakeCloser struct{ closed int }

func (f *fakeCloser) Close() error {
	f.closed++
	return nil
}

func TestStoreRemoveClosesCloser(t *testing.T) {
	s := NewStore()
	c := &fakeCloser{}
	d := s.Store(c)

	s.Remove(d)

	if c.closed != 1 {
		t.Fatalf("expected Remove to close the item exactly once, closed=%d", c.closed)
	}
	if s.Fetch(d) != nil {
		t.Fatalf("expected descriptor to be gone after Remove")
	}
}

func TestStoreCloseSweepsRemainingCloser(t *testing.T) {
	s := NewStore()
	c1 := &fakeCloser{}
	c2 := &fakeCloser{}
	s.Store(c1)
	s.Store("plain string, not a Closer")
	s.Store(c2)

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if c1.closed != 1 || c2.closed != 1 {
		t.Fatalf("expected both closers closed exactly once, got %d and %d", c1.closed, c2.closed)
	}
}

// A *quickjs.VM (as stored by js.go's context_create and, indirectly, via
// webviewContext.Close in webview.go) must actually satisfy io.Closer so
// Remove/Close pick it up — this is what stands between a JS context and a
// permanently leaked native (non-Go-GC-visible) arena.
func TestStoreRemoveClosesRealVM(t *testing.T) {
	vm, err := quickjs.NewVM()
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	s := NewStore()
	d := s.Store(vm)
	s.Remove(d) // must not panic; VM.Close() runs exactly once here.
}

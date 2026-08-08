// Package host implements every WASM host namespace (env, std, defaults,
// net, html, js, canvas) that Aidoku source .wasm guests import, mirroring
// AidokuRunner's Imports/*.swift function-for-function. Each namespace
// registers its functions onto a wazero HostModuleBuilder and shares a
// single Store (the int32 handle table AidokuRunner calls GlobalStore).
package host

import (
	"io"
	"sync"
)

// Store is an int32 -> any handle table, matching AidokuRunner's
// GlobalStore.swift. Guest code never sees Go values directly; it only
// holds opaque descriptors returned by host functions and passes them back
// on later calls.
type Store struct {
	mu      sync.Mutex
	storage map[int32]any
	next    int32
}

func NewStore() *Store {
	return &Store{storage: make(map[int32]any), next: 1}
}

// Store saves an item and returns its descriptor.
func (s *Store) Store(item any) int32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	descriptor := s.next
	s.storage[descriptor] = item
	s.next++
	return descriptor
}

// Fetch returns the item at descriptor, or nil if absent.
func (s *Store) Fetch(descriptor int32) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.storage[descriptor]
}

// Set overwrites the item at an existing descriptor.
func (s *Store) Set(descriptor int32, item any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.storage[descriptor] = item
}

// Remove deletes the item at descriptor. Matches GlobalStore.swift's
// behavior of resetting the descriptor counter once the store empties, so
// long-lived interpreters don't monotonically drift toward int32 overflow.
//
// Most stored items (parsed HTML nodes, strings, byte buffers, ...) are
// plain Go values the garbage collector reclaims on its own. A few —
// notably *quickjs.VM (see js.go/webview.go) — hold native, non-Go-GC-
// visible memory that a Close() must release explicitly, or it leaks for
// the life of the process. Remove closes any such item (anything
// implementing io.Closer) before dropping it, so a guest that calls
// std.destroy() on a JS/webview descriptor actually frees the VM behind it;
// Close (below) sweeps whatever a guest never explicitly destroyed.
func (s *Store) Remove(descriptor int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.storage[descriptor].(io.Closer); ok {
		_ = c.Close()
	}
	delete(s.storage, descriptor)
	if len(s.storage) == 0 {
		s.next = 1
	}
}

// Close releases every remaining item that implements io.Closer (in
// particular, any *quickjs.VM/*webviewContext a guest never explicitly
// destroyed via std.destroy) and clears the store. Intended for use at
// interpreter teardown, once no further host calls will touch the store.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	for descriptor, item := range s.storage {
		if c, ok := item.(io.Closer); ok {
			if err := c.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		delete(s.storage, descriptor)
	}
	s.next = 1
	return firstErr
}

package settingsstore

import (
	"path/filepath"
	"sync"
	"testing"
)

// TestSetValueConcurrent exercises the scenario that used to crash with
// "fatal error: concurrent map read and map write": net.send_all fans out
// one goroutine per descriptor, and more than one of them can reach
// SetValue (e.g. via defaults.set or FlareSolverr's persistUserAgent) at
// the same time. Run with -race to catch a regression even on a machine
// where the fatal error itself doesn't reproduce every time.
func TestSetValueConcurrent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "source." + string(rune('a'+i%26))
			if err := s.SetValue(key, int32(i)); err != nil {
				t.Errorf("SetValue: %v", err)
			}
		}(i)
	}
	wg.Wait()
}

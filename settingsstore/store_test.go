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

// TestSaveKeepsOtherProcessesChanges: two Stores opened on the same file
// stand in for two aidoku-run processes (e.g. a background prefetch and a
// "settings set"). Each one's save must keep the other's keys, not write
// its own stale snapshot over them.
func TestSaveKeepsOtherProcessesChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	a, err := Open(path)
	if err != nil {
		t.Fatalf("Open a: %v", err)
	}
	b, err := Open(path)
	if err != nil {
		t.Fatalf("Open b: %v", err)
	}

	if err := b.SetValue("src.userChoice", "new"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetValue("src.token", "abc"); err != nil {
		t.Fatal(err)
	}
	if err := b.Remove("src.token"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetValue("src.other", true); err != nil {
		t.Fatal(err)
	}

	c, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := c.Object("src.userChoice"); got != "new" {
		t.Errorf("userChoice = %v, want new (lost to the other store's save)", got)
	}
	if got := c.Object("src.token"); got != nil {
		t.Errorf("token = %v, want removed", got)
	}
	if got := c.Object("src.other"); got != true {
		t.Errorf("other = %v, want true", got)
	}
}

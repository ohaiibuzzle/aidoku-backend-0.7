package downloads

import (
	"testing"
	"time"
)

func TestAcquireLockSerializesSameChapter(t *testing.T) {
	dir := t.TempDir()

	first, err := AcquireLock(dir, "src", "manga", "ch1")
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		second, err := AcquireLock(dir, "src", "manga", "ch1")
		if err != nil {
			t.Errorf("second AcquireLock: %v", err)
			return
		}
		defer second.Release()
		close(acquired)
	}()

	select {
	case <-acquired:
		t.Fatal("second AcquireLock returned before first was released")
	case <-time.After(100 * time.Millisecond):
	}

	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second AcquireLock never returned after first Release")
	}
}

func TestAcquireLockDoesNotSerializeDifferentChapters(t *testing.T) {
	dir := t.TempDir()

	first, err := AcquireLock(dir, "src", "manga", "ch1")
	if err != nil {
		t.Fatalf("AcquireLock ch1: %v", err)
	}
	defer first.Release()

	second, err := AcquireLock(dir, "src", "manga", "ch2")
	if err != nil {
		t.Fatalf("AcquireLock ch2 blocked on an unrelated chapter's lock: %v", err)
	}
	defer second.Release()
}

func TestLockReleaseNilSafe(t *testing.T) {
	var l *Lock
	if err := l.Release(); err != nil {
		t.Fatalf("Release on nil *Lock: %v", err)
	}
}

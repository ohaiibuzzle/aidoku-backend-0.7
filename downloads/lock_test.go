package downloads

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAcquireLockSerializesSameChapter(t *testing.T) {
	first, err := AcquireLock(context.Background(), "src", "manga", "ch1")
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		second, err := AcquireLock(context.Background(), "src", "manga", "ch1")
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
	first, err := AcquireLock(context.Background(), "src", "manga", "ch1")
	if err != nil {
		t.Fatalf("AcquireLock ch1: %v", err)
	}
	defer first.Release()

	second, err := AcquireLock(context.Background(), "src", "manga", "ch2")
	if err != nil {
		t.Fatalf("AcquireLock ch2 blocked on an unrelated chapter's lock: %v", err)
	}
	defer second.Release()
}

func TestAcquireLockHonorsContext(t *testing.T) {
	first, err := AcquireLock(context.Background(), "src", "manga", "ch-ctx")
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	defer first.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	second, err := AcquireLock(ctx, "src", "manga", "ch-ctx")
	if !errors.Is(err, ErrLockBusy) || !errors.Is(err, context.DeadlineExceeded) {
		second.Release()
		t.Fatalf("AcquireLock on a held lock with an expiring ctx = %v, want ErrLockBusy+DeadlineExceeded", err)
	}
}

func TestAcquirePathLockSerializesSamePath(t *testing.T) {
	first, err := AcquirePathLock(context.Background(), "/x/Manga/Omake.cbz")
	if err != nil {
		t.Fatalf("AcquirePathLock: %v", err)
	}
	defer first.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	// Case-insensitive on purpose (FAT32/exFAT).
	if second, err := AcquirePathLock(ctx, "/x/Manga/omake.cbz"); !errors.Is(err, ErrLockBusy) {
		second.Release()
		t.Fatalf("second AcquirePathLock on the same path = %v, want ErrLockBusy", err)
	}
	other, err := AcquirePathLock(context.Background(), "/x/Manga/Other.cbz")
	if err != nil {
		t.Fatalf("AcquirePathLock on a different path: %v", err)
	}
	other.Release()
}

func TestLockReleaseNilSafe(t *testing.T) {
	var l *Lock
	if err := l.Release(); err != nil {
		t.Fatalf("Release on nil *Lock: %v", err)
	}
}

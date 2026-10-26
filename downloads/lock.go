package downloads

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Lock provides cross-process mutual exclusion for downloading one
// (sourceKey, mangaKey, chapterKey) chapter, via flock(2) on a marker file
// under a fixed directory in os.TempDir() -- not the caller's downloads
// directory, since KOReader's Ephemeral Mode can relocate that mid-session.
// This exists because aidoku-run isn't a daemon (every invocation is a
// fresh process), so no in-process mutex can coordinate two invocations
// downloading the same chapter at once; a Lua-side lock was tried instead
// and reverted, since KOReader's Trapper can report a download "cancelled"
// (releasing the lock) while the spawned process keeps running regardless.
//
// Lock files are never deleted: unlinking one out from under a waiter
// wouldn't invalidate that waiter's lock, but a later caller opening the
// same path would get a different inode with its own lock domain, silently
// defeating mutual exclusion. Leaving these empty markers in os.TempDir()
// (tmpfs on target devices, so a reboot clears them) is a negligible cost.
type Lock struct {
	f *os.File
}

// lockDir returns (creating it if needed) the fixed directory holding every
// chapter's flock marker file. See the package doc above for why this is a
// caller-independent location rather than something derived from the
// downloads directory being locked against.
func lockDir() (string, error) {
	dir := filepath.Join(os.TempDir(), "aidoku-run-locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// lockPath returns the flock marker file for one lock key. The key's parts
// (sourceKey/mangaKey/chapterKey, or a path) can be arbitrary source-provided strings (URLs,
// slugs, ...) unsafe to use as a filename directly, so this hashes them
// into a fixed-width hex name -- same reasoning as sanitizeFilename in
// source/download.go, but simpler since a lock file's name is never shown
// to a user.
func lockPath(key string) (string, error) {
	dir, err := lockDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".lock"), nil
}

// ErrLockBusy is returned (wrapping ctx's error) when ctx ends while another
// process still holds the lock being waited on.
var ErrLockBusy = errors.New("another download is already running for this chapter")

// lockPollInterval is how often a waiter retries a held lock; flock itself
// can't be interrupted by ctx.
const lockPollInterval = 250 * time.Millisecond

// AcquireLock waits until the caller exclusively owns the download of
// (sourceKey, mangaKey, chapterKey), or until ctx ends (ErrLockBusy), then
// returns a Lock whose Release must be called exactly once (typically via
// defer) regardless of how the download that follows turns out. A caller
// that acquires the lock should re-check the downloads index before doing
// any real work: whoever held the lock before it may have just finished
// downloading the exact same chapter while this call was waiting.
func AcquireLock(ctx context.Context, sourceKey, mangaKey, chapterKey string) (*Lock, error) {
	return acquire(ctx, sourceKey+"\x00"+mangaKey+"\x00"+chapterKey)
}

// AcquirePathLock is AcquireLock keyed on an output file path instead, so
// two different chapters whose titles sanitize to the same filename can't
// write it at once. Take it after the chapter's own AcquireLock (fixed
// order, so two callers can't deadlock). The key is lowercased: FAT32/exFAT
// treat paths differing only in case as the same file.
func AcquirePathLock(ctx context.Context, path string) (*Lock, error) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	// One NUL, vs AcquireLock's two -- the key spaces can't collide.
	return acquire(ctx, "path\x00"+strings.ToLower(path))
}

func acquire(ctx context.Context, key string) (*Lock, error) {
	path, err := lockPath(key)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &Lock{f: f}, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EINTR {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, fmt.Errorf("%w: %w", ErrLockBusy, ctx.Err())
		case <-time.After(lockPollInterval):
		}
	}
}

// Release unlocks and closes the lock file. Safe to call on a nil *Lock (a
// caller that failed to acquire one has nothing to release).
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	closeErr := l.f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

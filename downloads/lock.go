package downloads

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"syscall"
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

// lockPath returns the flock marker file for one chapter. sourceKey/
// mangaKey/chapterKey can be arbitrary source-provided strings (URLs,
// slugs, ...) unsafe to use as a filename directly, so this hashes them
// into a fixed-width hex name -- same reasoning as sanitizeFilename in
// source/download.go, but simpler since a lock file's name is never shown
// to a user.
func lockPath(sourceKey, mangaKey, chapterKey string) (string, error) {
	dir, err := lockDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(sourceKey + "\x00" + mangaKey + "\x00" + chapterKey))
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".lock"), nil
}

// AcquireLock blocks until the caller exclusively owns the download of
// (sourceKey, mangaKey, chapterKey), then returns a Lock whose Release must
// be called exactly once (typically via defer) regardless of how the
// download that follows turns out. A caller that acquires the lock should
// re-check the downloads index before doing any real work: whoever held the
// lock before it may have just finished downloading the exact same chapter
// while this call was blocked waiting.
func AcquireLock(sourceKey, mangaKey, chapterKey string) (*Lock, error) {
	path, err := lockPath(sourceKey, mangaKey, chapterKey)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return &Lock{f: f}, nil
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

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
// under a fixed directory in os.TempDir().
//
// This exists because aidoku-run is not a daemon -- every invocation is a
// fresh OS process (see cmd/aidoku-run's own usage text) -- so an in-process
// mutex inside aidoku-run can never coordinate two concurrent "download this
// same chapter" invocations; each would start with an empty lock table. The
// KOReader plugin used to try to prevent this duplication itself (a Lua-side
// lock keyed the same way, released once its subprocess call returned), but
// that's the wrong layer for it: KOReader's Trapper can report a download as
// "cancelled" the instant an unrelated tap dismisses its progress widget
// (see subprocess.lua/engine.lua's notes on this), while the underlying
// aidoku-run process it spawned keeps running to completion regardless --
// confirmed on-device: a background prefetch's silent download got
// "cancelled" by a stray page-turn tap while genuinely still writing pages,
// and by the time the reader caught up to that same chapter moments later,
// Lua's own lock had already been released (because its subprocess call had
// returned, not because the chapter was actually done), so it fired a
// second full download that overlapped the still-running first one. flock
// is scoped to the OS file table, not to any one process's belief about
// whether its subprocess is still alive, so it can't be fooled the same way.
//
// The lock directory is deliberately not the caller's downloads directory
// (which is what this used to key off of, before this comment). Keying it
// there tied lock identity to a path that can itself change: the KOReader
// plugin's Ephemeral Mode redirects its downloads directory to a RAM disk
// and back depending on a Settings toggle, so two attempts at the exact
// same chapter straddling that toggle would have hashed into two different
// lock files, silently defeating mutual exclusion for that case. A fixed,
// caller-independent location closes that gap. It also matches the
// tmpfs-for-ephemeral-process-state precedent cmd/aidoku-run's own
// FlareSolverr cookie file already uses (os.TempDir()) -- flock's state
// dies with its holding process regardless of reboot, so nothing here ever
// needed to survive on persistent storage in the first place.
//
// The lock file is deliberately never deleted: unlinking a flock'd file
// while another process still holds an open fd to it doesn't invalidate
// that fd's lock, but a *third* caller opening the same path afterward
// would create a new inode with its own independent lock domain -- silently
// defeating mutual exclusion for exactly the callers this exists to
// serialize. These are empty marker files (one per chapter ever attempted
// since the last reboot -- os.TempDir() is expected to be tmpfs on target
// devices, so a reboot clears them for free), so leaving them in place is a
// negligible, fixed cost -- not worth the correctness risk of trying to
// clean them up.
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

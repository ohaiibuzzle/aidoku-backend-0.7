// Package fsutil holds small filesystem helpers shared across packages.
package fsutil

import (
	"errors"
	"os"
	"syscall"
)

// SyncDir fsyncs dir so a rename into it survives power loss. Filesystems
// that can't sync a directory (EINVAL/ENOTSUP, e.g. some FAT drivers) are
// not an error: there's nothing more to do there.
func SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) {
		err = nil
	}
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

//go:build !windows

package lock

import (
	"os"
	"syscall"
)

// tryLock attempts a non-blocking exclusive flock on the open file.
func tryLock(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) //nolint:gosec // G115: fd is a valid int on Unix.
	if err != nil {
		return wrapLocked(f.Name())
	}
	return nil
}

// unlock releases the flock.
func unlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:gosec // G115: fd is a valid int on Unix.
}

//go:build windows

package lock

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	modkernel32      = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = modkernel32.NewProc("LockFileEx")
	procUnlockFileEx = modkernel32.NewProc("UnlockFileEx")
)

const (
	// lockfileExclusiveLock requests an exclusive lock.
	lockfileExclusiveLock = 0x02
	// lockfileFailImmediately makes the call non-blocking.
	lockfileFailImmediately = 0x01
)

// tryLock attempts a non-blocking exclusive lock via LockFileEx.
func tryLock(f *os.File) error {
	var ol syscall.Overlapped
	r1, _, err := procLockFileEx.Call(
		f.Fd(),
		uintptr(lockfileExclusiveLock|lockfileFailImmediately),
		0,                                    // reserved
		1,                                    // nNumberOfBytesToLockLow
		0,                                    // nNumberOfBytesToLockHigh
		uintptr(unsafe.Pointer(&ol)), //nolint:gosec // G103: required for Win32 syscall.
	)
	if r1 == 0 {
		_ = err // suppress; we wrap our own.
		return wrapLocked(f.Name())
	}
	return nil
}

// unlock releases the lock via UnlockFileEx.
func unlock(f *os.File) error {
	var ol syscall.Overlapped
	r1, _, err := procUnlockFileEx.Call(
		f.Fd(),
		0,                                    // reserved
		1,                                    // nNumberOfBytesToUnlockLow
		0,                                    // nNumberOfBytesToUnlockHigh
		uintptr(unsafe.Pointer(&ol)), //nolint:gosec // G103: required for Win32 syscall.
	)
	if r1 == 0 {
		return err
	}
	return nil
}

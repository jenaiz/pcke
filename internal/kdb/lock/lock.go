// Package lock provides exclusive file locking for kdb databases.
//
// It uses flock(2) on Unix systems and LockFileEx on Windows.
// When acquired, a LOCK file is created containing the PID of the
// owning process for diagnostics. The lock is released by calling
// Unlock or when the process exits.
//
// Phase 0 — Task T1.
package lock

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ErrDBLocked indicates another process holds the exclusive file lock.
var ErrDBLocked = errors.New("kdb: database is locked by another process")

// FileLock represents an exclusive file lock backed by the OS flock mechanism.
type FileLock struct {
	path string
	file *os.File
}

// Acquire attempts to obtain an exclusive, non-blocking file lock at path.
// On success it writes the current PID into the file for diagnostics.
// If another process already holds the lock, it returns ErrDBLocked.
func Acquire(path string) (*FileLock, error) {
	// Open or create the lock file with restricted permissions (0600).
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // G304: path is controlled by the caller (DB directory).
	if err != nil {
		return nil, fmt.Errorf("lock: open %s: %w", path, err)
	}

	if err := tryLock(f); err != nil {
		_ = f.Close()
		return nil, err
	}

	// Write PID for diagnostics.
	pid := strconv.Itoa(os.Getpid())
	if err := f.Truncate(0); err != nil {
		// Non-fatal: lock is held; PID write is best-effort.
		_ = f // keep the lock
	} else {
		_, _ = f.WriteAt([]byte(pid+"\n"), 0)
	}

	return &FileLock{path: path, file: f}, nil
}

// Unlock releases the file lock and removes the lock file.
func (fl *FileLock) Unlock() error {
	if fl.file == nil {
		return nil
	}
	err := unlock(fl.file)
	closeErr := fl.file.Close()
	fl.file = nil
	// Best-effort removal; another process may have already removed it.
	_ = os.Remove(fl.path)
	if err != nil {
		return err
	}
	return closeErr
}

// OwnerPID reads the PID written by the lock holder from the lock file at path.
// Returns 0 if the file doesn't exist or can't be parsed.
func OwnerPID(path string) int {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is controlled by the caller.
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

// wrapLocked wraps an OS error as ErrDBLocked with the holder's PID if available.
func wrapLocked(path string) error {
	pid := OwnerPID(path)
	if pid > 0 {
		return fmt.Errorf("lock: held by PID %d: %w", pid, ErrDBLocked)
	}
	return fmt.Errorf("lock: %w", ErrDBLocked)
}

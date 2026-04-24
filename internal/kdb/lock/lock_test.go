package lock_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/lock"
)

func TestAcquireAndUnlock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "LOCK")

	fl, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	// Lock file should exist with our PID.
	data, err := os.ReadFile(path) //nolint:gosec // G304: test-controlled path.
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}

	got := strings.TrimSpace(string(data))
	want := strconv.Itoa(os.Getpid())

	if got != want {
		t.Errorf("lock file PID = %q, want %q", got, want)
	}

	if err := fl.Unlock(); err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}
}

func TestDoubleAcquireReturnsErrDBLocked(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "LOCK")

	fl1, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer func() { _ = fl1.Unlock() }()

	// Second acquire on the same file should fail with ErrDBLocked.
	_, err = lock.Acquire(path)
	if err == nil {
		t.Fatal("second Acquire succeeded, want ErrDBLocked")
	}

	if !errors.Is(err, lock.ErrDBLocked) {
		t.Errorf("error = %v, want ErrDBLocked", err)
	}
}

func TestUnlockReleasesLock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "LOCK")

	fl1, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	if err := fl1.Unlock(); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	// Re-acquire should succeed.
	fl2, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("re-Acquire after Unlock: %v", err)
	}
	defer func() { _ = fl2.Unlock() }()
}

func TestUnlockIdempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "LOCK")

	fl, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	if err := fl.Unlock(); err != nil {
		t.Fatalf("first Unlock: %v", err)
	}

	// Second unlock should be a no-op, not error.
	if err := fl.Unlock(); err != nil {
		t.Errorf("second Unlock returned error: %v", err)
	}
}

func TestOwnerPIDNoFile(t *testing.T) {
	t.Parallel()

	pid := lock.OwnerPID("/nonexistent/path/LOCK")
	if pid != 0 {
		t.Errorf("OwnerPID on nonexistent file = %d, want 0", pid)
	}
}

func TestOwnerPIDGarbageContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "LOCK")
	if err := os.WriteFile(path, []byte("not-a-number\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	pid := lock.OwnerPID(path)
	if pid != 0 {
		t.Errorf("OwnerPID on garbage = %d, want 0", pid)
	}
}

func TestOwnerPIDValid(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "LOCK")
	if err := os.WriteFile(path, []byte("12345\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	pid := lock.OwnerPID(path)
	if pid != 12345 {
		t.Errorf("OwnerPID = %d, want 12345", pid)
	}
}

func TestAcquireBadPath(t *testing.T) {
	t.Parallel()

	_, err := lock.Acquire("/nonexistent/dir/LOCK")
	if err == nil {
		t.Fatal("Acquire on bad path should fail")
	}
}

func TestLockFilePermissions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "LOCK")

	fl, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer func() { _ = fl.Unlock() }()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	// On Unix, expect 0600 (owner rw only). Skip check on Windows.
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("lock file permissions = %o, want 0600", perm)
	}
}

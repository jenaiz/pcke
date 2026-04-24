package kdb_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/page"
)

// testDir creates a temporary directory for a test and returns its path.
func testDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func TestOpenCreatesLayout(t *testing.T) {
	dir := testDir(t)

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// .pcke directory must exist with correct perms.
	pckeDir := filepath.Join(dir, ".pcke")
	info, err := os.Stat(pckeDir)
	if err != nil {
		t.Fatalf("stat .pcke: %v", err)
	}
	if !info.IsDir() {
		t.Fatal(".pcke is not a directory")
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf(".pcke perms = %o, want 0700", perm)
		}
	}

	// data.kdb must exist with correct perms.
	dataPath := filepath.Join(pckeDir, "data.kdb")
	dInfo, err := os.Stat(dataPath)
	if err != nil {
		t.Fatalf("stat data.kdb: %v", err)
	}
	if runtime.GOOS != "windows" {
		if perm := dInfo.Mode().Perm(); perm != 0o600 {
			t.Errorf("data.kdb perms = %o, want 0600", perm)
		}
	}

	// LOCK file must exist.
	lockPath := filepath.Join(pckeDir, "LOCK")
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("stat LOCK: %v", err)
	}
}

func TestOpenInitialSize(t *testing.T) {
	dir := testDir(t)

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	size, err := db.FileSize()
	if err != nil {
		t.Fatalf("FileSize: %v", err)
	}

	wantSize := int64(kdb.GrowthChunk) * int64(page.Size)
	if size != wantSize {
		t.Errorf("initial size = %d, want %d", size, wantSize)
	}

	count, err := db.PageCount()
	if err != nil {
		t.Fatalf("PageCount: %v", err)
	}
	if count != int64(kdb.GrowthChunk) {
		t.Errorf("initial page count = %d, want %d", count, kdb.GrowthChunk)
	}
}

func TestGrowChunk(t *testing.T) {
	dir := testDir(t)

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Grow once.
	if err := db.Grow(); err != nil {
		t.Fatalf("Grow: %v", err)
	}

	size, err := db.FileSize()
	if err != nil {
		t.Fatalf("FileSize: %v", err)
	}

	wantSize := int64(2*kdb.GrowthChunk) * int64(page.Size)
	if size != wantSize {
		t.Errorf("after grow: size = %d, want %d", size, wantSize)
	}
}

func TestGrowVerifyWithStat(t *testing.T) {
	dir := testDir(t)

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Grow 3 times and verify with os.Stat each time.
	for i := 1; i <= 3; i++ {
		if err := db.Grow(); err != nil {
			t.Fatalf("Grow[%d]: %v", i, err)
		}

		dataPath := filepath.Join(db.Path(), "data.kdb")
		info, err := os.Stat(dataPath)
		if err != nil {
			t.Fatalf("stat[%d]: %v", i, err)
		}

		wantSize := int64((1+i)*kdb.GrowthChunk) * int64(page.Size)
		if info.Size() != wantSize {
			t.Errorf("stat[%d]: size = %d, want %d", i, info.Size(), wantSize)
		}
	}
}

func TestOpenExistingDB(t *testing.T) {
	dir := testDir(t)

	// Create and grow.
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Grow(); err != nil {
		t.Fatalf("Grow: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen — must not reinitialise.
	db2, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = db2.Close() }()

	size, err := db2.FileSize()
	if err != nil {
		t.Fatalf("FileSize: %v", err)
	}

	// Should still have 2 chunks (initial + 1 grow).
	wantSize := int64(2*kdb.GrowthChunk) * int64(page.Size)
	if size != wantSize {
		t.Errorf("reopen size = %d, want %d", size, wantSize)
	}
}

func TestCloseIdempotent(t *testing.T) {
	dir := testDir(t)

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close[1]: %v", err)
	}
	// Second close must not error.
	if err := db.Close(); err != nil {
		t.Errorf("Close[2]: %v", err)
	}
}

func TestClosedDBReturnsError(t *testing.T) {
	dir := testDir(t)

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := db.FileSize(); err != kdb.ErrDBClosed {
		t.Errorf("FileSize on closed DB: got %v, want ErrDBClosed", err)
	}
	if err := db.Grow(); err != kdb.ErrDBClosed {
		t.Errorf("Grow on closed DB: got %v, want ErrDBClosed", err)
	}
}

func TestOpenCloseIdempotent1000(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 1000× open/close in short mode")
	}

	dir := testDir(t)

	for i := range 1000 {
		db, err := kdb.Open(dir, nil)
		if err != nil {
			t.Fatalf("Open[%d]: %v", i, err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("Close[%d]: %v", i, err)
		}
	}

	// Verify the file is still correct after 1000 cycles.
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("final Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	size, err := db.FileSize()
	if err != nil {
		t.Fatalf("FileSize: %v", err)
	}

	wantSize := int64(kdb.GrowthChunk) * int64(page.Size)
	if size != wantSize {
		t.Errorf("size after 1000 cycles = %d, want %d", size, wantSize)
	}
}

func TestOpenLocked(t *testing.T) {
	dir := testDir(t)

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// A second Open on the same path must fail with ErrDBLocked.
	_, err = kdb.Open(dir, nil)
	if err == nil {
		t.Fatal("expected error on double open, got nil")
	}
}

func TestOpenBadPath(t *testing.T) {
	// Try to open a database under a path that can't be created.
	_, err := kdb.Open("/dev/null/impossible", nil)
	if err == nil {
		t.Fatal("expected error for bad path, got nil")
	}
}

func TestPageCountAfterMultipleGrows(t *testing.T) {
	dir := testDir(t)

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	grows := 5
	for i := range grows {
		if err := db.Grow(); err != nil {
			t.Fatalf("Grow[%d]: %v", i, err)
		}
	}

	count, err := db.PageCount()
	if err != nil {
		t.Fatalf("PageCount: %v", err)
	}

	want := int64((1 + grows) * kdb.GrowthChunk)
	if count != want {
		t.Errorf("PageCount = %d, want %d", count, want)
	}
}

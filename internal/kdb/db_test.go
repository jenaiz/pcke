package kdb_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/page"
	"github.com/jenaiz/pcke/internal/kdb/tx"
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

// ── Transaction API integration tests (T10) ──

func TestUpdatePutViewGet(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Write via Update.
	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		return wtx.Put([]byte("hello"), []byte("world"))
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Read via View.
	if err := db.View(ctx, func(rtx *tx.ReadTx) error {
		val, err := rtx.Get([]byte("hello"))
		if err != nil {
			return err
		}
		if string(val) != "world" {
			t.Errorf("Get = %q, want %q", val, "world")
		}
		return nil
	}); err != nil {
		t.Fatalf("View: %v", err)
	}
}

func TestUpdateMultipleKeys(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Insert 10 keys in a single transaction.
	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		for i := range 10 {
			key := []byte(fmt.Sprintf("key-%03d", i))
			val := []byte(fmt.Sprintf("val-%03d", i))
			if err := wtx.Put(key, val); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Verify all keys.
	if err := db.View(ctx, func(rtx *tx.ReadTx) error {
		for i := range 10 {
			key := []byte(fmt.Sprintf("key-%03d", i))
			val, err := rtx.Get(key)
			if err != nil {
				return fmt.Errorf("Get(%s): %w", key, err)
			}
			want := fmt.Sprintf("val-%03d", i)
			if string(val) != want {
				t.Errorf("Get(%s) = %q, want %q", key, val, want)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("View: %v", err)
	}
}

func TestUpdateDelete(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Insert.
	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		return wtx.Put([]byte("del-key"), []byte("bye"))
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Delete.
	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		return wtx.Delete([]byte("del-key"))
	}); err != nil {
		t.Fatalf("Update delete: %v", err)
	}

	// Verify deleted.
	if err := db.View(ctx, func(rtx *tx.ReadTx) error {
		_, err := rtx.Get([]byte("del-key"))
		if err == nil {
			t.Error("expected error for deleted key")
		}
		return nil
	}); err != nil {
		t.Fatalf("View: %v", err)
	}
}

func TestUpdateRollbackOnError(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	sentinel := errors.New("abort")

	// Failing transaction.
	err = db.Update(ctx, func(wtx *tx.WriteTx) error {
		if err := wtx.Put([]byte("ephemeral"), []byte("value")); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Update error: got %v, want %v", err, sentinel)
	}

	// Subsequent update should succeed (writer lock released).
	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		return wtx.Put([]byte("real"), []byte("data"))
	}); err != nil {
		t.Fatalf("Update after rollback: %v", err)
	}
}

func TestViewOnClosedDB(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = db.Close()

	ctx := context.Background()
	err = db.View(ctx, func(_ *tx.ReadTx) error { return nil })
	if !errors.Is(err, kdb.ErrDBClosed) {
		t.Errorf("View on closed DB: got %v, want ErrDBClosed", err)
	}
}

func TestUpdateOnClosedDB(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = db.Close()

	ctx := context.Background()
	err = db.Update(ctx, func(_ *tx.WriteTx) error { return nil })
	if !errors.Is(err, kdb.ErrDBClosed) {
		t.Errorf("Update on closed DB: got %v, want ErrDBClosed", err)
	}
}

func TestConcurrentViews(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Insert seed data.
	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		return wtx.Put([]byte("shared"), []byte("value"))
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Run 10 concurrent Views.
	var wg sync.WaitGroup
	errs := make([]error, 10)
	for i := range 10 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = db.View(ctx, func(rtx *tx.ReadTx) error {
				val, err := rtx.Get([]byte("shared"))
				if err != nil {
					return err
				}
				if string(val) != "value" {
					return fmt.Errorf("got %q", val)
				}
				return nil
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("View[%d]: %v", i, err)
		}
	}
}

func TestPersistAcrossReopen(t *testing.T) {
	dir := testDir(t)
	ctx := context.Background()

	// Write data and close.
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		return wtx.Put([]byte("persist"), []byte("ok"))
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen and read.
	db2, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = db2.Close() }()

	if err := db2.View(ctx, func(rtx *tx.ReadTx) error {
		val, err := rtx.Get([]byte("persist"))
		if err != nil {
			return err
		}
		if string(val) != "ok" {
			t.Errorf("Get = %q, want %q", val, "ok")
		}
		return nil
	}); err != nil {
		t.Fatalf("View: %v", err)
	}
}

func TestCheckpointReducesWAL(t *testing.T) {
	dir := testDir(t)
	ctx := context.Background()

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Grow the file to have enough free pages for many inserts.
	for range 4 {
		if err := db.Grow(); err != nil {
			t.Fatalf("Grow: %v", err)
		}
	}

	// Insert some data to generate WAL records.
	for i := range 20 {
		if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
			key := fmt.Sprintf("key-%04d", i)
			val := fmt.Sprintf("value-%04d", i)
			return wtx.Put([]byte(key), []byte(val))
		}); err != nil {
			t.Fatalf("Update(%d): %v", i, err)
		}
	}

	// WAL should be non-empty now.
	walSizeBefore := walFileSize(t, dir)
	if walSizeBefore == 0 {
		t.Fatal("WAL should be non-empty after 20 inserts")
	}

	// Checkpoint should flush and truncate WAL.
	if err := db.Checkpoint(ctx); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	walSizeAfter := walFileSize(t, dir)
	if walSizeAfter >= walSizeBefore {
		t.Errorf("WAL size after checkpoint (%d) should be less than before (%d)",
			walSizeAfter, walSizeBefore)
	}
	if walSizeAfter != 0 {
		t.Errorf("WAL size after checkpoint = %d, want 0", walSizeAfter)
	}

	// Data should still be readable after checkpoint.
	if err := db.View(ctx, func(rtx *tx.ReadTx) error {
		val, err := rtx.Get([]byte("key-0010"))
		if err != nil {
			return err
		}
		if string(val) != "value-0010" {
			t.Errorf("Get = %q, want %q", val, "value-0010")
		}
		return nil
	}); err != nil {
		t.Fatalf("View after checkpoint: %v", err)
	}
}

func TestCheckpointDataSurvivesReopen(t *testing.T) {
	dir := testDir(t)
	ctx := context.Background()

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Insert data.
	for i := range 50 {
		if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
			return wtx.Put([]byte(fmt.Sprintf("k%d", i)), []byte(fmt.Sprintf("v%d", i)))
		}); err != nil {
			t.Fatalf("Update(%d): %v", i, err)
		}
	}

	// Checkpoint.
	if err := db.Checkpoint(ctx); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	// Close and reopen.
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer func() { _ = db2.Close() }()

	// All data should be accessible.
	if err := db2.View(ctx, func(rtx *tx.ReadTx) error {
		for i := range 50 {
			val, err := rtx.Get([]byte(fmt.Sprintf("k%d", i)))
			if err != nil {
				return fmt.Errorf("Get k%d: %w", i, err)
			}
			if string(val) != fmt.Sprintf("v%d", i) {
				t.Errorf("k%d = %q, want %q", i, val, fmt.Sprintf("v%d", i))
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("View after reopen: %v", err)
	}
}

func TestCheckpointOnClosedDB(t *testing.T) {
	dir := testDir(t)

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	err = db.Checkpoint(context.Background())
	if !errors.Is(err, kdb.ErrDBClosed) {
		t.Errorf("Checkpoint on closed DB: got %v, want ErrDBClosed", err)
	}
}

// walFileSize returns the total WAL file size in bytes across all segments.
func walFileSize(t *testing.T, dir string) int64 {
	t.Helper()
	walDir := filepath.Join(dir, ".pcke")
	entries, err := os.ReadDir(walDir)
	if err != nil {
		t.Fatalf("read WAL dir: %v", err)
	}
	var total int64
	for _, e := range entries {
		if len(e.Name()) > 4 && e.Name()[:4] == "wal-" {
			info, err := e.Info()
			if err != nil {
				t.Fatalf("stat WAL segment: %v", err)
			}
			total += info.Size()
		}
	}
	return total
}

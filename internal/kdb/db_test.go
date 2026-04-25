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

// TestSnapshotIsolation verifies that N concurrent readers see a consistent
// snapshot while a writer is actively modifying the tree. This is the F2.T1
// acceptance test: N readers vs writer under the race detector.
func TestSnapshotIsolation(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	for range 20 {
		if err := db.Grow(); err != nil {
			t.Fatalf("Grow: %v", err)
		}
	}

	snapSeedKeys(ctx, t, db)

	const numReaders = 8
	const readerIterations = 50

	var wg sync.WaitGroup
	errs := make(chan error, numReaders+1)

	for r := range numReaders {
		wg.Add(1)
		go snapReader(ctx, db, r, readerIterations, &wg, errs)
	}

	wg.Add(1)
	go snapWriter(ctx, db, &wg, errs)

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

func snapSeedKeys(ctx context.Context, t *testing.T, db *kdb.DB) {
	t.Helper()
	err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		for i := range 100 {
			key := fmt.Sprintf("key-%05d", i)
			val := fmt.Sprintf("val-v0-%05d", i)
			if err := wtx.Put([]byte(key), []byte(val)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func snapReader(ctx context.Context, db *kdb.DB, id, iters int, wg *sync.WaitGroup, errs chan<- error) {
	defer wg.Done()
	for i := range iters {
		err := db.View(ctx, func(rtx *tx.ReadTx) error {
			c := rtx.Cursor()
			if !c.First() {
				return nil
			}
			count := 0
			for c.Valid() {
				count++
				if !c.Next() {
					break
				}
			}
			if count < 50 {
				return fmt.Errorf("reader %d iter %d: only %d keys (expected ≥50)", id, i, count)
			}
			return nil
		})
		if err != nil {
			errs <- err
			return
		}
	}
}

func snapWriter(ctx context.Context, db *kdb.DB, wg *sync.WaitGroup, errs chan<- error) {
	defer wg.Done()
	for v := 1; v <= 20; v++ {
		err := db.Update(ctx, func(wtx *tx.WriteTx) error {
			for i := range 100 {
				key := fmt.Sprintf("key-%05d", i)
				val := fmt.Sprintf("val-v%d-%05d", v, i)
				if err := wtx.Put([]byte(key), []byte(val)); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			errs <- fmt.Errorf("writer v%d: %w", v, err)
			return
		}
	}
}

// ---------- Additional coverage tests ----------

// TestStats verifies that Stats returns valid diagnostic counters
// on both empty and populated databases.
func TestStats(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Stats on fresh db should work.
	s, err := db.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if s.DataFileBytes == 0 {
		t.Error("DataFileBytes should be > 0")
	}
	if s.PageCount == 0 {
		t.Error("PageCount should be > 0")
	}
	t.Logf("empty db: pages=%d keys=%d depth=%d free=%d",
		s.PageCount, s.KeyCount, s.TreeDepth, s.FreePageCount)

	// Insert some data and re-check.
	ctx := context.Background()

	for range 10 {
		if err := db.Grow(); err != nil {
			t.Fatalf("Grow: %v", err)
		}
	}

	err = db.Update(ctx, func(wtx *tx.WriteTx) error {
		for i := range 50 {
			k := fmt.Sprintf("stat-key-%03d", i)
			v := fmt.Sprintf("stat-val-%03d", i)
			if err := wtx.Put([]byte(k), []byte(v)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	s2, err := db.Stats()
	if err != nil {
		t.Fatalf("Stats after insert: %v", err)
	}
	if s2.KeyCount < 50 {
		t.Errorf("KeyCount = %d, want >= 50", s2.KeyCount)
	}
	if s2.TreeDepth == 0 {
		t.Error("TreeDepth should be > 0 after inserts")
	}
	t.Logf("populated db: pages=%d keys=%d depth=%d wal=%d",
		s2.PageCount, s2.KeyCount, s2.TreeDepth, s2.WALTotalBytes)
}

// TestStatsOnClosedDB verifies Stats returns ErrDBClosed.
func TestStatsOnClosedDB(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = db.Close()

	_, err = db.Stats()
	if !errors.Is(err, kdb.ErrDBClosed) {
		t.Errorf("Stats on closed db: got %v, want ErrDBClosed", err)
	}
}

// TestIndexAccessors verifies that ModuleIndex, TagIndex, FileIndex, and
// TypeIndex return non-nil secondary indexes.
func TestIndexAccessors(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	if db.ModuleIndex() == nil {
		t.Error("ModuleIndex() returned nil")
	}
	if db.TagIndex() == nil {
		t.Error("TagIndex() returned nil")
	}
	if db.FileIndex() == nil {
		t.Error("FileIndex() returned nil")
	}
	if db.TypeIndex() == nil {
		t.Error("TypeIndex() returned nil")
	}
}

// TestTestSubsystems verifies that TestSubsystems returns non-nil
// pool and freelist.
func TestTestSubsystems(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	pool, fl := db.TestSubsystems()
	if pool == nil {
		t.Error("TestSubsystems pool is nil")
	}
	if fl == nil {
		t.Error("TestSubsystems freelist is nil")
	}
}

// TestPath verifies the Path method returns the .pcke directory.
func TestPath(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	got := db.Path()
	want := filepath.Join(dir, ".pcke")
	if got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

// TestCheckpointAfterInserts verifies checkpoint + reopen preserves
// all data and doesn't leave stale WAL entries.
func TestCheckpointAfterInserts(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	ctx := context.Background()

	// Pre-grow so we have space.
	for range 30 {
		if err := db.Grow(); err != nil {
			t.Fatalf("Grow: %v", err)
		}
	}

	// Insert 200 keys.
	for batch := range 4 {
		err := db.Update(ctx, func(wtx *tx.WriteTx) error {
			for i := range 50 {
				key := fmt.Sprintf("cp-key-%03d", batch*50+i)
				val := fmt.Sprintf("cp-val-%03d", batch*50+i)
				if err := wtx.Put([]byte(key), []byte(val)); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("Update batch %d: %v", batch, err)
		}
	}

	// Checkpoint.
	if err := db.Checkpoint(ctx); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	// Stats after checkpoint.
	s, err := db.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if s.KeyCount < 200 {
		t.Errorf("KeyCount = %d, want >= 200", s.KeyCount)
	}

	_ = db.Close()

	// Reopen and verify.
	db2, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer func() { _ = db2.Close() }()

	count := 0
	err = db2.View(ctx, func(rtx *tx.ReadTx) error {
		c := rtx.Cursor()
		for ok := c.First(); ok; ok = c.Next() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if count < 200 {
		t.Errorf("after reopen: %d keys, want >= 200", count)
	}
}

// TestCursorIteration verifies full cursor traversal.
func TestCursorIteration(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	const n = 100

	// Pre-grow for enough pages.
	for range 10 {
		if err := db.Grow(); err != nil {
			t.Fatalf("Grow: %v", err)
		}
	}

	// Insert N keys.
	err = db.Update(ctx, func(wtx *tx.WriteTx) error {
		for i := range n {
			k := fmt.Sprintf("iter-%04d", i)
			v := fmt.Sprintf("val-%04d", i)
			if err := wtx.Put([]byte(k), []byte(v)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Count via cursor.
	var count int
	err = db.View(ctx, func(rtx *tx.ReadTx) error {
		c := rtx.Cursor()
		for ok := c.First(); ok; ok = c.Next() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if count != n {
		t.Errorf("cursor count = %d, want %d", count, n)
	}
}

// TestCursorSeek verifies cursor seek and prefix scan.
func TestCursorSeek(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Pre-grow for enough pages.
	for range 5 {
		if err := db.Grow(); err != nil {
			t.Fatalf("Grow: %v", err)
		}
	}

	// Insert keys with different prefixes.
	err = db.Update(ctx, func(wtx *tx.WriteTx) error {
		keys := []string{
			"aaa:1", "aaa:2", "aaa:3",
			"bbb:1", "bbb:2",
			"ccc:1",
		}
		for _, k := range keys {
			if err := wtx.Put([]byte(k), []byte("v")); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Seek to "bbb:" prefix.
	var found []string
	err = db.View(ctx, func(rtx *tx.ReadTx) error {
		c := rtx.Cursor()
		for ok := c.Seek([]byte("bbb:")); ok; ok = c.Next() {
			k := string(c.Key())
			if k[:4] != "bbb:" {
				break
			}
			found = append(found, k)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if len(found) != 2 {
		t.Errorf("seek bbb: got %v, want 2 keys", found)
	}
}

// TestUpdateDeleteAll verifies deleting all keys leaves empty tree.
func TestUpdateDeleteAll(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	keys := []string{"d-1", "d-2", "d-3", "d-4", "d-5"}

	// Pre-grow.
	for range 5 {
		if err := db.Grow(); err != nil {
			t.Fatalf("Grow: %v", err)
		}
	}

	// Insert.
	err = db.Update(ctx, func(wtx *tx.WriteTx) error {
		for _, k := range keys {
			if err := wtx.Put([]byte(k), []byte("val")); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Delete all.
	err = db.Update(ctx, func(wtx *tx.WriteTx) error {
		for _, k := range keys {
			if err := wtx.Delete([]byte(k)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify all gone.
	err = db.View(ctx, func(rtx *tx.ReadTx) error {
		for _, k := range keys {
			_, err := rtx.Get([]byte(k))
			if err == nil {
				return fmt.Errorf("key %q should be deleted", k)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestLargeValues verifies storing and retrieving large values.
func TestLargeValues(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Pre-grow for large values.
	for range 30 {
		if err := db.Grow(); err != nil {
			t.Fatalf("Grow: %v", err)
		}
	}

	// Store values of increasing size.
	sizes := []int{100, 1000, 4000, 8000, 16000}
	for _, sz := range sizes {
		key := fmt.Sprintf("large-%d", sz)
		val := make([]byte, sz)
		for i := range val {
			val[i] = byte(i % 256)
		}

		err := db.Update(ctx, func(wtx *tx.WriteTx) error {
			return wtx.Put([]byte(key), val)
		})
		if err != nil {
			t.Fatalf("Put %d bytes: %v", sz, err)
		}

		// Verify retrieval.
		err = db.View(ctx, func(rtx *tx.ReadTx) error {
			got, err := rtx.Get([]byte(key))
			if err != nil {
				return err
			}
			if len(got) != sz {
				return fmt.Errorf("got %d bytes, want %d", len(got), sz)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("Get %d bytes: %v", sz, err)
		}
	}
}

// TestOverwriteKey verifies that overwriting a key replaces its value.
func TestOverwriteKey(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	key := []byte("overwrite-key")

	// Pre-grow.
	for range 5 {
		if err := db.Grow(); err != nil {
			t.Fatalf("Grow: %v", err)
		}
	}

	// Write initial value.
	err = db.Update(ctx, func(wtx *tx.WriteTx) error {
		return wtx.Put(key, []byte("v1"))
	})
	if err != nil {
		t.Fatalf("Put v1: %v", err)
	}

	// Overwrite.
	err = db.Update(ctx, func(wtx *tx.WriteTx) error {
		return wtx.Put(key, []byte("v2"))
	})
	if err != nil {
		t.Fatalf("Put v2: %v", err)
	}

	// Verify latest value.
	err = db.View(ctx, func(rtx *tx.ReadTx) error {
		val, err := rtx.Get(key)
		if err != nil {
			return err
		}
		if string(val) != "v2" {
			return fmt.Errorf("got %q, want v2", val)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestGrowOnClosedDB verifies Grow returns ErrDBClosed.
func TestGrowOnClosedDB(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = db.Close()

	err = db.Grow()
	if !errors.Is(err, kdb.ErrDBClosed) {
		t.Errorf("Grow on closed: got %v, want ErrDBClosed", err)
	}
}

// TestFileSizeOnClosedDB verifies FileSize returns ErrDBClosed.
func TestFileSizeOnClosedDB(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = db.Close()

	_, err = db.FileSize()
	if !errors.Is(err, kdb.ErrDBClosed) {
		t.Errorf("FileSize on closed: got %v, want ErrDBClosed", err)
	}
}

// TestPageCountOnClosedDB verifies PageCount returns ErrDBClosed.
func TestPageCountOnClosedDB(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = db.Close()

	_, err = db.PageCount()
	if !errors.Is(err, kdb.ErrDBClosed) {
		t.Errorf("PageCount on closed: got %v, want ErrDBClosed", err)
	}
}

// TestMultipleCheckpoints exercises multiple checkpoint cycles.
func TestMultipleCheckpoints(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	for range 10 {
		if err := db.Grow(); err != nil {
			t.Fatalf("Grow: %v", err)
		}
	}

	// Run 3 cycles of insert + checkpoint.
	for cycle := range 3 {
		err := db.Update(ctx, func(wtx *tx.WriteTx) error {
			for i := range 30 {
				k := fmt.Sprintf("mc-%d-%03d", cycle, i)
				return wtx.Put([]byte(k), []byte("v"))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("Update cycle %d: %v", cycle, err)
		}

		if err := db.Checkpoint(ctx); err != nil {
			t.Fatalf("Checkpoint cycle %d: %v", cycle, err)
		}
	}

	// Verify stats.
	s, err := db.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if s.KeyCount == 0 {
		t.Error("expected keys after multiple checkpoints")
	}
}

// TestCompactOnPopulatedDB verifies compact works and preserves data.
func TestCompactOnPopulatedDB(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	ctx := context.Background()
	compactGrowDB(t, db)
	compactInsertKeys(ctx, t, db)
	compactDeleteEvenKeys(ctx, t, db)

	if err := db.Checkpoint(ctx); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	result, err := db.Compact(ctx)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	t.Logf("compact: %d keys, %d → %d bytes", result.KeysCopied, result.OldSize, result.NewSize)
	if result.KeysCopied < 50 {
		t.Errorf("KeysCopied = %d, want >= 50", result.KeysCopied)
	}

	compactVerifyOddKeys(ctx, t, db)
}

func compactGrowDB(t *testing.T, db *kdb.DB) {
	t.Helper()
	for range 20 {
		if err := db.Grow(); err != nil {
			t.Fatalf("Grow: %v", err)
		}
	}
}

func compactInsertKeys(ctx context.Context, t *testing.T, db *kdb.DB) {
	t.Helper()
	err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		for i := range 100 {
			k := fmt.Sprintf("compact-%04d", i)
			v := fmt.Sprintf("val-%04d", i)
			if err := wtx.Put([]byte(k), []byte(v)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
}

func compactDeleteEvenKeys(ctx context.Context, t *testing.T, db *kdb.DB) {
	t.Helper()
	err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		for i := 0; i < 100; i += 2 {
			k := fmt.Sprintf("compact-%04d", i)
			if err := wtx.Delete([]byte(k)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func compactVerifyOddKeys(ctx context.Context, t *testing.T, db *kdb.DB) {
	t.Helper()
	err := db.View(ctx, func(rtx *tx.ReadTx) error {
		for i := 1; i < 100; i += 2 {
			k := fmt.Sprintf("compact-%04d", i)
			if _, err := rtx.Get([]byte(k)); err != nil {
				return fmt.Errorf("missing key %s: %w", k, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestConcurrentViewAndUpdate ensures Views and Updates can run
// concurrently under the race detector.
func TestConcurrentViewAndUpdate(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	for range 10 {
		if err := db.Grow(); err != nil {
			t.Fatalf("Grow: %v", err)
		}
	}

	// Seed.
	err = db.Update(ctx, func(wtx *tx.WriteTx) error {
		for i := range 20 {
			return wtx.Put([]byte(fmt.Sprintf("cv-%03d", i)), []byte("seed"))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	// 4 readers.
	for r := range 4 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for range 20 {
				if err := db.View(ctx, func(rtx *tx.ReadTx) error {
					_, _ = rtx.Get([]byte("cv-000"))
					return nil
				}); err != nil {
					errs <- fmt.Errorf("reader %d: %w", id, err)
					return
				}
			}
		}(r)
	}

	// 1 writer.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 20 {
			if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
				return wtx.Put([]byte("cv-000"), []byte(fmt.Sprintf("w-%d", i)))
			}); err != nil {
				errs <- fmt.Errorf("writer: %w", err)
				return
			}
		}
	}()

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestCompactOnClosedDB verifies Compact returns ErrDBClosed.
func TestCompactOnClosedDB(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = db.Close()

	_, err = db.Compact(context.Background())
	if !errors.Is(err, kdb.ErrDBClosed) {
		t.Errorf("Compact on closed: got %v, want ErrDBClosed", err)
	}
}

// TestReopenAfterUpdates verifies WAL replay after writes without checkpoint.
// This exercises replayWAL and postReplayCommit paths.
func TestReopenAfterUpdates(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	ctx := context.Background()
	for range 10 {
		if err := db.Grow(); err != nil {
			t.Fatalf("Grow: %v", err)
		}
	}

	// Write data but do NOT checkpoint — leave WAL entries.
	err = db.Update(ctx, func(wtx *tx.WriteTx) error {
		for i := range 20 {
			k := fmt.Sprintf("replay-%03d", i)
			v := fmt.Sprintf("val-%03d", i)
			if err := wtx.Put([]byte(k), []byte(v)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	_ = db.Close()

	// Reopen — WAL replay should recover the data.
	db2, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer func() { _ = db2.Close() }()

	count := 0
	err = db2.View(ctx, func(rtx *tx.ReadTx) error {
		c := rtx.Cursor()
		for ok := c.First(); ok; ok = c.Next() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if count < 20 {
		t.Errorf("after replay: %d keys, want >= 20", count)
	}
}

// TestFileSize verifies FileSize returns a positive number.
func TestFileSize(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	sz, err := db.FileSize()
	if err != nil {
		t.Fatalf("FileSize: %v", err)
	}
	if sz <= 0 {
		t.Errorf("FileSize = %d, want > 0", sz)
	}
}

// TestGrowAndFileSize verifies that Grow increases the file size.
func TestGrowAndFileSize(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	before, err := db.FileSize()
	if err != nil {
		t.Fatalf("FileSize before: %v", err)
	}

	if err := db.Grow(); err != nil {
		t.Fatalf("Grow: %v", err)
	}

	after, err := db.FileSize()
	if err != nil {
		t.Fatalf("FileSize after: %v", err)
	}

	if after <= before {
		t.Errorf("file did not grow: %d → %d", before, after)
	}
}

// TestOpenWithNilOptions verifies Open works with nil options.
func TestOpenWithNilOptions(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = db.Close()
}

// TestDoubleOpen verifies that opening a DB that's already open fails with
// a lock error (since the lock is held).
func TestDoubleOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("lock behavior differs on Windows")
	}
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	_, err = kdb.Open(dir, nil)
	if err == nil {
		t.Fatal("second Open should fail with lock error")
	}
}

// TestMultipleReopenCycles ensures data survives many open/close cycles.
func TestMultipleReopenCycles(t *testing.T) {
	dir := testDir(t)
	ctx := context.Background()

	for cycle := range 5 {
		db, err := kdb.Open(dir, nil)
		if err != nil {
			t.Fatalf("Open cycle %d: %v", cycle, err)
		}

		for range 5 {
			if err := db.Grow(); err != nil {
				t.Fatalf("Grow: %v", err)
			}
		}

		// Write a key unique to this cycle.
		key := fmt.Sprintf("cycle-%d", cycle)
		err = db.Update(ctx, func(wtx *tx.WriteTx) error {
			return wtx.Put([]byte(key), []byte("v"))
		})
		if err != nil {
			t.Fatalf("Update cycle %d: %v", cycle, err)
		}

		// Checkpoint every other cycle.
		if cycle%2 == 0 {
			if err := db.Checkpoint(ctx); err != nil {
				t.Fatalf("Checkpoint cycle %d: %v", cycle, err)
			}
		}

		_ = db.Close()
	}

	// Final open — all cycle keys should exist.
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Final open: %v", err)
	}
	defer func() { _ = db.Close() }()

	err = db.View(ctx, func(rtx *tx.ReadTx) error {
		for cycle := range 5 {
			key := fmt.Sprintf("cycle-%d", cycle)
			if _, err := rtx.Get([]byte(key)); err != nil {
				return fmt.Errorf("missing %s: %w", key, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

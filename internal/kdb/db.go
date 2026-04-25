// Package kdb provides the embedded key-value storage engine for pcke.
//
// This file implements the database file layout: Open, Close, and chunk-based
// growth. The .pcke/ directory holds a .kdb data file and a LOCK file.
//
// Phase 0 — Tasks T3 + T10.
package kdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/bufpool"
	"github.com/jenaiz/pcke/internal/kdb/freelist"
	"github.com/jenaiz/pcke/internal/kdb/lock"
	"github.com/jenaiz/pcke/internal/kdb/tx"
	"github.com/jenaiz/pcke/internal/kdb/wal"
)

const (
	// GrowthChunk is the number of pages allocated per growth operation.
	GrowthChunk = 16

	// pageSize is the fixed page size in bytes (must match page.Size).
	pageSize = 4096

	// growthBytes is GrowthChunk pages in bytes.
	growthBytes = GrowthChunk * pageSize

	// dirName is the hidden directory inside the user-specified path.
	dirName = ".pcke"

	// dataFileName is the name of the KDB data file.
	dataFileName = "data.kdb"

	// lockFileName is the name of the exclusive lock file.
	lockFileName = "LOCK"

	// dirPerms is the permission mode for the .pcke directory.
	dirPerms = 0o700

	// filePerms is the permission mode for the .kdb data file.
	filePerms = 0o600
)

// Options configures a database open operation.
type Options struct {
	// ReadOnly opens the database in read-only mode (not yet implemented;
	// reserved for future use).
	ReadOnly bool
}

// DB represents an open kdb database.
type DB struct {
	mu     sync.Mutex
	rwmu   sync.RWMutex // serialises View (RLock) vs Update (Lock)
	dir    string       // path to .pcke directory
	file   *os.File
	flock  *lock.FileLock
	wal    *wal.WAL
	pool   *bufpool.Pool
	fl     *freelist.BTreeFreelist
	tree   *btree.Tree
	meta   *Meta
	closed bool
}

// Open opens (or creates) a kdb database rooted at path.
//
// It creates <path>/.pcke/ (0700) if needed, acquires an exclusive flock on
// <path>/.pcke/LOCK, and creates/opens the data.kdb file (0600). A new
// database starts with GrowthChunk (16) pages.
//
// After file setup, Open wires: WAL → replay → bufpool → freelist (B+tree) →
// B+tree. The database is ready for View/Update calls.
//
// Passing nil for opts uses default options.
func Open(path string, opts *Options) (*DB, error) {
	_ = opts // reserved for future use

	dir := filepath.Join(path, dirName)
	if err := os.MkdirAll(dir, dirPerms); err != nil {
		return nil, fmt.Errorf("kdb: create directory %s: %w", dir, err)
	}

	// Acquire exclusive lock.
	lockPath := filepath.Join(dir, lockFileName)
	fl, err := lock.Acquire(lockPath)
	if err != nil {
		return nil, err
	}

	// Open or create the data file.
	dataPath := filepath.Join(dir, dataFileName)
	f, err := os.OpenFile(dataPath, os.O_CREATE|os.O_RDWR, filePerms) //nolint:gosec // G304: path controlled by caller.
	if err != nil {
		_ = fl.Unlock()
		return nil, fmt.Errorf("kdb: open data file %s: %w", dataPath, err)
	}

	db := &DB{
		dir:   dir,
		file:  f,
		flock: fl,
	}

	// Initialise a new database with one growth chunk if the file is empty.
	if err := db.initIfEmpty(); err != nil {
		_ = f.Close()
		_ = fl.Unlock()
		return nil, err
	}

	// Wire subsystems.
	if err := db.wireSubsystems(); err != nil {
		_ = f.Close()
		_ = fl.Unlock()
		return nil, err
	}

	return db, nil
}

// Close releases all resources: flushes dirty pages, closes the WAL, syncs
// and closes the data file, then releases the exclusive flock.
// Close is idempotent — calling it on an already-closed DB returns nil.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return nil
	}
	db.closed = true

	var firstErr error

	// Flush dirty pages before closing.
	if db.pool != nil {
		if err := db.pool.FlushDirty(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("kdb: flush pool: %w", err)
		}
	}

	// Close WAL.
	if db.wal != nil {
		if err := db.wal.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("kdb: close wal: %w", err)
		}
	}

	if err := db.file.Sync(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("kdb: sync data file: %w", err)
	}
	if err := db.file.Close(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("kdb: close data file: %w", err)
	}
	if err := db.flock.Unlock(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("kdb: unlock: %w", err)
	}

	db.file = nil
	db.flock = nil
	db.wal = nil
	db.pool = nil
	db.fl = nil
	db.tree = nil

	return firstErr
}

// Path returns the .pcke directory path.
func (db *DB) Path() string {
	return db.dir
}

// FileSize returns the current size of the data file in bytes.
func (db *DB) FileSize() (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return 0, ErrDBClosed
	}

	info, err := db.file.Stat()
	if err != nil {
		return 0, fmt.Errorf("kdb: stat data file: %w", err)
	}
	return info.Size(), nil
}

// PageCount returns the number of pages in the data file.
func (db *DB) PageCount() (int64, error) {
	size, err := db.FileSize()
	if err != nil {
		return 0, err
	}
	return size / pageSize, nil
}

// Grow extends the data file by one growth chunk (16 pages = 65536 bytes).
// The new pages are zero-filled.
func (db *DB) Grow() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return ErrDBClosed
	}

	return db.grow()
}

// grow extends the file by one chunk. Caller must hold db.mu.
func (db *DB) grow() error {
	info, err := db.file.Stat()
	if err != nil {
		return fmt.Errorf("kdb: stat for grow: %w", err)
	}

	newSize := info.Size() + growthBytes
	if err := db.file.Truncate(newSize); err != nil {
		return fmt.Errorf("kdb: truncate to %d: %w", newSize, err)
	}

	if err := db.file.Sync(); err != nil {
		return fmt.Errorf("kdb: sync after grow: %w", err)
	}

	return nil
}

// initIfEmpty initialises a new database file with one growth chunk if the
// file is empty. Caller must hold no lock (called from Open before returning).
func (db *DB) initIfEmpty() error {
	info, err := db.file.Stat()
	if err != nil {
		return fmt.Errorf("kdb: stat data file: %w", err)
	}

	if info.Size() > 0 {
		return nil // existing database
	}

	// Allocate the initial chunk.
	if err := db.file.Truncate(growthBytes); err != nil {
		return fmt.Errorf("kdb: initial truncate: %w", err)
	}

	if err := db.file.Sync(); err != nil {
		return fmt.Errorf("kdb: sync after init: %w", err)
	}

	// Write initial meta pages (both slots, generation 1).
	if err := initMeta(db.file, GrowthChunk); err != nil {
		return fmt.Errorf("kdb: init meta: %w", err)
	}

	return nil
}

// ── FilePageIO adapter ──

// FilePageIO adapts an os.File to the bufpool.PageIO / freelist.PageIO interface.
type FilePageIO struct {
	file *os.File
}

// NewFilePageIO creates a PageIO backed by the given file.
func NewFilePageIO(f *os.File) *FilePageIO {
	return &FilePageIO{file: f}
}

// ReadPage reads a full 4096-byte page from the file.
func (fp *FilePageIO) ReadPage(pageID uint64) ([]byte, error) {
	buf := make([]byte, pageSize)
	offset := int64(pageID) * int64(pageSize) //nolint:gosec // G115: pageID bounded by file size.
	if _, err := fp.file.ReadAt(buf, offset); err != nil {
		return nil, fmt.Errorf("pageio: read page %d: %w", pageID, err)
	}
	return buf, nil
}

// WritePage writes a full 4096-byte page to the file.
func (fp *FilePageIO) WritePage(pageID uint64, buf []byte) error {
	offset := int64(pageID) * int64(pageSize) //nolint:gosec // G115: pageID bounded by file size.
	if _, err := fp.file.WriteAt(buf, offset); err != nil {
		return fmt.Errorf("pageio: write page %d: %w", pageID, err)
	}
	return nil
}

// Sync fsyncs the underlying file.
func (fp *FilePageIO) Sync() error {
	return fp.file.Sync()
}

// ── Subsystem wiring ──

// wireSubsystems initialises WAL, bufpool, freelist, and B+tree after file open.
func (db *DB) wireSubsystems() error {
	// Load meta.
	m, err := loadMeta(db.file)
	if err != nil {
		return fmt.Errorf("kdb: load meta: %w", err)
	}
	db.meta = m

	// Open WAL.
	w, err := wal.Open(db.dir)
	if err != nil {
		return fmt.Errorf("kdb: open wal: %w", err)
	}
	db.wal = w

	// Create PageIO adapter and buffer pool.
	pio := NewFilePageIO(db.file)
	db.pool = bufpool.New(pio, 256)

	// Set up freelist (B+tree format).
	if err := db.setupFreelist(m, pio); err != nil {
		_ = w.Close()
		return err
	}

	// Create the main B+tree, using the root from meta if available.
	// TreeRoot is 0 for a brand-new database; on subsequent opens it holds
	// the committed root from the last successful meta swap.
	db.tree = btree.New(m.TreeRoot, db.pool, db.fl)

	// Replay WAL to recover any in-flight transactions since the last commit.
	if err := db.replayWAL(); err != nil {
		_ = w.Close()
		return fmt.Errorf("kdb: replay wal: %w", err)
	}

	// After successful replay, persist the recovered state and truncate the WAL.
	// This ensures: (a) the tree root is up-to-date in meta, (b) the WAL doesn't
	// grow unboundedly, and (c) no stale records are replayed on the next open.
	if err := db.postReplayCommit(); err != nil {
		_ = w.Close()
		return fmt.Errorf("kdb: post-replay commit: %w", err)
	}

	return nil
}

// postReplayCommit flushes the tree state after WAL replay and truncates the WAL.
func (db *DB) postReplayCommit() error {
	// Flush any dirty pages from replay.
	if err := db.pool.FlushDirty(); err != nil {
		return fmt.Errorf("flush after replay: %w", err)
	}

	// Write meta with the current tree root.
	info, err := db.file.Stat()
	if err != nil {
		return fmt.Errorf("stat for post-replay meta: %w", err)
	}

	newMeta := &Meta{
		Version:        MetaVersion,
		Generation:     db.meta.Generation + 1,
		PageCount:      uint64(info.Size() / pageSize), //nolint:gosec // G115
		FreelistRoot:   db.fl.Root(),
		FreelistFormat: FreelistBTree,
		TreeRoot:       db.tree.Root(),
	}

	if err := swapMeta(db.file, newMeta); err != nil {
		return fmt.Errorf("swap meta after replay: %w", err)
	}
	db.meta = newMeta

	// Truncate WAL — all data is safely in the tree + meta.
	if err := db.wal.Truncate(); err != nil {
		return fmt.Errorf("truncate wal: %w", err)
	}

	return nil
}

// setupFreelist creates or migrates the freelist to B+tree format.
func (db *DB) setupFreelist(m *Meta, pio *FilePageIO) error {
	switch m.FreelistFormat {
	case FreelistLinkedList:
		return db.migrateFreelist(m, pio)
	case FreelistBTree:
		// Existing B+tree DB. Open with empty reserve — reserve self-fills
		// from Free() calls. Alloc/Delete don't need reserve (no merges in Phase 0).
		db.fl = freelist.OpenBTreeFreelist(db.pool, m.FreelistRoot, nil)
		return nil
	default:
		return fmt.Errorf("kdb: unknown freelist format %d", m.FreelistFormat)
	}
}

// reservePageCount is the number of reserve pages for BTreeFreelist.
const reservePageCount = 8

// persistMigration writes the updated meta after freelist migration.
func (db *DB) persistMigration(m *Meta) error {
	newMeta := &Meta{
		Version:        MetaVersion,
		Generation:     m.Generation + 1,
		PageCount:      m.PageCount,
		FreelistRoot:   db.fl.Root(),
		FreelistFormat: FreelistBTree,
		TreeRoot:       0, // No tree yet during migration.
	}
	if err := swapMeta(db.file, newMeta); err != nil {
		return fmt.Errorf("kdb: swap meta after migration: %w", err)
	}
	db.meta = newMeta
	return nil
}

// migrateFreelist migrates from linked-list (T4) to B+tree (T8) freelist.
func (db *DB) migrateFreelist(m *Meta, pio *FilePageIO) error {
	// For a new DB (generation 1, no freelist root), seed free pages from the
	// initial growth chunk. Pages 0 and 1 are meta; pages 2..PageCount-1 are free.
	// Use the first reservePageCount as reserve, rest go into freelist.
	if m.FreelistRoot == 0 && m.Generation <= 1 {
		return db.seedNewDB(m)
	}

	// Existing linked-list DB: migrate entries to B+tree.
	return db.migrateExistingFreelist(m, pio)
}

// seedNewDB initialises the freelist for a brand-new database.
func (db *DB) seedNewDB(m *Meta) error {
	// Collect all free pages (2..PageCount-1).
	var allFree []uint64
	for pgID := uint64(2); pgID < m.PageCount; pgID++ {
		allFree = append(allFree, pgID)
	}

	// Split into reserve and freelist pages.
	reserveCount := reservePageCount
	if reserveCount > len(allFree) {
		reserveCount = len(allFree)
	}
	reserve := allFree[:reserveCount]
	rest := allFree[reserveCount:]

	db.fl = freelist.OpenBTreeFreelist(db.pool, 0, reserve)

	// Add remaining pages to the freelist.
	for _, pgID := range rest {
		if err := db.fl.Free(pgID); err != nil {
			return fmt.Errorf("kdb: seed freelist: %w", err)
		}
	}

	return db.persistMigration(m)
}

// migrateExistingFreelist migrates an existing linked-list freelist to B+tree.
func (db *DB) migrateExistingFreelist(m *Meta, pio *FilePageIO) error {
	oldFL, err := freelist.New(pio, m.FreelistRoot)
	if err != nil {
		return fmt.Errorf("kdb: load old freelist: %w", err)
	}

	// Collect old freelist pages (the linked-list pages themselves).
	oldPages, err := freelist.CollectFreelistPages(pio, m.FreelistRoot)
	if err != nil {
		return fmt.Errorf("kdb: collect old freelist pages: %w", err)
	}

	// Allocate reserve pages from the old freelist.
	var reserve []uint64
	for range reservePageCount {
		id, err := oldFL.Alloc()
		if err != nil {
			break // not enough pages for full reserve
		}
		reserve = append(reserve, id)
	}

	db.fl = freelist.OpenBTreeFreelist(db.pool, 0, reserve)

	// Migrate remaining entries.
	if err := freelist.Migrate(oldFL, db.fl, oldPages); err != nil {
		return fmt.Errorf("kdb: migrate freelist: %w", err)
	}

	return db.persistMigration(m)
}

// ── Transaction API ──

// View executes fn within a read-only transaction. Multiple Views can run
// concurrently. View acquires the RWMutex read lock.
func (db *DB) View(_ context.Context, fn func(*tx.ReadTx) error) error {
	db.rwmu.RLock()
	defer db.rwmu.RUnlock()

	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return ErrDBClosed
	}
	tree := db.tree
	db.mu.Unlock()

	rtx := tx.NewReadTx(tree)
	defer rtx.Close()

	return fn(rtx)
}

// Update executes fn within a read-write transaction. Only one Update can
// run at a time (exclusive write lock). If fn returns nil, the transaction
// is committed (WAL commit + flush dirty pages + meta swap). If fn returns
// an error, the transaction is rolled back.
func (db *DB) Update(_ context.Context, fn func(*tx.WriteTx) error) error {
	db.rwmu.Lock()
	defer db.rwmu.Unlock()

	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return ErrDBClosed
	}
	tree := db.tree
	w := db.wal
	pool := db.pool
	db.mu.Unlock()

	wtx := tx.NewWriteTx(tree, w, pool)

	if err := fn(wtx); err != nil {
		wtx.Rollback()
		return err
	}

	if err := wtx.Commit(); err != nil {
		wtx.Rollback()
		return fmt.Errorf("kdb: commit: %w", err)
	}

	// Swap meta with new generation.
	return db.commitMeta()
}

// commitMeta writes a new meta page with bumped generation and current tree/freelist roots.
func (db *DB) commitMeta() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	checkCrashHook("db-pre-meta-swap")

	info, err := db.file.Stat()
	if err != nil {
		return fmt.Errorf("kdb: stat for meta: %w", err)
	}

	newMeta := &Meta{
		Version:        MetaVersion,
		Generation:     db.meta.Generation + 1,
		PageCount:      uint64(info.Size() / pageSize), //nolint:gosec // G115: file size is always positive.
		FreelistRoot:   db.fl.Root(),
		FreelistFormat: FreelistBTree,
		TreeRoot:       db.tree.Root(),
	}

	if err := swapMeta(db.file, newMeta); err != nil {
		return fmt.Errorf("kdb: swap meta: %w", err)
	}
	db.meta = newMeta

	checkCrashHook("db-post-meta-swap")

	return nil
}

// replayWAL replays committed WAL records to restore the B+tree state.
func (db *DB) replayWAL() error {
	checkCrashHook("db-pre-wal-replay")

	committed := false

	err := db.wal.Replay(func(rec wal.Record) error {
		switch rec.Type {
		case wal.TypeInsert:
			key, value := tx.DecodeKV(rec.Payload)
			if key == nil {
				return nil
			}
			return db.tree.Put(key, value)
		case wal.TypeDelete:
			if err := db.tree.Delete(rec.Payload); err != nil {
				// Key might not exist if partially replayed; ignore.
				return nil //nolint:nilerr // expected during replay.
			}
		case wal.TypeCommit:
			committed = true
		case wal.TypeCheckpoint:
			// Future: truncate WAL.
		}
		_ = committed // consumed by future checkpoint logic
		return nil
	})
	if err != nil {
		return err
	}

	checkCrashHook("db-post-wal-replay")

	return nil
}

// TestSubsystems returns the internal buffer pool and freelist for use in
// tests that need direct subsystem access (e.g., secondary index tests).
// This method is intended for testing only.
func (db *DB) TestSubsystems() (*bufpool.Pool, *freelist.BTreeFreelist) {
	return db.pool, db.fl
}

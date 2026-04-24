// Package kdb provides the embedded key-value storage engine for pcke.
//
// This file implements the database file layout: Open, Close, and chunk-based
// growth. The .pcke/ directory holds a .kdb data file and a LOCK file.
//
// Phase 0 — Task T3.
package kdb

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/jenaiz/pcke/internal/kdb/lock"
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
	dir    string // path to .pcke directory
	file   *os.File
	flock  *lock.FileLock
	closed bool
}

// Open opens (or creates) a kdb database rooted at path.
//
// It creates <path>/.pcke/ (0700) if needed, acquires an exclusive flock on
// <path>/.pcke/LOCK, and creates/opens the data.kdb file (0600). A new
// database starts with GrowthChunk (16) pages.
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

	return db, nil
}

// Close releases all resources: flushes and closes the data file, then
// releases the exclusive flock. Close is idempotent — calling it on an
// already-closed DB returns nil.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return nil
	}
	db.closed = true

	var firstErr error
	if err := db.file.Sync(); err != nil {
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

	return nil
}

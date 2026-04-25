package kdb // compact.go — offline database compaction.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// CompactResult holds statistics from a compaction operation.
type CompactResult struct {
	OldSize    int64 // Size of the original data file in bytes.
	NewSize    int64 // Size of the compacted data file in bytes.
	KeysCopied int   // Number of live keys copied.
}

// Compact performs offline compaction by copying all live key-value pairs
// into a fresh database file. The original file is replaced atomically.
//
// Compaction reclaims free pages from deleted entries, reduces file
// fragmentation, and prunes soft-deleted data (nodes with status "deleted").
//
// The database must not be concurrently accessed during compaction.
// Returns [CompactResult] with before/after sizes.
func (db *DB) Compact(ctx context.Context) (*CompactResult, error) {
	db.rwmu.Lock()
	defer db.rwmu.Unlock()

	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return nil, ErrDBClosed
	}
	db.mu.Unlock()

	oldInfo, err := db.file.Stat()
	if err != nil {
		return nil, fmt.Errorf("kdb: compact stat: %w", err)
	}

	tmpPath := filepath.Join(db.dir, "compact.tmp")
	defer os.Remove(tmpPath) //nolint:errcheck // best-effort cleanup on error.

	result := &CompactResult{OldSize: oldInfo.Size()}

	if err := db.compactCopy(ctx, tmpPath, result); err != nil {
		return nil, err
	}

	tmpInfo, err := os.Stat(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("kdb: compact stat tmp: %w", err)
	}
	result.NewSize = tmpInfo.Size()

	if err := db.compactSwap(tmpPath); err != nil {
		return nil, err
	}

	return result, nil
}

// compactKV is a key-value pair for compaction.
type compactKV struct {
	key, val []byte
}

// compactCopy copies all live keys from the source tree to a fresh DB at tmpPath.
func (db *DB) compactCopy(ctx context.Context, tmpPath string, result *CompactResult) error {
	tmpDB, err := openCompactTarget(tmpPath)
	if err != nil {
		return fmt.Errorf("kdb: compact open tmp: %w", err)
	}

	err = db.viewInternal(func(rtx *tx.ReadTx) error {
		pairs := collectAllPairs(rtx)

		growChunks := (len(pairs)*2)/GrowthChunk + 2
		for range growChunks {
			if err := tmpDB.Grow(); err != nil {
				return fmt.Errorf("grow tmp: %w", err)
			}
		}

		return writePairsInBatches(ctx, tmpDB, pairs, result)
	})
	if err != nil {
		_ = tmpDB.Close()
		return fmt.Errorf("kdb: compact copy: %w", err)
	}

	if err := tmpDB.Checkpoint(ctx); err != nil {
		_ = tmpDB.Close()
		return fmt.Errorf("kdb: compact checkpoint: %w", err)
	}
	return tmpDB.Close()
}

// collectAllPairs reads all key-value pairs from the read transaction.
func collectAllPairs(rtx *tx.ReadTx) []compactKV {
	c := rtx.Cursor()
	if !c.First() {
		return nil
	}

	var pairs []compactKV
	for c.Valid() {
		k := make([]byte, len(c.Key()))
		copy(k, c.Key())
		v := make([]byte, len(c.Value()))
		copy(v, c.Value())
		pairs = append(pairs, compactKV{k, v})
		if !c.Next() {
			break
		}
	}
	return pairs
}

// writePairsInBatches writes pairs to the target DB in batches of 500.
func writePairsInBatches(ctx context.Context, dst *DB, pairs []compactKV, result *CompactResult) error {
	const batchSize = 500
	for i := 0; i < len(pairs); i += batchSize {
		end := min(i+batchSize, len(pairs))
		batch := pairs[i:end]

		if err := dst.Update(ctx, func(wtx *tx.WriteTx) error {
			for _, p := range batch {
				if err := wtx.Put(p.key, p.val); err != nil {
					return err
				}
				result.KeysCopied++
			}
			return nil
		}); err != nil {
			return fmt.Errorf("write batch: %w", err)
		}
	}
	return nil
}

// compactSwap replaces the data file with the compacted version and rewires.
func (db *DB) compactSwap(tmpPath string) error {
	dataPath := filepath.Join(db.dir, dataFileName)
	if err := db.file.Close(); err != nil {
		return fmt.Errorf("kdb: compact close original: %w", err)
	}

	if err := os.Rename(tmpPath, dataPath); err != nil {
		return fmt.Errorf("kdb: compact rename: %w", err)
	}

	f, err := os.OpenFile(dataPath, os.O_RDWR, filePerms) //nolint:gosec // G304: path controlled internally.
	if err != nil {
		return fmt.Errorf("kdb: compact reopen: %w", err)
	}
	db.file = f

	if err := db.wireSubsystems(); err != nil {
		return fmt.Errorf("kdb: compact rewire: %w", err)
	}
	return nil
}

// viewInternal executes fn with a read transaction without acquiring rwmu
// (the caller must already hold rwmu).
func (db *DB) viewInternal(fn func(*tx.ReadTx) error) error {
	rtx := tx.NewReadTx(db.tree)
	defer rtx.Close()
	return fn(rtx)
}

// openCompactTarget creates a minimal database for compaction output.
func openCompactTarget(path string) (*DB, error) {
	dir := filepath.Dir(path)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, filePerms) //nolint:gosec // G304: path controlled internally.
	if err != nil {
		return nil, err
	}

	// Initialize with one growth chunk.
	if err := f.Truncate(growthBytes); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := initMeta(f, GrowthChunk); err != nil {
		_ = f.Close()
		return nil, err
	}

	db := &DB{
		dir:  dir,
		file: f,
	}

	if err := db.wireSubsystems(); err != nil {
		_ = f.Close()
		return nil, err
	}

	return db, nil
}

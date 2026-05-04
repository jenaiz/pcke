// Package migrate — backfill.go provides lazy and eager backfill for fields
// added via online ALTER operations.
//
// Lazy backfill: when a record is read that lacks a new field, the default
// value is returned by the query layer (JSON decode handles missing fields
// naturally).
//
// Eager backfill: iterates all records in a collection, adds the default
// value for the new field, and re-writes the record. Processing is chunked
// to avoid holding the write lock for extended periods.
//
// Phase 7 — Tasks F7.T2 + F7.T3.
package migrate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/index"
	"github.com/jenaiz/pcke/internal/kdb/tx"
	"github.com/jenaiz/pcke/internal/query"
)

// UpdateDB extends DB with read-write transaction support. Satisfied by *kdb.DB.
type UpdateDB interface {
	DB
	View(ctx context.Context, fn func(*tx.ReadTx) error) error
	Update(ctx context.Context, fn func(*tx.WriteTx) error) error
}

// Backfill eagerly populates a new field across all records in a collection.
// It processes records in batches of batchSize per write transaction.
// Returns the number of records updated and any error.
//
// Backfill is idempotent: records that already have the field are skipped.
func Backfill(ctx context.Context, db UpdateDB, op *AlterOp, batchSize int) (int, error) {
	if op.Type != AddField {
		return 0, fmt.Errorf("%w: backfill only supports AddField", ErrInvalidOp)
	}
	if batchSize <= 0 {
		batchSize = 500
	}

	prefix := query.CollectionPrefix(op.Collection)
	if prefix == "" {
		return 0, fmt.Errorf("alter: no prefix for collection %q", op.Collection)
	}

	defaultVal := op.Default
	if defaultVal == nil {
		defaultVal = zeroValue(op.FieldType)
	}

	totalUpdated := 0
	var cursor []byte // resume cursor for chunked processing

	for {
		if err := ctx.Err(); err != nil {
			return totalUpdated, fmt.Errorf("backfill: cancelled: %w", err)
		}

		keys, err := collectKeys(ctx, db, []byte(prefix), cursor, batchSize)
		if err != nil {
			return totalUpdated, fmt.Errorf("backfill: collect keys: %w", err)
		}

		if len(keys) == 0 {
			break
		}

		updated, err := backfillBatch(ctx, db, keys, op.Field, defaultVal)
		if err != nil {
			return totalUpdated, fmt.Errorf("backfill: batch: %w", err)
		}
		totalUpdated += updated

		// Set cursor to last key for next chunk.
		cursor = keys[len(keys)-1]

		if len(keys) < batchSize {
			break // no more records
		}
	}

	return totalUpdated, nil
}

// kv holds a key-value pair read from the B+tree.
type kv struct {
	key   []byte
	value []byte
}

// BackfillIndex adds index entries for a new field across all records in a
// collection. It processes records in batches per write transaction.
// Returns the number of records indexed and any error.
func BackfillIndex(ctx context.Context, db UpdateDB, idx *index.SecondaryIndex, collection, fieldName string, batchSize int) (int, error) {
	if batchSize <= 0 {
		batchSize = 500
	}

	prefix := query.CollectionPrefix(collection)
	if prefix == "" {
		return 0, fmt.Errorf("alter: no prefix for collection %q", collection)
	}

	totalIndexed := 0
	var cursor []byte

	for {
		if err := ctx.Err(); err != nil {
			return totalIndexed, fmt.Errorf("backfill index: cancelled: %w", err)
		}

		batch, err := collectKVs(ctx, db, []byte(prefix), cursor, batchSize)
		if err != nil {
			return totalIndexed, fmt.Errorf("backfill index: read: %w", err)
		}

		if len(batch) == 0 {
			break
		}

		indexed, err := indexBatch(idx, batch, fieldName)
		if err != nil {
			return totalIndexed, err
		}
		totalIndexed += indexed

		cursor = batch[len(batch)-1].key

		if len(batch) < batchSize {
			break
		}
	}

	return totalIndexed, nil
}

// indexBatch inserts index entries for a batch of records.
func indexBatch(idx *index.SecondaryIndex, batch []kv, fieldName string) (int, error) {
	indexed := 0
	for _, item := range batch {
		var m map[string]any
		if err := json.Unmarshal(item.value, &m); err != nil {
			continue
		}

		val, ok := m[fieldName]
		if !ok || val == nil {
			continue
		}

		indexKey := fmt.Sprintf("%v", val)
		if indexKey == "" {
			continue
		}

		if err := idx.Insert(item.key, [][]byte{[]byte(indexKey)}); err != nil {
			return indexed, fmt.Errorf("backfill index: insert: %w", err)
		}
		indexed++
	}
	return indexed, nil
}

// collectKVs reads up to limit key-value pairs starting after cursor.
func collectKVs(ctx context.Context, db UpdateDB, prefix, cursor []byte, limit int) ([]kv, error) {
	var batch []kv

	if err := db.View(ctx, func(rtx *tx.ReadTx) error {
		c := rtx.Cursor()
		if !seekPastCursor(c, prefix, cursor) {
			return nil
		}

		for c.Valid() && len(batch) < limit {
			k := c.Key()
			if !bytes.HasPrefix(k, prefix) {
				break
			}
			keyCopy := make([]byte, len(k))
			copy(keyCopy, k)
			valCopy := make([]byte, len(c.Value()))
			copy(valCopy, c.Value())
			batch = append(batch, kv{key: keyCopy, value: valCopy})
			if !c.Next() {
				break
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return batch, nil
}

// collectKeys reads up to limit keys from the collection starting after cursor.
func collectKeys(ctx context.Context, db UpdateDB, prefix, cursor []byte, limit int) ([][]byte, error) {
	var keys [][]byte

	if err := db.View(ctx, func(rtx *tx.ReadTx) error {
		c := rtx.Cursor()
		if !seekPastCursor(c, prefix, cursor) {
			return nil
		}

		for c.Valid() && len(keys) < limit {
			k := c.Key()
			if !bytes.HasPrefix(k, prefix) {
				break
			}
			keyCopy := make([]byte, len(k))
			copy(keyCopy, k)
			keys = append(keys, keyCopy)
			if !c.Next() {
				break
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return keys, nil
}

// backfillBatch updates a batch of records to include the new field.
func backfillBatch(ctx context.Context, db UpdateDB, keys [][]byte, field string, defaultVal any) (int, error) {
	updated := 0

	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		for _, key := range keys {
			val, err := wtx.Get(key)
			if err != nil {
				continue // key may have been deleted since collection
			}

			var m map[string]any
			if err := json.Unmarshal(val, &m); err != nil {
				continue // skip unparseable records
			}

			// Skip if field already present.
			if _, exists := m[field]; exists {
				continue
			}

			m[field] = defaultVal

			data, err := json.Marshal(m)
			if err != nil {
				return fmt.Errorf("backfill: marshal: %w", err)
			}

			if err := wtx.Put(key, data); err != nil {
				return fmt.Errorf("backfill: put: %w", err)
			}
			updated++
		}
		return nil
	}); err != nil {
		return 0, err
	}

	return updated, nil
}

// zeroValue returns the zero value for a FieldType.
func zeroValue(ft query.FieldType) any {
	switch ft {
	case query.FieldString:
		return ""
	case query.FieldNumber:
		return float64(0)
	case query.FieldBool:
		return false
	case query.FieldTime:
		return ""
	case query.FieldStringSlice:
		return []string{}
	default:
		return nil
	}
}

// seekPastCursor positions a cursor at the first key after cursor, or at
// the prefix start if cursor is nil. Returns false if no valid position found.
func seekPastCursor(c *btree.Cursor, prefix, cursor []byte) bool {
	if cursor != nil {
		if !c.Seek(cursor) {
			return false
		}
		if bytes.Equal(c.Key(), cursor) {
			return c.Next()
		}
		return true
	}
	return c.Seek(prefix)
}

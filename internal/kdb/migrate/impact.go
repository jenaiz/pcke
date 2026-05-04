// Package migrate — impact.go provides dry-run impact analysis for online
// ALTER operations.
//
// Phase 7 — Task F7.T5.
package migrate

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/jenaiz/pcke/internal/kdb/tx"
	"github.com/jenaiz/pcke/internal/query"
)

// ImpactReport describes the projected impact of an ALTER operation.
type ImpactReport struct {
	AffectedRecords   int
	EstimatedBackfill time.Duration
	IndexRebuildScope []string // Index names needing rebuild
	SchemaVersionFrom uint16
	SchemaVersionTo   uint16
	IsIdempotent      bool // true if ALTER was already applied
}

// TxDB is the interface for databases that support View transactions.
// This is satisfied by *kdb.DB.
type TxDB interface {
	DB
	View(ctx context.Context, fn func(*tx.ReadTx) error) error
}

// AnalyzeImpact estimates the impact of an ALTER operation without mutating
// the database. It counts affected records and identifies indexes that would
// need rebuilding.
func AnalyzeImpact(ctx context.Context, db TxDB, op *AlterOp) (*ImpactReport, error) {
	if err := op.validate(); err != nil {
		return nil, err
	}

	report := &ImpactReport{
		SchemaVersionFrom: db.SchemaVersion(),
		SchemaVersionTo:   db.SchemaVersion() + 1,
	}

	switch op.Type {
	case AddField:
		return analyzeAddField(ctx, db, op, report)
	case AddCollection:
		return analyzeAddCollection(op, report)
	default:
		return nil, fmt.Errorf("%w: unknown alter type %d", ErrInvalidOp, op.Type)
	}
}

// analyzeAddField checks if the field already exists and counts affected records.
func analyzeAddField(ctx context.Context, db TxDB, op *AlterOp, report *ImpactReport) (*ImpactReport, error) {
	schema := query.CollectionSchema(op.Collection)
	if schema == nil {
		return nil, fmt.Errorf("%w: %q", ErrCollectionNotFound, op.Collection)
	}

	// Check idempotency: field already exists with same type.
	if ft, exists := schema[op.Field]; exists {
		if ft == op.FieldType {
			report.IsIdempotent = true
			return report, nil
		}
		return nil, fmt.Errorf("%w: %q already exists as %s", ErrFieldExists, op.Field, ft)
	}

	// Count records in the collection.
	prefix := query.CollectionPrefix(op.Collection)
	if prefix == "" {
		return nil, fmt.Errorf("query: no prefix for collection %q", op.Collection)
	}

	count, err := countRecords(ctx, db, []byte(prefix))
	if err != nil {
		return nil, fmt.Errorf("alter: count records: %w", err)
	}
	report.AffectedRecords = count

	// Estimate backfill time: ~10µs per record (JSON decode + re-encode).
	report.EstimatedBackfill = time.Duration(count) * 10 * time.Microsecond

	// Index rebuild scope.
	if op.Indexed {
		report.IndexRebuildScope = []string{fmt.Sprintf("by_%s", op.Field)}
	}

	return report, nil
}

// analyzeAddCollection reports impact for a new collection (0 affected records).
func analyzeAddCollection(op *AlterOp, report *ImpactReport) (*ImpactReport, error) {
	// Check if collection already exists.
	if existing := query.CollectionSchema(op.Collection); existing != nil {
		if schemasMatch(existing, op.Fields) {
			report.IsIdempotent = true
			return report, nil
		}
		return nil, fmt.Errorf("%w: %q", ErrCollectionExists, op.Collection)
	}

	report.AffectedRecords = 0
	report.EstimatedBackfill = 0
	return report, nil
}

// schemasMatch returns true if two schemas have the same fields and types.
func schemasMatch(a, b query.Schema) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// countRecords counts all records with the given key prefix via cursor scan.
func countRecords(ctx context.Context, db TxDB, prefix []byte) (int, error) {
	var count int

	if err := db.View(ctx, func(rtx *tx.ReadTx) error {
		c := rtx.Cursor()
		if !c.Seek(prefix) {
			return nil
		}

		for c.Valid() {
			if !bytes.HasPrefix(c.Key(), prefix) {
				break
			}
			count++
			if !c.Next() {
				break
			}
		}
		return nil
	}); err != nil {
		return 0, err
	}

	return count, nil
}

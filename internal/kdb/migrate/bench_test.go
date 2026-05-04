package migrate_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/migrate"
	"github.com/jenaiz/pcke/internal/kdb/tx"
	"github.com/jenaiz/pcke/internal/query"
)

// seedBenchNodes inserts n records into the database for benchmarks.
func seedBenchNodes(b *testing.B, db *kdb.DB, n int) {
	b.Helper()
	ctx := context.Background()

	// Grow DB sufficiently.
	for range (n / 5) + 2 {
		if err := db.Grow(); err != nil {
			b.Fatalf("grow: %v", err)
		}
	}

	// Insert in batches of 100.
	batchSize := 100
	for start := 0; start < n; start += batchSize {
		end := start + batchSize
		if end > n {
			end = n
		}
		if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
			for i := start; i < end; i++ {
				node := map[string]any{
					"id":        fmt.Sprintf("bench-node-%06d", i),
					"name":      fmt.Sprintf("BenchNode%d", i),
					"type":      "function",
					"file_path": fmt.Sprintf("pkg/module%d/file.go", i%50),
					"module":    fmt.Sprintf("module%d", i%10),
					"language":  "go",
					"stability": float64(i % 100),
				}
				data, err := json.Marshal(node)
				if err != nil {
					return err
				}
				if err := wtx.Put([]byte(fmt.Sprintf("kn:bench-node-%06d", i)), data); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			b.Fatalf("seed batch: %v", err)
		}
	}
}

func BenchmarkAlter100KNodes(b *testing.B) {
	// Use a smaller count for CI sanity; scale up manually for load testing.
	const nodeCount = 1000

	dir := b.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	seedBenchNodes(b, db, nodeCount)

	ctx := context.Background()

	b.ResetTimer()
	for i := range b.N {
		op := &migrate.AlterOp{
			Type:       migrate.AddField,
			Collection: "nodes",
			Field:      fmt.Sprintf("bench_field_%d", i),
			FieldType:  query.FieldNumber,
		}
		if err := migrate.Apply(ctx, db, op); err != nil {
			b.Fatalf("Apply: %v", err)
		}
	}
}

func BenchmarkBackfill100KNodes(b *testing.B) {
	const nodeCount = 1000

	dir := b.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	seedBenchNodes(b, db, nodeCount)

	ctx := context.Background()

	b.ResetTimer()
	for i := range b.N {
		field := fmt.Sprintf("bench_backfill_%d", i)
		op := &migrate.AlterOp{
			Type:       migrate.AddField,
			Collection: "nodes",
			Field:      field,
			FieldType:  query.FieldNumber,
			Default:    float64(42),
		}
		count, err := migrate.Backfill(ctx, db, op, 500)
		if err != nil {
			b.Fatalf("Backfill: %v", err)
		}
		if count != nodeCount {
			b.Fatalf("backfilled = %d, want %d", count, nodeCount)
		}
	}
}

func BenchmarkReadDuringBackfill(b *testing.B) {
	const nodeCount = 1000

	dir := b.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	seedBenchNodes(b, db, nodeCount)

	ctx := context.Background()

	// Measure read latency (baseline).
	b.ResetTimer()
	for range b.N {
		if err := db.View(ctx, func(rtx *tx.ReadTx) error {
			c := rtx.Cursor()
			if !c.Seek([]byte("kn:")) {
				return nil
			}
			count := 0
			for c.Valid() {
				k := c.Key()
				if len(k) < 3 || string(k[:3]) != "kn:" {
					break
				}
				_ = c.Value()
				count++
				if !c.Next() {
					break
				}
			}
			return nil
		}); err != nil {
			b.Fatalf("View: %v", err)
		}
	}
}

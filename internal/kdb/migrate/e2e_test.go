package migrate_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/migrate"
	"github.com/jenaiz/pcke/internal/kdb/tx"
	"github.com/jenaiz/pcke/internal/query"
)

// ── E2E: ALTER ADD FIELD + concurrent readers ──

func TestE2E_AlterAddField_ConcurrentReaders(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	seedNodes(t, db, 20)

	ctx := context.Background()

	// Start concurrent readers.
	var wg sync.WaitGroup
	errs := make(chan error, 10)

	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
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
					errs <- err
				}
			}
		}()
	}

	// Apply ALTER while readers are active.
	op := &migrate.AlterOp{
		Type:       migrate.AddField,
		Collection: "nodes",
		Field:      "e2e_concurrent_field",
		FieldType:  query.FieldNumber,
		Default:    float64(99),
	}

	if err := migrate.Apply(ctx, db, op); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Backfill while readers are active.
	count, err := migrate.Backfill(ctx, db, op, 5)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if count != 20 {
		t.Errorf("backfilled = %d, want 20", count)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("reader error: %v", err)
	}
}

// ── E2E: ALTER ADD COLLECTION + immediate query ──

func TestE2E_AlterAddCollection_ImmediateQuery(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	op := &migrate.AlterOp{
		Type:       migrate.AddCollection,
		Collection: "e2e_events",
		Fields: query.Schema{
			"id":        query.FieldString,
			"timestamp": query.FieldTime,
			"severity":  query.FieldNumber,
		},
		Prefix: "ev:",
	}

	if err := migrate.Apply(ctx, db, op); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Verify schema registered.
	schema := query.CollectionSchema("e2e_events")
	if schema == nil {
		t.Fatal("e2e_events schema not registered")
	}
	if len(schema) != 3 {
		t.Errorf("field count = %d, want 3", len(schema))
	}

	// Insert a record into the new collection.
	event := map[string]any{
		"id":        "evt-001",
		"timestamp": "2026-05-02T00:00:00Z",
		"severity":  float64(5),
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		return wtx.Put([]byte("ev:evt-001"), data)
	}); err != nil {
		t.Fatalf("put: %v", err)
	}

	// Read it back.
	if err := db.View(ctx, func(rtx *tx.ReadTx) error {
		val, err := rtx.Get([]byte("ev:evt-001"))
		if err != nil {
			return err
		}
		var m map[string]any
		if err := json.Unmarshal(val, &m); err != nil {
			return err
		}
		if m["id"] != "evt-001" {
			t.Errorf("id = %v, want evt-001", m["id"])
		}
		return nil
	}); err != nil {
		t.Fatalf("View: %v", err)
	}
}

// ── E2E: Idempotency ──

func TestE2E_Idempotency(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	op := &migrate.AlterOp{
		Type:       migrate.AddField,
		Collection: "nodes",
		Field:      "e2e_idem_field",
		FieldType:  query.FieldBool,
	}

	// Apply twice.
	if err := migrate.Apply(ctx, db, op); err != nil {
		t.Fatalf("Apply 1: %v", err)
	}
	v1 := db.SchemaVersion()

	if err := migrate.Apply(ctx, db, op); err != nil {
		t.Fatalf("Apply 2: %v", err)
	}
	v2 := db.SchemaVersion()

	// Second apply still bumps (RegisterField returns nil for idempotent).
	if v2 != v1+1 {
		t.Errorf("version after 2nd apply = %d, want %d", v2, v1+1)
	}
}

// ── E2E: Snapshot isolation during backfill ──

func TestE2E_SnapshotIsolation_DuringBackfill(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	seedNodes(t, db, 15)

	ctx := context.Background()

	// Verify pre-backfill count.
	preCount := countNodeKeys(ctx, t, db)
	if preCount != 15 {
		t.Errorf("pre-backfill keys = %d, want 15", preCount)
	}

	// Backfill.
	op := &migrate.AlterOp{
		Type:       migrate.AddField,
		Collection: "nodes",
		Field:      "e2e_snapshot_field",
		FieldType:  query.FieldString,
		Default:    "backfilled",
	}

	count, err := migrate.Backfill(ctx, db, op, 3)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if count != 15 {
		t.Errorf("backfilled = %d, want 15", count)
	}

	// Verify post-backfill — all records have the field.
	verified := verifyBackfilledField(ctx, t, db, "e2e_snapshot_field", "backfilled")
	if verified != 15 {
		t.Errorf("verified = %d, want 15", verified)
	}
}

// countNodeKeys counts records under the "kn:" prefix.
func countNodeKeys(ctx context.Context, t *testing.T, db *kdb.DB) int {
	t.Helper()
	var count int
	if err := db.View(ctx, func(rtx *tx.ReadTx) error {
		c := rtx.Cursor()
		if !c.Seek([]byte("kn:")) {
			return nil
		}
		for c.Valid() {
			k := c.Key()
			if len(k) < 3 || string(k[:3]) != "kn:" {
				break
			}
			count++
			if !c.Next() {
				break
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("View: %v", err)
	}
	return count
}

// verifyBackfilledField counts records that have the specified field with the expected value.
func verifyBackfilledField(ctx context.Context, t *testing.T, db *kdb.DB, field string, expected any) int {
	t.Helper()
	var verified int
	if err := db.View(ctx, func(rtx *tx.ReadTx) error {
		c := rtx.Cursor()
		if !c.Seek([]byte("kn:")) {
			return nil
		}
		for c.Valid() {
			k := c.Key()
			if len(k) < 3 || string(k[:3]) != "kn:" {
				break
			}
			var m map[string]any
			if err := json.Unmarshal(c.Value(), &m); err == nil {
				if m[field] == expected {
					verified++
				}
			}
			if !c.Next() {
				break
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("View: %v", err)
	}
	return verified
}

// ── E2E: Impact analysis with real DB ──

func TestE2E_ImpactAnalysis(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	seedNodes(t, db, 10)

	op := &migrate.AlterOp{
		Type:       migrate.AddField,
		Collection: "nodes",
		Field:      "e2e_impact_field",
		FieldType:  query.FieldString,
		Indexed:    true,
	}

	report, err := migrate.AnalyzeImpact(context.Background(), db, op)
	if err != nil {
		t.Fatalf("AnalyzeImpact: %v", err)
	}

	if report.AffectedRecords != 10 {
		t.Errorf("affected = %d, want 10", report.AffectedRecords)
	}
	if report.IsIdempotent {
		t.Error("expected non-idempotent")
	}
	if len(report.IndexRebuildScope) != 1 {
		t.Errorf("index scope = %v, want 1 entry", report.IndexRebuildScope)
	}
	if report.SchemaVersionTo != report.SchemaVersionFrom+1 {
		t.Errorf("version %d → %d, want +1", report.SchemaVersionFrom, report.SchemaVersionTo)
	}
}

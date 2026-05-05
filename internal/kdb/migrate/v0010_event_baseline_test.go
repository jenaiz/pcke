package migrate_test

import (
	"context"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/migrate"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// seedLegacyRecords writes a few representative kn:/rel:/nt:/el: records
// to simulate a v0.9.x database. The migration must leave them untouched.
func seedLegacyRecords(t *testing.T, db *kdb.DB) map[string][]byte {
	t.Helper()
	records := map[string][]byte{
		"kn:internal/kdb/db.go":                         []byte(`{"type":"file"}`),
		"rel:internal/kdb/db.go->internal/kdb/btree.go": []byte(`{"type":"imports"}`),
		"nt:adr-0008":                  []byte(`{"title":"Pivot"}`),
		"el:internal/kdb/db.go:abc123": []byte(`{"action":"rename"}`),
	}
	if err := db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		for k, v := range records {
			if err := wtx.Put([]byte(k), v); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return records
}

func TestV0010EventBaseline_BumpsVersionWithoutTouchingData(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if got := db.SchemaVersion(); got != 0 {
		t.Fatalf("fresh DB schema version = %d, want 0", got)
	}
	expected := seedLegacyRecords(t, db)

	engine := migrate.New()
	engine.Register(migrate.V0010EventBaseline())

	applied, err := engine.Run(context.Background(), db)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if applied != 1 {
		t.Errorf("applied = %d, want 1", applied)
	}
	if got := db.SchemaVersion(); got != 10 {
		t.Errorf("after migrate, schema version = %d, want 10", got)
	}

	// Legacy records must round-trip byte-for-byte.
	if err := db.View(context.Background(), func(rtx *tx.ReadTx) error {
		for k, want := range expected {
			got, err := rtx.Get([]byte(k))
			if err != nil {
				return err
			}
			if string(got) != string(want) {
				t.Errorf("record %q changed: got %q, want %q", k, got, want)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("View: %v", err)
	}
}

func TestV0010EventBaseline_IsIdempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	engine := migrate.New()
	engine.Register(migrate.V0010EventBaseline())

	if applied, err := engine.Run(context.Background(), db); err != nil || applied != 1 {
		t.Fatalf("first run: applied=%d err=%v, want applied=1 err=nil", applied, err)
	}
	if applied, err := engine.Run(context.Background(), db); err != nil || applied != 0 {
		t.Errorf("second run: applied=%d err=%v, want applied=0 err=nil (no-op)", applied, err)
	}
	if got := db.SchemaVersion(); got != 10 {
		t.Errorf("after idempotent re-run, schema version = %d, want 10", got)
	}
}

func TestV0010EventBaseline_HandlesPreExistingHigherVersion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Simulate a DB that already passed through a hypothetical migration
	// to version 5; the baseline at 10 should still be applied.
	db.SetSchemaVersion(5)

	engine := migrate.New()
	engine.Register(migrate.V0010EventBaseline())
	applied, err := engine.Run(context.Background(), db)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if applied != 1 {
		t.Errorf("applied = %d, want 1 (baseline still pending after preset v5)", applied)
	}
	if got := db.SchemaVersion(); got != 10 {
		t.Errorf("schema version = %d, want 10", got)
	}
}

func TestV0010EventBaseline_SurvivesReopen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}

	engine := migrate.New()
	engine.Register(migrate.V0010EventBaseline())
	if _, err := engine.Run(context.Background(), db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Force the meta to persist by performing an Update, then Close.
	if err := db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		return wtx.Put([]byte("flush-marker"), []byte("v"))
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen and confirm the version is durable.
	db2, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open #2: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	if got := db2.SchemaVersion(); got != 10 {
		t.Errorf("after reopen, schema version = %d, want 10", got)
	}
}

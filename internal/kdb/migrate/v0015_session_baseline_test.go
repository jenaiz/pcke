package migrate_test

import (
	"context"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/migrate"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

func TestV0015SessionBaseline_BumpsVersionToFifteen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Simulate a database that has already passed through every prior
	// migration up to v14 — the realistic starting state when this
	// migration runs.
	db.SetSchemaVersion(14)

	engine := migrate.New()
	engine.Register(migrate.V0015SessionBaseline())

	applied, err := engine.Run(context.Background(), db)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if applied != 1 {
		t.Errorf("applied = %d, want 1", applied)
	}
	if got := db.SchemaVersion(); got != 15 {
		t.Errorf("after migrate, schema version = %d, want 15", got)
	}
}

func TestV0015SessionBaseline_IsIdempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	db.SetSchemaVersion(14)

	engine := migrate.New()
	engine.Register(migrate.V0015SessionBaseline())

	if applied, err := engine.Run(context.Background(), db); err != nil || applied != 1 {
		t.Fatalf("first run: applied=%d err=%v, want applied=1 err=nil", applied, err)
	}
	if applied, err := engine.Run(context.Background(), db); err != nil || applied != 0 {
		t.Errorf("second run: applied=%d err=%v, want applied=0 err=nil (no-op)", applied, err)
	}
	if got := db.SchemaVersion(); got != 15 {
		t.Errorf("after idempotent re-run, schema version = %d, want 15", got)
	}
}

func TestV0015SessionBaseline_LeavesExistingRecordsUntouched(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Pre-existing typed-event records (from Phase 12+) must survive the
	// no-op bump byte-for-byte. The migration only touches the meta page.
	records := map[string][]byte{
		"e:internal/kdb/db.go:v0000000000000001": []byte("entity-blob"),
		"d:adr-0008:v0000000000000001":           []byte("decision-blob"),
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

	db.SetSchemaVersion(14)
	engine := migrate.New()
	engine.Register(migrate.V0015SessionBaseline())
	if _, err := engine.Run(context.Background(), db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if err := db.View(context.Background(), func(rtx *tx.ReadTx) error {
		for k, want := range records {
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

package migrate_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/migrate"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// legacyKn matches the JSON shape the scanner writes today. Tests
// construct these and stuff them into the DB to simulate a v0.9.x
// snapshot.
type legacyKn struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Name      string    `json:"name"`
	FilePath  string    `json:"file_path"`
	Module    string    `json:"module"` // ignored by 0011 — proves field-drop is intentional
	CreatedAt time.Time `json:"created_at"`
}

func openMigrationDB(t *testing.T) *kdb.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for range 10 {
		if err := db.Grow(); err != nil {
			t.Fatalf("db.Grow: %v", err)
		}
	}
	return db
}

func seedLegacyKn(t *testing.T, db *kdb.DB, items []legacyKn) {
	t.Helper()
	if err := db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		for _, it := range items {
			data, err := json.Marshal(it)
			if err != nil {
				return err
			}
			if err := wtx.Put([]byte("kn:"+it.ID), data); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed kn: %v", err)
	}
}

func runEngineTo11(t *testing.T, db *kdb.DB) {
	t.Helper()
	e := migrate.New()
	e.Register(migrate.V0010EventBaseline())
	e.Register(migrate.V0011MigrateKnToE())
	if _, err := e.Run(context.Background(), db); err != nil {
		t.Fatalf("engine.Run: %v", err)
	}
}

func TestV0011_TranslatesAllKnRecords(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	t1 := time.Unix(1_700_000_000, 0).UTC()
	items := []legacyKn{
		{ID: "internal/kdb/db.go", Type: "file", Name: "db.go", FilePath: "internal/kdb/db.go", Module: "kdb", CreatedAt: t1},
		{ID: "internal/kdb/btree/btree.go", Type: "file", Name: "btree.go", FilePath: "internal/kdb/btree/btree.go", Module: "btree", CreatedAt: t1.Add(time.Hour)},
		{ID: "cmd/pcke/main", Type: "function", Name: "main", FilePath: "cmd/pcke/root.go", CreatedAt: t1.Add(2 * time.Hour)},
	}
	seedLegacyKn(t, db, items)

	runEngineTo11(t, db)

	store := event.New(db)
	for _, want := range items {
		got, err := store.Latest(context.Background(), event.KindEntity, want.ID)
		if err != nil {
			t.Fatalf("Latest(%q): %v", want.ID, err)
		}
		ent, ok := got.(*event.Entity)
		if !ok {
			t.Fatalf("Latest(%q) returned %T, want *Entity", want.ID, got)
		}
		if ent.EID != want.ID {
			t.Errorf("EID = %q, want %q", ent.EID, want.ID)
		}
		if ent.Type != want.Type {
			t.Errorf("Type = %q, want %q", ent.Type, want.Type)
		}
		if ent.Path != want.FilePath {
			t.Errorf("Path = %q, want %q", ent.Path, want.FilePath)
		}
		if ent.Name != want.Name {
			t.Errorf("Name = %q, want %q", ent.Name, want.Name)
		}
		if !ent.Header().CreatedAt.Equal(want.CreatedAt) {
			t.Errorf("CreatedAt = %v, want %v", ent.Header().CreatedAt, want.CreatedAt)
		}
		if ent.Header().Lifecycle != event.LifecycleActive {
			t.Errorf("Lifecycle = %d, want LifecycleActive", ent.Header().Lifecycle)
		}
		if ent.Header().Version != 1 {
			t.Errorf("Version = %d, want 1 (translated records start at v1)", ent.Header().Version)
		}
	}
}

func TestV0011_PreservesLegacyRecords(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	items := []legacyKn{
		{ID: "x", Type: "file", FilePath: "x.go"},
	}
	seedLegacyKn(t, db, items)
	runEngineTo11(t, db)

	if err := db.View(context.Background(), func(rtx *tx.ReadTx) error {
		val, err := rtx.Get([]byte("kn:x"))
		if err != nil {
			return err
		}
		var node legacyKn
		if err := json.Unmarshal(val, &node); err != nil {
			return err
		}
		if node.ID != "x" {
			t.Errorf("legacy kn:x lost: got %+v", node)
		}
		return nil
	}); err != nil {
		t.Fatalf("View: %v", err)
	}
}

func TestV0011_IsIdempotent(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	items := []legacyKn{
		{ID: "a", Type: "file", FilePath: "a.go"},
		{ID: "b", Type: "file", FilePath: "b.go"},
	}
	seedLegacyKn(t, db, items)

	// First run: applies 0010 + 0011.
	runEngineTo11(t, db)
	if got := db.SchemaVersion(); got != 11 {
		t.Fatalf("after run 1, version = %d, want 11", got)
	}

	// Second run: no-op (engine sees current version 11 == latest).
	e := migrate.New()
	e.Register(migrate.V0010EventBaseline())
	e.Register(migrate.V0011MigrateKnToE())
	applied, err := e.Run(context.Background(), db)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if applied != 0 {
		t.Errorf("run 2 applied = %d, want 0 (engine-level idempotency)", applied)
	}

	// Each entity should still be at v1 — never bumped.
	store := event.New(db)
	for _, it := range items {
		got, err := store.Latest(context.Background(), event.KindEntity, it.ID)
		if err != nil {
			t.Fatalf("Latest(%q): %v", it.ID, err)
		}
		if got.Header().Version != 1 {
			t.Errorf("entity %q version = %d, want 1", it.ID, got.Header().Version)
		}
	}
}

func TestV0011_PartialRunResumesWithoutDuplicates(t *testing.T) {
	t.Parallel()
	// Simulate "0010 already applied; e:a:v1 already exists from a partial
	// 0011 run that crashed before processing 'b'". The migration must
	// translate 'b' but not write a duplicate v2 for 'a'.
	db := openMigrationDB(t)
	seedLegacyKn(t, db, []legacyKn{
		{ID: "a", Type: "file", FilePath: "a.go"},
		{ID: "b", Type: "file", FilePath: "b.go"},
	})

	// Bring DB to version 10 + manually create e:a:v1 to mimic the partial state.
	preEngine := migrate.New()
	preEngine.Register(migrate.V0010EventBaseline())
	if _, err := preEngine.Run(context.Background(), db); err != nil {
		t.Fatalf("pre-run 0010: %v", err)
	}

	store := event.New(db)
	if _, err := store.Append(context.Background(), &event.Entity{
		EID: "a", Type: "file", Path: "a.go",
	}); err != nil {
		t.Fatalf("seed e:a:v1: %v", err)
	}

	// Now run 0011 — it should see e:a:v1 exists, skip it, and translate b.
	postEngine := migrate.New()
	postEngine.Register(migrate.V0010EventBaseline())
	postEngine.Register(migrate.V0011MigrateKnToE())
	if _, err := postEngine.Run(context.Background(), db); err != nil {
		t.Fatalf("post-run: %v", err)
	}

	// Check 'a' is still at v1.
	gotA, err := store.Latest(context.Background(), event.KindEntity, "a")
	if err != nil {
		t.Fatalf("Latest a: %v", err)
	}
	if gotA.Header().Version != 1 {
		t.Errorf("a version = %d, want 1 (no duplicate write)", gotA.Header().Version)
	}

	// Check 'b' got translated.
	gotB, err := store.Latest(context.Background(), event.KindEntity, "b")
	if err != nil {
		t.Fatalf("Latest b: %v", err)
	}
	if gotB.Header().Version != 1 {
		t.Errorf("b version = %d, want 1", gotB.Header().Version)
	}
}

func TestV0011_HandlesEmptyDB(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)
	runEngineTo11(t, db)
	if got := db.SchemaVersion(); got != 11 {
		t.Errorf("version = %d, want 11", got)
	}
}

func TestV0011_SkipsRecordsWithEmptyID(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	// A malformed legacy record with empty id must not block the migration.
	if err := db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		bad, _ := json.Marshal(legacyKn{Type: "file", FilePath: "lost.go"}) // ID empty
		good, _ := json.Marshal(legacyKn{ID: "ok", Type: "file", FilePath: "ok.go"})
		if err := wtx.Put([]byte("kn:lost"), bad); err != nil {
			return err
		}
		return wtx.Put([]byte("kn:ok"), good)
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	runEngineTo11(t, db)

	store := event.New(db)
	if _, err := store.Latest(context.Background(), event.KindEntity, "ok"); err != nil {
		t.Errorf("Latest(ok): %v", err)
	}
	if _, err := store.Latest(context.Background(), event.KindEntity, "lost"); !errors.Is(err, event.ErrNotFound) {
		t.Errorf("Latest(lost) got %v, want ErrNotFound", err)
	}
}

func TestV0011_ErrorOnMalformedJSON(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)
	if err := db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		return wtx.Put([]byte("kn:bad"), []byte("not-json"))
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	e := migrate.New()
	e.Register(migrate.V0010EventBaseline())
	e.Register(migrate.V0011MigrateKnToE())
	_, err := e.Run(context.Background(), db)
	if err == nil {
		t.Fatal("expected JSON decode error, got nil")
	}
	if got := db.SchemaVersion(); got != 10 {
		t.Errorf("schema version = %d, want 10 (failed migration must not bump version)", got)
	}
}

// _ keeps fmt import used in case future tests need it.
var _ = fmt.Errorf

package main

import (
	"context"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
)

func TestApplyPendingMigrations_StampsAndPersists(t *testing.T) {
	dir := t.TempDir()

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	if v := db.SchemaVersion(); v != 0 {
		t.Fatalf("fresh SchemaVersion = %d, want 0", v)
	}

	applied, err := applyPendingMigrations(context.Background(), db)
	if err != nil {
		t.Fatalf("applyPendingMigrations: %v", err)
	}
	if applied == 0 {
		t.Fatalf("applied = 0, want the pending migrations to run on a v0 database")
	}
	stamped := db.SchemaVersion()
	if stamped == 0 {
		t.Fatalf("SchemaVersion still 0 after migrating")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	// Reopen: the schema version must have persisted even though the
	// migrations wrote no records on an empty database.
	db2, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open #2: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	if v := db2.SchemaVersion(); v != stamped {
		t.Fatalf("reopened SchemaVersion = %d, want persisted %d", v, stamped)
	}
	applied2, err := applyPendingMigrations(context.Background(), db2)
	if err != nil {
		t.Fatalf("applyPendingMigrations #2: %v", err)
	}
	if applied2 != 0 {
		t.Fatalf("second run applied = %d, want 0 (idempotent)", applied2)
	}
}

func TestEventLogEmpty(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for range 4 {
		if err := db.Grow(); err != nil {
			t.Fatalf("db.Grow: %v", err)
		}
	}

	if !eventLogEmpty(context.Background(), db) {
		t.Fatalf("eventLogEmpty = false on a fresh database, want true")
	}

	store := event.New(db)
	if _, err := store.Append(context.Background(), &event.Entity{
		EID:  "a.go",
		Path: "a.go",
		Type: "file",
	}); err != nil {
		t.Fatalf("append entity: %v", err)
	}

	if eventLogEmpty(context.Background(), db) {
		t.Fatalf("eventLogEmpty = true after appending an entity, want false")
	}
}

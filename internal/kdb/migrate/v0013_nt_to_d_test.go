package migrate_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/migrate"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

type legacyNote struct {
	ID        string   `json:"id"`
	Content   string   `json:"content"`
	Tags      []string `json:"tags"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

func seedLegacyNote(t *testing.T, db *kdb.DB, items []legacyNote) {
	t.Helper()
	if err := db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		for _, it := range items {
			data, err := json.Marshal(it)
			if err != nil {
				return err
			}
			if err := wtx.Put([]byte("nt:"+it.ID), data); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed nt: %v", err)
	}
}

func runEngineTo13(t *testing.T, db *kdb.DB) {
	t.Helper()
	e := migrate.New()
	e.Register(migrate.V0010EventBaseline())
	e.Register(migrate.V0011MigrateKnToE())
	e.Register(migrate.V0012MigrateRelToL())
	e.Register(migrate.V0013MigrateNtToD())
	if _, err := e.Run(context.Background(), db); err != nil {
		t.Fatalf("engine.Run: %v", err)
	}
}

func TestV0013_TranslatesNotes(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	t1 := time.Unix(1_700_000_000, 0).UTC().Format(time.RFC3339)
	notes := []legacyNote{
		{ID: "uuid-1", Content: "Always validate input at boundaries.", Tags: []string{"security"}, CreatedAt: t1},
		{ID: "uuid-2", Content: "Multi-line note.\nSecond line has detail.\nThird line wraps up.", CreatedAt: t1},
	}
	seedLegacyNote(t, db, notes)
	runEngineTo13(t, db)

	store := event.New(db)
	for _, n := range notes {
		got, err := store.Latest(context.Background(), event.KindDecision, n.ID)
		if err != nil {
			t.Fatalf("Latest(%q): %v", n.ID, err)
		}
		dec, ok := got.(*event.Decision)
		if !ok {
			t.Fatalf("Latest returned %T, want *Decision", got)
		}
		if dec.DID != n.ID {
			t.Errorf("DID = %q, want %q", dec.DID, n.ID)
		}
		if dec.Body != n.Content {
			t.Errorf("Body = %q, want %q", dec.Body, n.Content)
		}
		if dec.Severity != event.SeverityShould {
			t.Errorf("Severity = %d, want SeverityShould", dec.Severity)
		}
		if dec.Scope != event.ScopeGlobal {
			t.Errorf("Scope = %d, want ScopeGlobal", dec.Scope)
		}
		if dec.Source != "manual" {
			t.Errorf("Source = %q, want %q", dec.Source, "manual")
		}
	}
}

func TestV0013_ExtractsFirstLineAsTitle(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	t1 := time.Unix(1_700_000_000, 0).UTC().Format(time.RFC3339)
	cases := []struct {
		id      string
		content string
		want    string
	}{
		{"single", "Just one line", "Just one line"},
		{"multi", "First line\nSecond line\nThird line", "First line"},
		{"leading-blank", "\n\nReal first line\nMore text", "Real first line"},
		{"empty", "", "(untitled)"},
		{"whitespace-only", "   \n\t\n", "(untitled)"},
	}
	notes := make([]legacyNote, 0, len(cases))
	for _, tc := range cases {
		notes = append(notes, legacyNote{ID: tc.id, Content: tc.content, CreatedAt: t1})
	}
	seedLegacyNote(t, db, notes)
	runEngineTo13(t, db)

	store := event.New(db)
	for _, tc := range cases {
		got, err := store.Latest(context.Background(), event.KindDecision, tc.id)
		if err != nil {
			t.Fatalf("Latest(%q): %v", tc.id, err)
		}
		dec := got.(*event.Decision)
		if dec.Title != tc.want {
			t.Errorf("Title for %q = %q, want %q", tc.id, dec.Title, tc.want)
		}
	}
}

func TestV0013_TruncatesLongTitle(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	long := make([]rune, 300)
	for i := range long {
		long[i] = 'a'
	}
	t1 := time.Unix(1_700_000_000, 0).UTC().Format(time.RFC3339)
	seedLegacyNote(t, db, []legacyNote{{ID: "long", Content: string(long), CreatedAt: t1}})
	runEngineTo13(t, db)

	store := event.New(db)
	got, err := store.Latest(context.Background(), event.KindDecision, "long")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	dec := got.(*event.Decision)
	if r := []rune(dec.Title); len(r) != 200 {
		t.Errorf("Title length = %d runes, want 200", len(r))
	}
	// Body keeps the full content.
	if r := []rune(dec.Body); len(r) != 300 {
		t.Errorf("Body length = %d runes, want 300", len(r))
	}
}

func TestV0013_PreservesCreatedAt(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	when := time.Unix(1_700_000_000, 0).UTC()
	seedLegacyNote(t, db, []legacyNote{
		{ID: "n", Content: "x", CreatedAt: when.Format(time.RFC3339)},
	})
	runEngineTo13(t, db)

	store := event.New(db)
	got, err := store.Latest(context.Background(), event.KindDecision, "n")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if !got.Header().CreatedAt.Equal(when) {
		t.Errorf("CreatedAt = %v, want %v", got.Header().CreatedAt, when)
	}
}

func TestV0013_HandlesUnparseableTimestamp(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	before := time.Now().Add(-time.Second)
	seedLegacyNote(t, db, []legacyNote{
		{ID: "bad-time", Content: "x", CreatedAt: "not a date"},
	})
	runEngineTo13(t, db)

	// Unparseable legacy timestamp -> migration constructs the Decision
	// with a zero CreatedAt; Store.AppendInTx then stamps it with the
	// current time (its documented zero-value default). The translation
	// still succeeds; we only lose the (already-bad) original timestamp.
	store := event.New(db)
	got, err := store.Latest(context.Background(), event.KindDecision, "bad-time")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	at := got.Header().CreatedAt
	if at.Before(before) {
		t.Errorf("CreatedAt = %v, want >= %v (current time fallback)", at, before)
	}
	if at.After(time.Now().Add(time.Second)) {
		t.Errorf("CreatedAt = %v, drifted into the future", at)
	}
}

func TestV0013_PreservesLegacyRecords(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	t1 := time.Unix(1_700_000_000, 0).UTC().Format(time.RFC3339)
	seedLegacyNote(t, db, []legacyNote{{ID: "x", Content: "kept", CreatedAt: t1}})
	runEngineTo13(t, db)

	if err := db.View(context.Background(), func(rtx *tx.ReadTx) error {
		val, err := rtx.Get([]byte("nt:x"))
		if err != nil {
			return err
		}
		var note legacyNote
		if err := json.Unmarshal(val, &note); err != nil {
			return err
		}
		if note.Content != "kept" {
			t.Errorf("nt:x lost: got %+v", note)
		}
		return nil
	}); err != nil {
		t.Fatalf("View: %v", err)
	}
}

func TestV0013_IsIdempotent(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	t1 := time.Unix(1_700_000_000, 0).UTC().Format(time.RFC3339)
	seedLegacyNote(t, db, []legacyNote{{ID: "x", Content: "v1", CreatedAt: t1}})
	runEngineTo13(t, db)

	e := migrate.New()
	e.Register(migrate.V0010EventBaseline())
	e.Register(migrate.V0011MigrateKnToE())
	e.Register(migrate.V0012MigrateRelToL())
	e.Register(migrate.V0013MigrateNtToD())
	applied, err := e.Run(context.Background(), db)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if applied != 0 {
		t.Errorf("re-run applied = %d, want 0", applied)
	}

	store := event.New(db)
	got, err := store.Latest(context.Background(), event.KindDecision, "x")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got.Header().Version != 1 {
		t.Errorf("version = %d, want 1", got.Header().Version)
	}
}

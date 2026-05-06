package migrate_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/graph"
	"github.com/jenaiz/pcke/internal/kdb/migrate"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

type legacyRel struct {
	ID           string    `json:"id"`
	SourceNodeID string    `json:"source_node_id"`
	TargetNodeID string    `json:"target_node_id"`
	Type         string    `json:"type"`
	Source       string    `json:"source"`
	CreatedAt    time.Time `json:"created_at"`
}

func seedLegacyRel(t *testing.T, db *kdb.DB, items []legacyRel) {
	t.Helper()
	if err := db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		for _, it := range items {
			data, err := json.Marshal(it)
			if err != nil {
				return err
			}
			if err := wtx.Put([]byte("rel:"+it.ID), data); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed rel: %v", err)
	}
}

func runEngineTo12(t *testing.T, db *kdb.DB) {
	t.Helper()
	e := migrate.New()
	e.Register(migrate.V0010EventBaseline())
	e.Register(migrate.V0011MigrateKnToE())
	e.Register(migrate.V0012MigrateRelToL())
	if _, err := e.Run(context.Background(), db); err != nil {
		t.Fatalf("engine.Run: %v", err)
	}
}

func TestV0012_TranslatesAllRelations(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	t1 := time.Unix(1_700_000_000, 0).UTC()
	rels := []legacyRel{
		{ID: "r-1", SourceNodeID: "internal/kdb/db.go", TargetNodeID: "internal/kdb/btree", Type: "imports", CreatedAt: t1},
		{ID: "r-2", SourceNodeID: "cmd/pcke/main", TargetNodeID: "internal/kdb", Type: "imports", CreatedAt: t1},
		{ID: "r-3", SourceNodeID: "internal/analysis", TargetNodeID: "internal/kdb/event", Type: "calls", CreatedAt: t1},
	}
	seedLegacyRel(t, db, rels)
	runEngineTo12(t, db)

	store := event.New(db)
	for _, want := range rels {
		linkID := event.EscapeID("e:"+want.SourceNodeID) + ":" + event.EscapeID(want.Type) + ":" + event.EscapeID("e:"+want.TargetNodeID)
		got, err := store.Latest(context.Background(), event.KindLink, linkID)
		if err != nil {
			t.Fatalf("Latest(link %s -> %s): %v", want.SourceNodeID, want.TargetNodeID, err)
		}
		link, ok := got.(*event.Link)
		if !ok {
			t.Fatalf("Latest returned %T, want *Link", got)
		}
		if link.SrcRef != "e:"+want.SourceNodeID {
			t.Errorf("SrcRef = %q, want %q", link.SrcRef, "e:"+want.SourceNodeID)
		}
		if link.DstRef != "e:"+want.TargetNodeID {
			t.Errorf("DstRef = %q, want %q", link.DstRef, "e:"+want.TargetNodeID)
		}
		if link.EdgeType != want.Type {
			t.Errorf("EdgeType = %q, want %q", link.EdgeType, want.Type)
		}
		if link.Header().Version != 1 {
			t.Errorf("Version = %d, want 1", link.Header().Version)
		}
	}
}

func TestV0012_GraphTraversalWorksOnTranslatedData(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	rels := []legacyRel{
		{ID: "r-1", SourceNodeID: "a", TargetNodeID: "b", Type: "imports"},
		{ID: "r-2", SourceNodeID: "b", TargetNodeID: "c", Type: "imports"},
		{ID: "r-3", SourceNodeID: "x", TargetNodeID: "b", Type: "imports"},
	}
	seedLegacyRel(t, db, rels)
	runEngineTo12(t, db)

	// Forward from a → b
	gotFwd, err := graph.Neighbors(context.Background(), db, "e:a", graph.TraversalOptions{
		Direction: graph.Forward,
	})
	if err != nil {
		t.Fatalf("Neighbors forward: %v", err)
	}
	if !slices.Equal(refsToStrings(gotFwd), []string{"e:b"}) {
		t.Errorf("forward: got %v, want [e:b]", gotFwd)
	}

	// Reverse from b ← {a, x}
	gotRev, err := graph.Neighbors(context.Background(), db, "e:b", graph.TraversalOptions{
		Direction: graph.Reverse,
	})
	if err != nil {
		t.Fatalf("Neighbors reverse: %v", err)
	}
	got := refsToStrings(gotRev)
	slices.Sort(got)
	if !slices.Equal(got, []string{"e:a", "e:x"}) {
		t.Errorf("reverse: got %v, want [e:a e:x]", got)
	}

	// Reachable forward from a → {b, c}
	gotReach, err := graph.Reachable(context.Background(), db, "e:a", graph.TraversalOptions{
		Direction: graph.Forward,
		MaxDepth:  3,
	})
	if err != nil {
		t.Fatalf("Reachable: %v", err)
	}
	reach := refsToStrings(gotReach)
	slices.Sort(reach)
	if !slices.Equal(reach, []string{"e:b", "e:c"}) {
		t.Errorf("reach: got %v, want [e:b e:c]", reach)
	}
}

func TestV0012_PreservesLegacyRecords(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	seedLegacyRel(t, db, []legacyRel{{ID: "r-1", SourceNodeID: "a", TargetNodeID: "b", Type: "imports"}})
	runEngineTo12(t, db)

	if err := db.View(context.Background(), func(rtx *tx.ReadTx) error {
		val, err := rtx.Get([]byte("rel:r-1"))
		if err != nil {
			return err
		}
		var rel legacyRel
		if err := json.Unmarshal(val, &rel); err != nil {
			return err
		}
		if rel.ID != "r-1" {
			t.Errorf("rel:r-1 lost: got %+v", rel)
		}
		return nil
	}); err != nil {
		t.Fatalf("View: %v", err)
	}
}

func TestV0012_IsIdempotent(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	seedLegacyRel(t, db, []legacyRel{{ID: "r-1", SourceNodeID: "a", TargetNodeID: "b", Type: "imports"}})
	runEngineTo12(t, db)

	// Second run is a no-op at engine level.
	e := migrate.New()
	e.Register(migrate.V0010EventBaseline())
	e.Register(migrate.V0011MigrateKnToE())
	e.Register(migrate.V0012MigrateRelToL())
	applied, err := e.Run(context.Background(), db)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if applied != 0 {
		t.Errorf("re-run applied = %d, want 0", applied)
	}

	// Link still at v1.
	store := event.New(db)
	linkID := event.EscapeID("e:a") + ":" + event.EscapeID("imports") + ":" + event.EscapeID("e:b")
	got, err := store.Latest(context.Background(), event.KindLink, linkID)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got.Header().Version != 1 {
		t.Errorf("version = %d, want 1", got.Header().Version)
	}
}

func TestV0012_NormalisesMissingEdgeType(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	seedLegacyRel(t, db, []legacyRel{
		{ID: "r-1", SourceNodeID: "a", TargetNodeID: "b", Type: ""}, // empty type
	})
	runEngineTo12(t, db)

	store := event.New(db)
	linkID := event.EscapeID("e:a") + ":" + event.EscapeID("related") + ":" + event.EscapeID("e:b")
	got, err := store.Latest(context.Background(), event.KindLink, linkID)
	if err != nil {
		t.Fatalf("Latest: %v (expected the empty type to default to 'related')", err)
	}
	link := got.(*event.Link)
	if link.EdgeType != "related" {
		t.Errorf("EdgeType = %q, want %q", link.EdgeType, "related")
	}
}

func TestV0012_SkipsRelationsWithMissingEndpoints(t *testing.T) {
	t.Parallel()
	db := openMigrationDB(t)

	seedLegacyRel(t, db, []legacyRel{
		{ID: "bad", SourceNodeID: "", TargetNodeID: "b", Type: "imports"},
		{ID: "ok", SourceNodeID: "a", TargetNodeID: "b", Type: "imports"},
	})
	runEngineTo12(t, db)

	store := event.New(db)
	okID := event.EscapeID("e:a") + ":" + event.EscapeID("imports") + ":" + event.EscapeID("e:b")
	if _, err := store.Latest(context.Background(), event.KindLink, okID); err != nil {
		t.Errorf("Latest(ok): %v", err)
	}

	// Verify nothing was written for the malformed record.
	count := 0
	if err := store.IterateKind(context.Background(), event.KindLink, func(event.Event) error {
		count++
		return nil
	}); err != nil {
		t.Fatalf("IterateKind: %v", err)
	}
	if count != 1 {
		t.Errorf("translated %d links, want 1 (malformed should be skipped)", count)
	}
}

// refsToStrings is a local helper since the graph_test fixture lives in
// a different test package and cannot be reused from migrate_test.
func refsToStrings(refs []graph.Ref) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = string(r)
	}
	return out
}

package session_test

import (
	"context"
	"slices"
	"testing"

	"github.com/jenaiz/pcke/internal/retrieval/session"
)

// TestBuildFocusMap_RanksByCallCount verifies the focus map orders refs
// by how many tool calls served them, derived from the o:session
// subgraph.
func TestBuildFocusMap_RanksByCallCount(t *testing.T) {
	t.Parallel()
	db, _, collector, ps := newPersistentStore(t)

	s := ps.Get("alpha")
	// e:hot.go served by three calls, e:warm.go by two, e:cold.go by one.
	s.Note(session.Observation{Refs: []string{"e:hot.go", "e:warm.go", "e:cold.go"}, Tool: "recall"})
	s.Note(session.Observation{Refs: []string{"e:hot.go", "e:warm.go"}, Tool: "recall"})
	s.Note(session.Observation{Refs: []string{"e:hot.go"}, Tool: "recall"})

	if err := collector.Close(); err != nil {
		t.Fatalf("collector.Close: %v", err)
	}

	fm, err := session.BuildFocusMap(context.Background(), db, "alpha")
	if err != nil {
		t.Fatalf("BuildFocusMap: %v", err)
	}
	if fm.SessionID != "alpha" {
		t.Errorf("SessionID = %q, want alpha", fm.SessionID)
	}

	want := []string{"e:hot.go", "e:warm.go", "e:cold.go"}
	if got := fm.Files(); !slices.Equal(got, want) {
		t.Fatalf("Files = %v, want %v", got, want)
	}

	// Counts must reflect calls-that-served, not raw edge counts.
	wantCounts := map[string]int{"e:hot.go": 3, "e:warm.go": 2, "e:cold.go": 1}
	for _, e := range fm.Entries {
		if wantCounts[e.Ref] != e.Count {
			t.Errorf("%s count = %d, want %d", e.Ref, e.Count, wantCounts[e.Ref])
		}
	}
}

func TestBuildFocusMap_TopAndEntityFiles(t *testing.T) {
	t.Parallel()
	db, _, collector, ps := newPersistentStore(t)

	s := ps.Get("beta")
	s.Note(session.Observation{Refs: []string{"e:a.go", "d:rule-1"}, Tool: "recall"})
	s.Note(session.Observation{Refs: []string{"e:a.go", "e:b.go"}, Tool: "recall"})

	if err := collector.Close(); err != nil {
		t.Fatalf("collector.Close: %v", err)
	}

	fm, err := session.BuildFocusMap(context.Background(), db, "beta")
	if err != nil {
		t.Fatalf("BuildFocusMap: %v", err)
	}

	// e:a.go served by 2 calls -> ranked first.
	if top := fm.Top(1); !slices.Equal(top, []string{"e:a.go"}) {
		t.Errorf("Top(1) = %v, want [e:a.go]", top)
	}
	// Top with n > len returns all; n <= 0 returns nil.
	if got := fm.Top(100); len(got) != len(fm.Entries) {
		t.Errorf("Top(100) len = %d, want %d", len(got), len(fm.Entries))
	}
	if got := fm.Top(0); got != nil {
		t.Errorf("Top(0) = %v, want nil", got)
	}

	// EntityFiles strips the e: prefix and drops decisions.
	wantEntities := []string{"a.go", "b.go"}
	got := fm.EntityFiles()
	slices.Sort(got)
	if !slices.Equal(got, wantEntities) {
		t.Errorf("EntityFiles = %v, want %v", got, wantEntities)
	}
}

func TestBuildFocusMap_EmptyAndUnknown(t *testing.T) {
	t.Parallel()
	db, _, _, _ := newPersistentStore(t)

	// Empty id -> empty non-nil map, no error.
	fm, err := session.BuildFocusMap(context.Background(), db, "")
	if err != nil {
		t.Fatalf("BuildFocusMap(empty): %v", err)
	}
	if len(fm.Entries) != 0 || fm.Files() == nil {
		t.Errorf("empty id: Entries=%v Files=%v, want empty entries + non-nil files", fm.Entries, fm.Files())
	}

	// Unknown session -> empty map, no error.
	fm, err = session.BuildFocusMap(context.Background(), db, "does-not-exist")
	if err != nil {
		t.Fatalf("BuildFocusMap(unknown): %v", err)
	}
	if len(fm.Entries) != 0 {
		t.Errorf("unknown session entries = %v, want empty", fm.Entries)
	}
}

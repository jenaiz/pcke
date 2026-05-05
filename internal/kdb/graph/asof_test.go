package graph_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/graph"
)

var (
	asofT1 = time.Unix(1_700_000_000, 0).UTC()
	asofT2 = asofT1.Add(1 * time.Hour)
	asofT3 = asofT1.Add(2 * time.Hour)
)

func ptrTime(t time.Time) *time.Time { return &t }

func TestNeighbors_AsOf_BeforeLinkExisted(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.appendLinkAt(t, "e:src", "imports", "e:dst", asofT2, event.LifecycleActive)

	cutoff := asofT1.Add(-time.Hour) // before any link
	got, err := graph.Neighbors(context.Background(), f.db, "e:src", graph.TraversalOptions{
		Direction: graph.Forward,
		AsOf:      ptrTime(cutoff),
	})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty (cutoff before link existed)", got)
	}
}

func TestNeighbors_AsOf_AtVersionBoundary(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.appendLinkAt(t, "e:src", "imports", "e:dst", asofT2, event.LifecycleActive)

	got, err := graph.Neighbors(context.Background(), f.db, "e:src", graph.TraversalOptions{
		Direction: graph.Forward,
		AsOf:      ptrTime(asofT2),
	})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if !slices.Equal(sortedRefs(got), []string{"e:dst"}) {
		t.Errorf("got %v, want [e:dst] (cutoff exactly at link CreatedAt)", got)
	}
}

func TestNeighbors_AsOf_BetweenActiveVersions(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// v1 active at t1, v2 active at t3 (still active, just newer payload).
	f.appendLinkAt(t, "e:src", "imports", "e:dst", asofT1, event.LifecycleActive)
	f.appendLinkAt(t, "e:src", "imports", "e:dst", asofT3, event.LifecycleActive)

	cutoff := asofT2 // between t1 and t3
	got, err := graph.Neighbors(context.Background(), f.db, "e:src", graph.TraversalOptions{
		Direction: graph.Forward,
		AsOf:      ptrTime(cutoff),
	})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if !slices.Equal(sortedRefs(got), []string{"e:dst"}) {
		t.Errorf("got %v, want [e:dst]", got)
	}
}

func TestNeighbors_AsOf_TombstoneActiveAtCutoff(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// v1 active at t1, v2 superseded at t3.
	f.appendLinkAt(t, "e:src", "imports", "e:dst", asofT1, event.LifecycleActive)
	f.appendLinkAt(t, "e:src", "imports", "e:dst", asofT3, event.LifecycleSuperseded)

	// cutoff between t1 and t3: v1 was active.
	got, err := graph.Neighbors(context.Background(), f.db, "e:src", graph.TraversalOptions{
		Direction: graph.Forward,
		AsOf:      ptrTime(asofT2),
	})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if !slices.Equal(sortedRefs(got), []string{"e:dst"}) {
		t.Errorf("got %v, want [e:dst] (v1 was active at cutoff)", got)
	}

	// cutoff after t3: v2 (superseded) is the active version → no edge.
	gotAfter, err := graph.Neighbors(context.Background(), f.db, "e:src", graph.TraversalOptions{
		Direction: graph.Forward,
		AsOf:      ptrTime(asofT3.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("Neighbors after tombstone: %v", err)
	}
	if len(gotAfter) != 0 {
		t.Errorf("after tombstone got %v, want empty", gotAfter)
	}
}

func TestNeighbors_AsOf_ReverseDirection(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.appendLinkAt(t, "e:src-a", "imports", "e:dst", asofT1, event.LifecycleActive)
	f.appendLinkAt(t, "e:src-b", "imports", "e:dst", asofT3, event.LifecycleActive)

	// At t2, only src-a's edge exists.
	got, err := graph.Neighbors(context.Background(), f.db, "e:dst", graph.TraversalOptions{
		Direction: graph.Reverse,
		AsOf:      ptrTime(asofT2),
	})
	if err != nil {
		t.Fatalf("Neighbors reverse: %v", err)
	}
	if !slices.Equal(sortedRefs(got), []string{"e:src-a"}) {
		t.Errorf("got %v, want [e:src-a] (src-b's edge is in the future)", got)
	}

	// After t3, both edges exist.
	gotAll, err := graph.Neighbors(context.Background(), f.db, "e:dst", graph.TraversalOptions{
		Direction: graph.Reverse,
		AsOf:      ptrTime(asofT3.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("Neighbors reverse all: %v", err)
	}
	if !slices.Equal(sortedRefs(gotAll), []string{"e:src-a", "e:src-b"}) {
		t.Errorf("got %v, want [e:src-a e:src-b]", gotAll)
	}
}

func TestNeighbors_AsOf_ReverseTombstone(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// v1 of (src→dst) active at t1, v2 superseded at t3.
	f.appendLinkAt(t, "e:src", "imports", "e:dst", asofT1, event.LifecycleActive)
	f.appendLinkAt(t, "e:src", "imports", "e:dst", asofT3, event.LifecycleSuperseded)

	// At t2, edge was active.
	got, err := graph.Neighbors(context.Background(), f.db, "e:dst", graph.TraversalOptions{
		Direction: graph.Reverse,
		AsOf:      ptrTime(asofT2),
	})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if !slices.Equal(sortedRefs(got), []string{"e:src"}) {
		t.Errorf("at t2 got %v, want [e:src]", got)
	}

	// After t3, edge tombstoned.
	gotAfter, err := graph.Neighbors(context.Background(), f.db, "e:dst", graph.TraversalOptions{
		Direction: graph.Reverse,
		AsOf:      ptrTime(asofT3.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("Neighbors after: %v", err)
	}
	if len(gotAfter) != 0 {
		t.Errorf("after tombstone got %v, want empty", gotAfter)
	}
}

func TestReachable_AsOf_RestrictsTopology(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// At t1: a -> b only.
	// At t3: also b -> c added.
	f.appendLinkAt(t, "e:a", "imports", "e:b", asofT1, event.LifecycleActive)
	f.appendLinkAt(t, "e:b", "imports", "e:c", asofT3, event.LifecycleActive)

	gotT1, err := graph.Reachable(context.Background(), f.db, "e:a", graph.TraversalOptions{
		Direction: graph.Forward,
		AsOf:      ptrTime(asofT2),
	})
	if err != nil {
		t.Fatalf("Reachable t1: %v", err)
	}
	if !slices.Equal(sortedRefs(gotT1), []string{"e:b"}) {
		t.Errorf("at t2 got %v, want [e:b] (b->c not yet)", gotT1)
	}

	gotT3, err := graph.Reachable(context.Background(), f.db, "e:a", graph.TraversalOptions{
		Direction: graph.Forward,
		AsOf:      ptrTime(asofT3.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("Reachable t3: %v", err)
	}
	if !slices.Equal(sortedRefs(gotT3), []string{"e:b", "e:c"}) {
		t.Errorf("after t3 got %v, want [e:b e:c]", gotT3)
	}
}

func TestNeighbors_AsOf_NilUsesLatest(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.appendLinkAt(t, "e:src", "imports", "e:dst", asofT1, event.LifecycleActive)

	got, err := graph.Neighbors(context.Background(), f.db, "e:src", graph.TraversalOptions{
		Direction: graph.Forward,
		AsOf:      nil,
	})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if !slices.Equal(sortedRefs(got), []string{"e:dst"}) {
		t.Errorf("got %v, want [e:dst]", got)
	}
}

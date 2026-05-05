package graph_test

import (
	"context"
	"errors"
	"slices"
	"sort"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/graph"
)

func TestReachable_EmptyStartRejected(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := graph.Reachable(context.Background(), f.db, "", graph.TraversalOptions{})
	if !errors.Is(err, graph.ErrInvalidStart) {
		t.Errorf("got %v, want ErrInvalidStart", err)
	}
}

func TestReachable_SingleHop(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.appendLink(t, "e:a", "imports", "e:b")

	got, err := graph.Reachable(context.Background(), f.db, "e:a", graph.TraversalOptions{
		Direction: graph.Forward,
	})
	if err != nil {
		t.Fatalf("Reachable: %v", err)
	}
	if !slices.Equal(sortedRefs(got), []string{"e:b"}) {
		t.Errorf("got %v, want [e:b]", got)
	}
}

func TestReachable_MultiHop(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// a -> b -> c -> d
	f.appendLink(t, "e:a", "imports", "e:b")
	f.appendLink(t, "e:b", "imports", "e:c")
	f.appendLink(t, "e:c", "imports", "e:d")

	got, err := graph.Reachable(context.Background(), f.db, "e:a", graph.TraversalOptions{
		Direction: graph.Forward,
	})
	if err != nil {
		t.Fatalf("Reachable: %v", err)
	}
	if !slices.Equal(sortedRefs(got), []string{"e:b", "e:c", "e:d"}) {
		t.Errorf("got %v, want [e:b e:c e:d]", got)
	}
}

func TestReachable_RespectsMaxDepth(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// 5-deep chain, MaxDepth=2 should stop at v3 (a's grandchildren).
	chain := []string{"e:v1", "e:v2", "e:v3", "e:v4", "e:v5"}
	for i := 0; i < len(chain)-1; i++ {
		f.appendLink(t, chain[i], "imports", chain[i+1])
	}

	got, err := graph.Reachable(context.Background(), f.db, "e:v1", graph.TraversalOptions{
		Direction: graph.Forward,
		MaxDepth:  2,
	})
	if err != nil {
		t.Fatalf("Reachable: %v", err)
	}
	if !slices.Equal(sortedRefs(got), []string{"e:v2", "e:v3"}) {
		t.Errorf("got %v, want [e:v2 e:v3] (depth=2 stops before v4)", got)
	}
}

func TestReachable_CycleDoesNotInfiniteLoop(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// a -> b -> a (cycle); from a, forward should yield {b} once
	f.appendLink(t, "e:a", "imports", "e:b")
	f.appendLink(t, "e:b", "imports", "e:a")

	got, err := graph.Reachable(context.Background(), f.db, "e:a", graph.TraversalOptions{
		Direction: graph.Forward,
		MaxDepth:  10,
	})
	if err != nil {
		t.Fatalf("Reachable: %v", err)
	}
	if !slices.Equal(sortedRefs(got), []string{"e:b"}) {
		t.Errorf("got %v, want [e:b] (cycle traversed once)", got)
	}
}

func TestReachable_VisitedCapExceededReturnsPartial(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// Star graph: a has 20 outgoing edges. With MaxVisited=5, we should
	// stop early and surface ErrVisitedCapExceeded with a partial result.
	for i := 0; i < 20; i++ {
		f.appendLink(t, "e:hub", "imports", "e:dst-"+string(rune('a'+i)))
	}

	got, err := graph.Reachable(context.Background(), f.db, "e:hub", graph.TraversalOptions{
		Direction:  graph.Forward,
		MaxVisited: 5,
	})
	if !errors.Is(err, graph.ErrVisitedCapExceeded) {
		t.Errorf("got %v, want ErrVisitedCapExceeded", err)
	}
	if len(got) > 5 {
		t.Errorf("partial result has %d refs, want <=5", len(got))
	}
	if len(got) == 0 {
		t.Errorf("partial result is empty; expected some refs before cap")
	}
}

func TestReachable_BothExploresBothSides(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// Diamond:  a -> b, c -> a, c -> d
	// From a:  forward {b}; reverse {c}; from c we reach d via forward
	// (Both direction continues the per-side dir on each hop)
	f.appendLink(t, "e:a", "imports", "e:b")
	f.appendLink(t, "e:c", "imports", "e:a")
	f.appendLink(t, "e:c", "imports", "e:d")

	got, err := graph.Reachable(context.Background(), f.db, "e:a", graph.TraversalOptions{
		Direction: graph.Both,
		MaxDepth:  3,
	})
	if err != nil {
		t.Fatalf("Reachable: %v", err)
	}
	want := []string{"e:b", "e:c"}
	// Note: e:d is NOT reachable from e:a in Both — going Reverse from a
	// reaches c, but the per-side direction is preserved on each hop, so
	// from c we continue Reverse (no edges into c). Forward from c would
	// reach d, but we're walking Reverse. This is the documented
	// behaviour of Direction.Both (per-side direction propagation).
	if !slices.Equal(sortedRefs(got), want) {
		t.Errorf("got %v, want %v", sortedRefs(got), want)
	}
}

func TestImpactRadius_EquivalentToReachableReverse(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// Three sources fan into target via two hops.
	//   src-1 -> mid-1 -> target
	//   src-2 -> mid-1 -> target
	//   src-3 -> mid-2 -> target
	f.appendLink(t, "e:src-1", "imports", "e:mid-1")
	f.appendLink(t, "e:src-2", "imports", "e:mid-1")
	f.appendLink(t, "e:src-3", "imports", "e:mid-2")
	f.appendLink(t, "e:mid-1", "imports", "e:target")
	f.appendLink(t, "e:mid-2", "imports", "e:target")

	gotIR, err := graph.ImpactRadius(context.Background(), f.db, "e:target", 5)
	if err != nil {
		t.Fatalf("ImpactRadius: %v", err)
	}
	gotRev, err := graph.Reachable(context.Background(), f.db, "e:target", graph.TraversalOptions{
		Direction: graph.Reverse,
		MaxDepth:  5,
	})
	if err != nil {
		t.Fatalf("Reachable Reverse: %v", err)
	}

	sort.Strings(sortedRefs(gotIR))
	sort.Strings(sortedRefs(gotRev))
	if !slices.Equal(sortedRefs(gotIR), sortedRefs(gotRev)) {
		t.Errorf("ImpactRadius and Reachable Reverse disagree:\n IR  = %v\n rev = %v",
			sortedRefs(gotIR), sortedRefs(gotRev))
	}

	want := []string{"e:mid-1", "e:mid-2", "e:src-1", "e:src-2", "e:src-3"}
	if !slices.Equal(sortedRefs(gotIR), want) {
		t.Errorf("got %v, want %v", sortedRefs(gotIR), want)
	}
}

func TestReachable_EdgeTypeFilter(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// Mixed edges; filter should restrict propagation.
	f.appendLink(t, "e:a", "imports", "e:b")
	f.appendLink(t, "e:b", "decision_link", "e:c") // not "imports"
	f.appendLink(t, "e:b", "imports", "e:d")

	got, err := graph.Reachable(context.Background(), f.db, "e:a", graph.TraversalOptions{
		Direction: graph.Forward,
		EdgeTypes: []string{"imports"},
	})
	if err != nil {
		t.Fatalf("Reachable: %v", err)
	}
	if !slices.Equal(sortedRefs(got), []string{"e:b", "e:d"}) {
		t.Errorf("got %v, want [e:b e:d] (decision_link blocked)", got)
	}
}

func TestImpactRadius_DefaultMaxDepth(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// 6-deep reverse chain — DefaultMaxDepth=5 stops at v6.
	for i := 1; i <= 6; i++ {
		src := "e:up-" + string(rune('a'+i-1))
		dst := "e:up-" + string(rune('a'+i))
		f.appendLink(t, src, "imports", dst)
	}
	// up-a -> up-b -> ... -> up-g; ImpactRadius from up-g should walk back.

	got, err := graph.ImpactRadius(context.Background(), f.db, "e:up-g", 0)
	if err != nil {
		t.Fatalf("ImpactRadius: %v", err)
	}
	if len(got) != 5 {
		t.Errorf("got %d refs, want 5 (default MaxDepth=5 stops before up-a)", len(got))
	}
}

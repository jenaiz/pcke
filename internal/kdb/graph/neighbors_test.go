package graph_test

import (
	"context"
	"errors"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/graph"
)

// fixture wires up a fresh kdb + event.Store + a few links the tests
// share. Cleanup is registered on the supplied *testing.T.
type fixture struct {
	db    *kdb.DB
	store *event.Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// Pre-grow so tests writing dozens of links don't exhaust the freelist
	// under kdb's CoW page allocation. 10 chunks ≈ 640 KB headroom.
	for range 10 {
		if err := db.Grow(); err != nil {
			t.Fatalf("db.Grow: %v", err)
		}
	}
	return &fixture{db: db, store: event.New(db)}
}

func (f *fixture) appendLink(t *testing.T, src, edge, dst string) {
	t.Helper()
	if _, err := f.store.AppendLink(context.Background(), &event.Link{
		SrcRef: src, EdgeType: edge, DstRef: dst,
	}); err != nil {
		t.Fatalf("AppendLink %s --%s--> %s: %v", src, edge, dst, err)
	}
}

// appendLinkAt is appendLink with an explicit CreatedAt; used to drive
// AsOf traversal tests deterministically. event.Store.appendInTx
// preserves a non-zero CreatedAt set on the header.
func (f *fixture) appendLinkAt(t *testing.T, src, edge, dst string, at time.Time, lifecycle event.Lifecycle) {
	t.Helper()
	link := &event.Link{
		Hdr:      event.Header{CreatedAt: at, Lifecycle: lifecycle},
		SrcRef:   src,
		EdgeType: edge,
		DstRef:   dst,
	}
	if _, err := f.store.AppendLink(context.Background(), link); err != nil {
		t.Fatalf("AppendLink %s --%s--> %s @ %v: %v", src, edge, dst, at, err)
	}
}

func sortedRefs(in []graph.Ref) []string {
	out := make([]string, len(in))
	for i, r := range in {
		out[i] = string(r)
	}
	sort.Strings(out)
	return out
}

func TestNeighbors_EmptyStartRejected(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := graph.Neighbors(context.Background(), f.db, "", graph.TraversalOptions{})
	if !errors.Is(err, graph.ErrInvalidStart) {
		t.Errorf("got %v, want ErrInvalidStart", err)
	}
}

func TestNeighbors_UnknownDirectionRejected(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := graph.Neighbors(context.Background(), f.db, "e:x", graph.TraversalOptions{
		Direction: graph.Direction(99),
	})
	if !errors.Is(err, graph.ErrUnknownDirection) {
		t.Errorf("got %v, want ErrUnknownDirection", err)
	}
}

func TestNeighbors_EmptyDB(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	got, err := graph.Neighbors(context.Background(), f.db, "e:x", graph.TraversalOptions{})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestNeighbors_ForwardOneHop(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.appendLink(t, "e:src", "imports", "e:dst-a")
	f.appendLink(t, "e:src", "imports", "e:dst-b")

	got, err := graph.Neighbors(context.Background(), f.db, "e:src", graph.TraversalOptions{
		Direction: graph.Forward,
	})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	want := []string{"e:dst-a", "e:dst-b"}
	if g := sortedRefs(got); !slices.Equal(g, want) {
		t.Errorf("got %v, want %v", g, want)
	}
}

func TestNeighbors_ReverseOneHop(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.appendLink(t, "e:src-a", "imports", "e:dst")
	f.appendLink(t, "e:src-b", "imports", "e:dst")

	got, err := graph.Neighbors(context.Background(), f.db, "e:dst", graph.TraversalOptions{
		Direction: graph.Reverse,
	})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	want := []string{"e:src-a", "e:src-b"}
	if g := sortedRefs(got); !slices.Equal(g, want) {
		t.Errorf("got %v, want %v", g, want)
	}
}

func TestNeighbors_BothCombinesDirections(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.appendLink(t, "e:x", "imports", "e:dst") // x  -> dst
	f.appendLink(t, "e:src", "imports", "e:x") // src -> x

	got, err := graph.Neighbors(context.Background(), f.db, "e:x", graph.TraversalOptions{
		Direction: graph.Both,
	})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	want := []string{"e:dst", "e:src"}
	if g := sortedRefs(got); !slices.Equal(g, want) {
		t.Errorf("got %v, want %v", g, want)
	}
}

func TestNeighbors_EdgeTypeFilter(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.appendLink(t, "e:src", "imports", "e:dst-a")
	f.appendLink(t, "e:src", "decision_link", "e:dst-b")

	got, err := graph.Neighbors(context.Background(), f.db, "e:src", graph.TraversalOptions{
		Direction: graph.Forward,
		EdgeTypes: []string{"imports"},
	})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if g := sortedRefs(got); !slices.Equal(g, []string{"e:dst-a"}) {
		t.Errorf("got %v, want [e:dst-a]", g)
	}
}

func TestNeighbors_LatestVersionWins(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// Same edge appended three times → only the latest matters.
	for i := 0; i < 3; i++ {
		f.appendLink(t, "e:src", "imports", "e:dst")
	}
	got, err := graph.Neighbors(context.Background(), f.db, "e:src", graph.TraversalOptions{
		Direction: graph.Forward,
	})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(got) != 1 || got[0] != "e:dst" {
		t.Errorf("got %v, want [e:dst]", got)
	}
}

func TestNeighbors_SkipsSupersededLink(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// v1 active.
	f.appendLink(t, "e:src", "imports", "e:dst")
	// v2 explicitly tombstones the edge.
	if _, err := f.store.AppendLink(context.Background(), &event.Link{
		Hdr:      event.Header{Lifecycle: event.LifecycleSuperseded},
		SrcRef:   "e:src",
		EdgeType: "imports",
		DstRef:   "e:dst",
	}); err != nil {
		t.Fatalf("AppendLink v2: %v", err)
	}

	got, err := graph.Neighbors(context.Background(), f.db, "e:src", graph.TraversalOptions{
		Direction: graph.Forward,
	})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty (edge tombstoned)", got)
	}

	// Reverse traversal must agree.
	gotReverse, err := graph.Neighbors(context.Background(), f.db, "e:dst", graph.TraversalOptions{
		Direction: graph.Reverse,
	})
	if err != nil {
		t.Fatalf("Neighbors reverse: %v", err)
	}
	if len(gotReverse) != 0 {
		t.Errorf("reverse got %v, want empty", gotReverse)
	}
}

func TestNeighbors_BidirectionalCycle(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.appendLink(t, "e:a", "imports", "e:b")
	f.appendLink(t, "e:b", "imports", "e:a")

	// From e:a, Direction.Both should surface e:b regardless of which
	// side reaches it; deduplication keeps the result clean.
	got, err := graph.Neighbors(context.Background(), f.db, "e:a", graph.TraversalOptions{
		Direction: graph.Both,
	})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if g := sortedRefs(got); !slices.Equal(g, []string{"e:b"}) {
		t.Errorf("got %v, want [e:b]", g)
	}
}

func TestNeighbors_DoesNotBleedAcrossSrcs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// "e:foo" and "e:foobar" share a textual prefix; the cursor stop
	// must distinguish them via the trailing ':'.
	f.appendLink(t, "e:foo", "imports", "e:dst-1")
	f.appendLink(t, "e:foobar", "imports", "e:dst-2")

	got, err := graph.Neighbors(context.Background(), f.db, "e:foo", graph.TraversalOptions{
		Direction: graph.Forward,
	})
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if g := sortedRefs(got); !slices.Equal(g, []string{"e:dst-1"}) {
		t.Errorf("got %v, want [e:dst-1] (no bleed from e:foobar)", g)
	}
}

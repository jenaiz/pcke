package query_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/query"
)

// openSeededDB returns a *kdb.DB pre-grown for write-heavy fixtures and
// seeded with the supplied links via the event Store.
func openSeededDB(t *testing.T, links []event.Link) *kdb.DB {
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
	store := event.New(db)
	for _, l := range links {
		l := l
		if _, err := store.AppendLink(context.Background(), &l); err != nil {
			t.Fatalf("AppendLink %s -> %s: %v", l.SrcRef, l.DstRef, err)
		}
	}
	return db
}

func runTraverse(t *testing.T, db *kdb.DB, dsl string) []string {
	t.Helper()
	q, err := query.Parse(dsl)
	if err != nil {
		t.Fatalf("Parse(%q): %v", dsl, err)
	}
	plan := query.BuildPlan(q)
	rs, err := query.Execute(context.Background(), db, plan)
	if err != nil {
		t.Fatalf("Execute(%q): %v", dsl, err)
	}
	out := make([]string, 0, len(rs.Rows))
	for _, row := range rs.Rows {
		ref, _ := row["ref"].(string)
		out = append(out, ref)
	}
	slices.Sort(out)
	return out
}

func TestExecute_Traverse_ForwardOneHop(t *testing.T) {
	t.Parallel()
	db := openSeededDB(t, []event.Link{
		{SrcRef: "e:a", EdgeType: "imports", DstRef: "e:b"},
		{SrcRef: "e:a", EdgeType: "imports", DstRef: "e:c"},
	})

	got := runTraverse(t, db, "nodes where traverse(edges, depth=1) from 'e:a'")
	want := []string{"e:b", "e:c"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExecute_Traverse_MultiHop(t *testing.T) {
	t.Parallel()
	db := openSeededDB(t, []event.Link{
		{SrcRef: "e:a", EdgeType: "imports", DstRef: "e:b"},
		{SrcRef: "e:b", EdgeType: "imports", DstRef: "e:c"},
		{SrcRef: "e:c", EdgeType: "imports", DstRef: "e:d"},
	})

	got := runTraverse(t, db, "nodes where traverse(edges, depth=3) from 'e:a'")
	want := []string{"e:b", "e:c", "e:d"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExecute_Traverse_ReverseDirection(t *testing.T) {
	t.Parallel()
	db := openSeededDB(t, []event.Link{
		{SrcRef: "e:src-a", EdgeType: "imports", DstRef: "e:dst"},
		{SrcRef: "e:src-b", EdgeType: "imports", DstRef: "e:dst"},
	})

	got := runTraverse(t, db, "nodes where traverse(edges, direction=reverse) from 'e:dst'")
	want := []string{"e:src-a", "e:src-b"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExecute_Traverse_EdgeTypeFilter(t *testing.T) {
	t.Parallel()
	db := openSeededDB(t, []event.Link{
		{SrcRef: "e:a", EdgeType: "imports", DstRef: "e:b"},
		{SrcRef: "e:a", EdgeType: "decision_link", DstRef: "e:c"},
	})

	got := runTraverse(t, db, "nodes where traverse(edges, edge='imports') from 'e:a'")
	want := []string{"e:b"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExecute_Traverse_LimitApplied(t *testing.T) {
	t.Parallel()
	links := []event.Link{}
	for _, dst := range []string{"e:b", "e:c", "e:d", "e:e", "e:f"} {
		links = append(links, event.Link{SrcRef: "e:a", EdgeType: "imports", DstRef: dst})
	}
	db := openSeededDB(t, links)

	got := runTraverse(t, db, "nodes where traverse(edges) from 'e:a' order by ref asc limit 3")
	if len(got) != 3 {
		t.Errorf("len = %d, want 3", len(got))
	}
}

func TestExecute_Traverse_AsOf_PinsHistoricalState(t *testing.T) {
	t.Parallel()
	t1 := time.Unix(1_700_000_000, 0).UTC()
	t3 := t1.Add(2 * time.Hour)

	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for range 10 {
		_ = db.Grow()
	}

	store := event.New(db)
	// At t1: e:a -> e:b only.
	if _, err := store.AppendLink(context.Background(), &event.Link{
		Hdr:    event.Header{CreatedAt: t1, Lifecycle: event.LifecycleActive},
		SrcRef: "e:a", EdgeType: "imports", DstRef: "e:b",
	}); err != nil {
		t.Fatalf("AppendLink t1: %v", err)
	}
	// At t3: e:b -> e:c added.
	if _, err := store.AppendLink(context.Background(), &event.Link{
		Hdr:    event.Header{CreatedAt: t3, Lifecycle: event.LifecycleActive},
		SrcRef: "e:b", EdgeType: "imports", DstRef: "e:c",
	}); err != nil {
		t.Fatalf("AppendLink t3: %v", err)
	}

	// AS OF t2 (between t1 and t3): only e:b reachable from e:a.
	t2 := t1.Add(time.Hour)
	dsl := "nodes where traverse(edges, depth=3) from 'e:a' as of '" + t2.Format(time.RFC3339) + "'"
	got := runTraverse(t, db, dsl)
	want := []string{"e:b"}
	if !slices.Equal(got, want) {
		t.Errorf("at t2 got %v, want %v (b->c not yet)", got, want)
	}

	// AS OF after t3: full chain reachable.
	tFuture := t3.Add(time.Hour)
	dsl = "nodes where traverse(edges, depth=3) from 'e:a' as of '" + tFuture.Format(time.RFC3339) + "'"
	got = runTraverse(t, db, dsl)
	want = []string{"e:b", "e:c"}
	if !slices.Equal(got, want) {
		t.Errorf("after t3 got %v, want %v", got, want)
	}
}

func TestExecute_AsOf_WithoutTraverse_ReturnsErr(t *testing.T) {
	t.Parallel()
	db := openSeededDB(t, nil)

	q, err := query.Parse("nodes as of '2026-04-01'")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	plan := query.BuildPlan(q)
	_, err = query.Execute(context.Background(), db, plan)
	if !errors.Is(err, query.ErrAsOfNotSupported) {
		t.Errorf("got %v, want ErrAsOfNotSupported", err)
	}
}

func TestExecute_NormalQuery_StillWorks(t *testing.T) {
	t.Parallel()
	// Regression guard: classic queries (no TRAVERSE, no AS OF) must
	// still hit the prefix-scan path.
	db := openSeededDB(t, nil)
	q, err := query.Parse("nodes where type = 'file'")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	plan := query.BuildPlan(q)
	rs, err := query.Execute(context.Background(), db, plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if rs == nil {
		t.Fatal("ResultSet is nil")
	}
}

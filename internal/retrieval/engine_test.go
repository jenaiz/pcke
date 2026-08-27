package retrieval_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/retrieval"
)

// fixture wires a kdb with entities, links, and decisions covering
// the typical "current file + dependency neighborhood + applicable
// rules" shape Assemble walks.
type fixture struct {
	db    *kdb.DB
	store *event.Store
	now   time.Time
}

func newFixture(t *testing.T) *fixture {
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
	return &fixture{
		db:    db,
		store: event.New(db),
		now:   time.Unix(1_700_000_000, 0).UTC(),
	}
}

func (f *fixture) addEntity(t *testing.T, eid, path, typ string) {
	t.Helper()
	if _, err := f.store.Append(context.Background(), &event.Entity{
		Hdr:  event.Header{CreatedAt: f.now},
		EID:  eid,
		Path: path,
		Type: typ,
	}); err != nil {
		t.Fatalf("addEntity %s: %v", eid, err)
	}
}

func (f *fixture) addDecision(t *testing.T, d *event.Decision) {
	t.Helper()
	if d.Hdr.CreatedAt.IsZero() {
		d.Hdr.CreatedAt = f.now
	}
	if d.Hdr.Lifecycle == 0 {
		d.Hdr.Lifecycle = event.LifecycleActive
	}
	if _, err := f.store.Append(context.Background(), d); err != nil {
		t.Fatalf("addDecision %s: %v", d.DID, err)
	}
}

func (f *fixture) addLink(t *testing.T, src, edge, dst string) {
	t.Helper()
	if _, err := f.store.AppendLink(context.Background(), &event.Link{
		Hdr:      event.Header{CreatedAt: f.now},
		SrcRef:   src,
		EdgeType: edge,
		DstRef:   dst,
	}); err != nil {
		t.Fatalf("addLink: %v", err)
	}
}

func TestAssemble_EmptyRequestReturnsWarning(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	eng := retrieval.New(f.db, retrieval.WithClock(func() time.Time { return f.now }))

	pkg, err := eng.Assemble(context.Background(), retrieval.Request{})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(pkg.Sections) != 0 {
		t.Errorf("got %d sections, want 0", len(pkg.Sections))
	}
	if len(pkg.Warnings) == 0 {
		t.Errorf("want a warning about empty request")
	}
}

func TestAssemble_FileNeighborhood(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// Topology:
	//   internal/kdb/db.go        — the focus file
	//     imports → internal/kdb/btree.go
	//     reverse: cmd/pcke/main.go imports it
	//   adr-0008                  — a global ADR (must)
	f.addEntity(t, "internal/kdb/db.go", "internal/kdb/db.go", "file")
	f.addEntity(t, "internal/kdb/btree.go", "internal/kdb/btree.go", "file")
	f.addEntity(t, "cmd/pcke/main.go", "cmd/pcke/main.go", "file")
	f.addLink(t, "e:internal/kdb/db.go", "imports", "e:internal/kdb/btree.go")
	f.addLink(t, "e:cmd/pcke/main.go", "imports", "e:internal/kdb/db.go")
	f.addDecision(t, &event.Decision{
		DID:      "adr-0008",
		Title:    "Context Graph Pivot",
		Body:     "Pivot the data model to a typed-event graph.",
		Severity: event.SeverityMust,
		Scope:    event.ScopeGlobal,
		Source:   "adr",
	})

	eng := retrieval.New(f.db, retrieval.WithClock(func() time.Time { return f.now }))
	pkg, err := eng.Assemble(context.Background(), retrieval.Request{
		FilePath: "internal/kdb/db.go",
	})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(pkg.Sections) == 0 {
		t.Fatal("want at least 1 section")
	}
	// The focus file's own entity must show up (proximity 1.0).
	hasFocus := false
	for _, s := range pkg.Sections {
		if s.Ref == "e:internal/kdb/db.go" {
			hasFocus = true
		}
	}
	if !hasFocus {
		t.Errorf("focus file e:internal/kdb/db.go not in sections: %+v", refs(pkg))
	}

	// Both 1-hop neighbors are reachable via Direction.Both at depth 2.
	for _, want := range []string{"e:internal/kdb/btree.go", "e:cmd/pcke/main.go"} {
		found := false
		for _, s := range pkg.Sections {
			if s.Ref == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %s in sections; got %+v", want, refs(pkg))
		}
	}
}

func TestAssemble_BudgetTruncates(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.addEntity(t, "f.go", "f.go", "file")
	// Add 5 decisions linked to nothing (so they don't appear in the
	// graph traversal — Assemble should NOT pull them in for a
	// file-scoped request).
	bigBody := strings.Repeat("word ", 1000) // ~1300 tokens each
	for i := 0; i < 5; i++ {
		f.addDecision(t, &event.Decision{
			DID:      string(rune('a' + i)),
			Title:    "x",
			Body:     bigBody,
			Severity: event.SeverityMust,
			Scope:    event.ScopeGlobal,
			Source:   "adr",
		})
	}
	// Link two of those decisions to the file so they show up in the
	// neighborhood. With ~1300 tokens each and a 2000 budget, only
	// one of the two should be admitted; the second is truncated.
	f.addLink(t, "e:f.go", "decision_link", "d:a")
	f.addLink(t, "e:f.go", "decision_link", "d:b")

	eng := retrieval.New(f.db, retrieval.WithClock(func() time.Time { return f.now }))
	pkg, err := eng.Assemble(context.Background(), retrieval.Request{
		FilePath: "f.go",
		Budget:   2000,
	})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !pkg.Truncated {
		t.Errorf("want Truncated=true with 2 oversized decisions and 2000-token budget")
	}
	if pkg.TokensUsed > 2000 {
		t.Errorf("TokensUsed = %d, want ≤ 2000", pkg.TokensUsed)
	}
}

func TestAssemble_HighestSeverityRanksFirst(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.addEntity(t, "f.go", "f.go", "file")
	f.addDecision(t, &event.Decision{
		DID: "may-rule", Title: "may rule", Body: "may", Severity: event.SeverityMay, Scope: event.ScopeGlobal, Source: "adr",
	})
	f.addDecision(t, &event.Decision{
		DID: "must-rule", Title: "must rule", Body: "must", Severity: event.SeverityMust, Scope: event.ScopeGlobal, Source: "adr",
	})
	f.addLink(t, "e:f.go", "decision_link", "d:may-rule")
	f.addLink(t, "e:f.go", "decision_link", "d:must-rule")

	eng := retrieval.New(f.db, retrieval.WithClock(func() time.Time { return f.now }))
	pkg, err := eng.Assemble(context.Background(), retrieval.Request{FilePath: "f.go"})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	// must-rule must rank above may-rule.
	mustIdx, mayIdx := -1, -1
	for i, s := range pkg.Sections {
		switch s.Ref {
		case "d:must-rule":
			mustIdx = i
		case "d:may-rule":
			mayIdx = i
		}
	}
	if mustIdx < 0 || mayIdx < 0 {
		t.Fatalf("missing decisions: must=%d may=%d", mustIdx, mayIdx)
	}
	if mustIdx >= mayIdx {
		t.Errorf("must@%d, may@%d — want must first", mustIdx, mayIdx)
	}
}

func TestAssemble_AlreadyServedDeprioritised(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.addEntity(t, "f.go", "f.go", "file")
	f.addDecision(t, &event.Decision{
		DID: "fresh", Title: "fresh", Body: "fresh", Severity: event.SeverityShould, Scope: event.ScopeGlobal, Source: "adr",
	})
	f.addDecision(t, &event.Decision{
		DID: "served", Title: "served", Body: "served", Severity: event.SeverityShould, Scope: event.ScopeGlobal, Source: "adr",
	})
	f.addLink(t, "e:f.go", "decision_link", "d:fresh")
	f.addLink(t, "e:f.go", "decision_link", "d:served")

	eng := retrieval.New(f.db, retrieval.WithClock(func() time.Time { return f.now }))
	pkg, err := eng.Assemble(context.Background(), retrieval.Request{
		FilePath:      "f.go",
		AlreadyServed: []string{"d:served"},
	})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	freshIdx, servedIdx := -1, -1
	for i, s := range pkg.Sections {
		switch s.Ref {
		case "d:fresh":
			freshIdx = i
		case "d:served":
			servedIdx = i
		}
	}
	if freshIdx < 0 || servedIdx < 0 {
		t.Fatalf("missing decisions: fresh=%d served=%d", freshIdx, servedIdx)
	}
	if freshIdx >= servedIdx {
		t.Errorf("fresh@%d should rank above served@%d (novelty=1 vs 0)", freshIdx, servedIdx)
	}
}

func TestAssemble_DefaultBudgetWhenZero(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.addEntity(t, "f.go", "f.go", "file")
	eng := retrieval.New(f.db, retrieval.WithClock(func() time.Time { return f.now }))
	pkg, err := eng.Assemble(context.Background(), retrieval.Request{FilePath: "f.go"})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if pkg.BudgetLimit != retrieval.DefaultBudget {
		t.Errorf("BudgetLimit = %d, want %d (DefaultBudget)", pkg.BudgetLimit, retrieval.DefaultBudget)
	}
}

func TestAssemble_UnscannedFileReturnsWarning(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// No entities seeded — request points at nothing.
	eng := retrieval.New(f.db, retrieval.WithClock(func() time.Time { return f.now }))
	pkg, err := eng.Assemble(context.Background(), retrieval.Request{
		FilePath: "ghost.go",
	})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(pkg.Sections) != 0 {
		t.Errorf("ghost file: got %d sections, want 0", len(pkg.Sections))
	}
	if !hasWarningContaining(pkg, "not in the index") || !hasWarningContaining(pkg, "pcke scan") {
		t.Errorf("want actionable not-indexed warning, got: %v", pkg.Warnings)
	}
}

func TestAssemble_NormalizesLeadingDotSlashAndSlash(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"./internal/x.go", "/internal/x.go", "internal/./x.go"} {
		f := newFixture(t)
		f.addEntity(t, "internal/x.go", "internal/x.go", "file")
		eng := retrieval.New(f.db, retrieval.WithClock(func() time.Time { return f.now }))
		pkg, err := eng.Assemble(context.Background(), retrieval.Request{FilePath: path})
		if err != nil {
			t.Fatalf("Assemble(%q): %v", path, err)
		}
		if len(pkg.Sections) == 0 {
			t.Errorf("path %q: got 0 sections, want the entity to resolve", path)
		}
	}
}

func TestAssemble_NoNeighborsAddsDeepHint(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// Only the focus file's own entity exists; no links to anything.
	f.addEntity(t, "lonely.go", "lonely.go", "file")
	eng := retrieval.New(f.db, retrieval.WithClock(func() time.Time { return f.now }))
	pkg, err := eng.Assemble(context.Background(), retrieval.Request{FilePath: "lonely.go"})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(pkg.Sections) == 0 {
		t.Fatal("want the focus file's own entity as a section")
	}
	if !hasWarningContaining(pkg, "no linked context") || !hasWarningContaining(pkg, "--deep") {
		t.Errorf("want a --deep hint when there are no neighbors, got: %v", pkg.Warnings)
	}
}

// hasWarningContaining reports whether any warning contains sub.
func hasWarningContaining(pkg *retrieval.ContextPackage, sub string) bool {
	for _, w := range pkg.Warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func TestAssemble_WeightSumWarning(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.addEntity(t, "f.go", "f.go", "file")
	bad := retrieval.Weights{Recency: 0.5, Severity: 0.5, Proximity: 0.5, Novelty: 0.5} // sums to 2.0
	eng := retrieval.New(f.db,
		retrieval.WithClock(func() time.Time { return f.now }),
		retrieval.WithWeights(bad),
	)
	pkg, err := eng.Assemble(context.Background(), retrieval.Request{FilePath: "f.go"})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	hasWarning := false
	for _, w := range pkg.Warnings {
		if strings.Contains(w, "weights sum") {
			hasWarning = true
		}
	}
	if !hasWarning {
		t.Errorf("expected weight-sum warning, got: %v", pkg.Warnings)
	}
}

// refs returns the section refs for tabular error output.
func refs(pkg *retrieval.ContextPackage) []string {
	out := make([]string, len(pkg.Sections))
	for i, s := range pkg.Sections {
		out[i] = s.Ref
	}
	return out
}

package retrieval_test

import (
	"context"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/retrieval"
)

// scoreOf returns the score the engine assigned to the section with the
// given ref, or -1 if no such section was admitted.
func scoreOf(pkg *retrieval.ContextPackage, ref string) float64 {
	for _, s := range pkg.Sections {
		if s.Ref == ref {
			return s.Score
		}
	}
	return -1
}

// TestWithWorkflow_EdgeBoostDifferentiates verifies that a review
// workflow (which prioritises decision_link) and a refactor workflow
// (which prioritises imports) score the same graph differently: each
// boosts the neighbour reached via its preferred edge type.
func TestWithWorkflow_EdgeBoostDifferentiates(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// Topology around the focus file f.go:
	//   f.go --imports--> dep.go
	//   f.go --decision_link--> d:rule-x  (a "should" decision)
	f.addEntity(t, "f.go", "f.go", "file")
	f.addEntity(t, "dep.go", "dep.go", "file")
	f.addLink(t, "e:f.go", "imports", "e:dep.go")
	f.addDecision(t, &event.Decision{
		DID:      "rule-x",
		Title:    "Style rule",
		Body:     "Prefer composition.",
		Severity: event.SeverityShould,
		Scope:    event.ScopeModule,
		Source:   "annotation",
	})
	f.addLink(t, "e:f.go", "decision_link", "d:rule-x")

	clock := retrieval.WithClock(func() time.Time { return f.now })
	req := retrieval.Request{FilePath: "f.go", Budget: 100000}

	reviewEng := retrieval.New(f.db, clock, retrieval.WithWorkflow(retrieval.WorkflowReview))
	refactorEng := retrieval.New(f.db, clock, retrieval.WithWorkflow(retrieval.WorkflowRefactor))

	reviewPkg, err := reviewEng.Assemble(context.Background(), req)
	if err != nil {
		t.Fatalf("review Assemble: %v", err)
	}
	refactorPkg, err := refactorEng.Assemble(context.Background(), req)
	if err != nil {
		t.Fatalf("refactor Assemble: %v", err)
	}

	// Review boosts the decision_link neighbour (d:rule-x); refactor does
	// not boost it. So the decision should score higher under review.
	reviewDec := scoreOf(reviewPkg, "d:rule-x")
	refactorDec := scoreOf(refactorPkg, "d:rule-x")
	if reviewDec < 0 || refactorDec < 0 {
		t.Fatalf("decision missing: review=%v refactor=%v", reviewDec, refactorDec)
	}
	if reviewDec <= refactorDec {
		t.Errorf("review decision score %v should exceed refactor %v (decision_link boost)",
			reviewDec, refactorDec)
	}

	// Refactor boosts the imports neighbour (dep.go); review does not.
	reviewDep := scoreOf(reviewPkg, "e:dep.go")
	refactorDep := scoreOf(refactorPkg, "e:dep.go")
	if reviewDep < 0 || refactorDep < 0 {
		t.Fatalf("dep missing: review=%v refactor=%v", reviewDep, refactorDep)
	}
	if refactorDep <= reviewDep {
		t.Errorf("refactor dep score %v should exceed review %v (imports boost)",
			refactorDep, reviewDep)
	}
}

// TestWithWorkflow_ExploreIsNeutral confirms WorkflowExplore applies no
// edge boost: it matches the default-weights engine for a graph that
// has priority edges.
func TestWithWorkflow_ExploreUsesDefaultWeights(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.addEntity(t, "f.go", "f.go", "file")
	f.addEntity(t, "dep.go", "dep.go", "file")
	f.addLink(t, "e:f.go", "imports", "e:dep.go")

	clock := retrieval.WithClock(func() time.Time { return f.now })
	req := retrieval.Request{FilePath: "f.go", Budget: 100000}

	defaultEng := retrieval.New(f.db, clock)
	exploreEng := retrieval.New(f.db, clock, retrieval.WithWorkflow(retrieval.WorkflowExplore))

	defPkg, err := defaultEng.Assemble(context.Background(), req)
	if err != nil {
		t.Fatalf("default Assemble: %v", err)
	}
	expPkg, err := exploreEng.Assemble(context.Background(), req)
	if err != nil {
		t.Fatalf("explore Assemble: %v", err)
	}

	if got, want := scoreOf(expPkg, "e:dep.go"), scoreOf(defPkg, "e:dep.go"); got != want {
		t.Errorf("explore dep score %v != default %v; explore must be neutral", got, want)
	}
}

// TestRequestWorkflow_OverridesEngineDefault verifies a per-call
// req.Workflow takes effect even on a default engine constructed
// without WithWorkflow — the path the MCP tools and `pcke context`
// rely on.
func TestRequestWorkflow_OverridesEngineDefault(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.addEntity(t, "f.go", "f.go", "file")
	f.addEntity(t, "dep.go", "dep.go", "file")
	f.addLink(t, "e:f.go", "imports", "e:dep.go")

	clock := retrieval.WithClock(func() time.Time { return f.now })
	eng := retrieval.New(f.db, clock) // no WithWorkflow

	explorePkg, err := eng.Assemble(context.Background(), retrieval.Request{
		FilePath: "f.go", Budget: 100000, Workflow: retrieval.WorkflowExplore,
	})
	if err != nil {
		t.Fatalf("explore Assemble: %v", err)
	}
	refactorPkg, err := eng.Assemble(context.Background(), retrieval.Request{
		FilePath: "f.go", Budget: 100000, Workflow: retrieval.WorkflowRefactor,
	})
	if err != nil {
		t.Fatalf("refactor Assemble: %v", err)
	}

	// Refactor prioritises imports, so dep.go (reached via imports) should
	// score higher than under explore — proving the per-call workflow took
	// effect on a default engine.
	if refactorPkg := scoreOf(refactorPkg, "e:dep.go"); refactorPkg <= scoreOf(explorePkg, "e:dep.go") {
		t.Errorf("per-request refactor dep score %v should exceed explore %v",
			refactorPkg, scoreOf(explorePkg, "e:dep.go"))
	}
}

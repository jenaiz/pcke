package retrieval_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/retrieval"
)

// TestAssemble_AnticipatedContainsOneHopNeighbors verifies the
// _anticipated field holds the focus file's direct graph neighbours
// that were not admitted as sections, and never the focus file itself
// (PRD v5.2 §6.2 F15.T4).
func TestAssemble_AnticipatedContainsOneHopNeighbors(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// Topology:
	//   focus.go --imports--> a.go --imports--> b.go (2 hops, not 1-hop)
	//   dep-caller.go --imports--> focus.go (reverse 1-hop)
	f.addEntity(t, "focus.go", "focus.go", "file")
	f.addEntity(t, "a.go", "a.go", "file")
	f.addEntity(t, "b.go", "b.go", "file")
	f.addEntity(t, "dep-caller.go", "dep-caller.go", "file")
	f.addLink(t, "e:focus.go", "imports", "e:a.go")
	f.addLink(t, "e:a.go", "imports", "e:b.go")
	f.addLink(t, "e:dep-caller.go", "imports", "e:focus.go")

	// Tiny budget so few sections are admitted, leaving neighbours for
	// the anticipated list.
	eng := retrieval.New(f.db, retrieval.WithClock(func() time.Time { return f.now }))
	pkg, err := eng.Assemble(context.Background(), retrieval.Request{
		FilePath: "focus.go",
		Budget:   1, // force heavy truncation
	})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	// The 1-hop neighbours of focus.go are a.go (forward) and
	// dep-caller.go (reverse). b.go is 2 hops away and must NOT appear.
	want := []string{"e:a.go", "e:dep-caller.go"}
	if got := pkg.Anticipated; !slices.Equal(got, want) {
		t.Fatalf("Anticipated = %v, want %v", got, want)
	}

	// The focus file itself must never be anticipated.
	if slices.Contains(pkg.Anticipated, "e:focus.go") {
		t.Errorf("Anticipated should not contain the focus file")
	}
}

// TestAssemble_AnticipatedExcludesAdmitted verifies a neighbour that is
// admitted as a section does not also appear in the anticipated list.
func TestAssemble_AnticipatedExcludesAdmitted(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.addEntity(t, "focus.go", "focus.go", "file")
	f.addEntity(t, "neighbor.go", "neighbor.go", "file")
	f.addLink(t, "e:focus.go", "imports", "e:neighbor.go")

	// Large budget so the neighbour is admitted as a section.
	eng := retrieval.New(f.db, retrieval.WithClock(func() time.Time { return f.now }))
	pkg, err := eng.Assemble(context.Background(), retrieval.Request{
		FilePath: "focus.go",
		Budget:   100000,
	})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if slices.Contains(pkg.Anticipated, "e:neighbor.go") {
		t.Errorf("admitted neighbour should not be in Anticipated; got %v", pkg.Anticipated)
	}
}

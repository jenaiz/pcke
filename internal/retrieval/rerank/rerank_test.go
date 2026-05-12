package rerank_test

import (
	"testing"

	"github.com/jenaiz/pcke/internal/retrieval"
	"github.com/jenaiz/pcke/internal/retrieval/rerank"
)

func TestDefault_ReturnsReranker(t *testing.T) {
	t.Parallel()
	r := rerank.Default()
	if r == nil {
		t.Fatal("Default() returned nil")
	}
}

func TestDefault_PassThroughOnEmpty(t *testing.T) {
	t.Parallel()
	r := rerank.Default()
	got, err := r.Reorder("any query", nil)
	if err != nil {
		t.Fatalf("Reorder(empty): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Reorder(empty) = %v, want empty slice", got)
	}
}

func TestDefault_PreservesSections(t *testing.T) {
	t.Parallel()
	r := rerank.Default()
	in := []retrieval.Section{
		{Ref: "e:a.go", Score: 0.9},
		{Ref: "e:b.go", Score: 0.7},
		{Ref: "e:c.go", Score: 0.4},
	}
	got, err := r.Reorder("kdb buffer pool", in)
	if err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	if len(got) != len(in) {
		t.Fatalf("len = %d, want %d", len(got), len(in))
	}
	// Pass-through implementations must preserve every input ref.
	want := map[string]bool{"e:a.go": true, "e:b.go": true, "e:c.go": true}
	for _, s := range got {
		if !want[s.Ref] {
			t.Errorf("unexpected ref in output: %s", s.Ref)
		}
		delete(want, s.Ref)
	}
	if len(want) != 0 {
		t.Errorf("missing refs in output: %v", want)
	}
}

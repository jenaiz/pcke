package query

import (
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/index/fts"
)

func TestPlannerSearch(t *testing.T) {
	idx := fts.NewIndex()
	idx.AddDocument("error handling in go")
	idx.AddDocument("go error patterns and best practices")
	idx.AddDocument("javascript frameworks")
	idx.Commit()

	p := NewPlanner(idx)
	results := p.Search("error handling", 10)

	if len(results) < 2 {
		t.Fatalf("got %d results, want >= 2", len(results))
	}

	// Results should be sorted by score descending.
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Error("results not sorted by score descending")
		}
	}
}

func TestPlannerSearchWithLimit(t *testing.T) {
	idx := fts.NewIndex()
	for range 20 {
		idx.AddDocument("common term in all documents")
	}
	idx.Commit()

	p := NewPlanner(idx)
	results := p.Search("common", 5)

	if len(results) != 5 {
		t.Errorf("got %d results with limit 5, want 5", len(results))
	}
}

func TestPlannerSearchEmptyQuery(t *testing.T) {
	idx := fts.NewIndex()
	idx.AddDocument("hello world")
	idx.Commit()

	p := NewPlanner(idx)
	results := p.Search("", 10)

	if len(results) != 0 {
		t.Errorf("empty query returned %d results", len(results))
	}
}

func TestPlannerSearchNoMatch(t *testing.T) {
	idx := fts.NewIndex()
	idx.AddDocument("hello world")
	idx.Commit()

	p := NewPlanner(idx)
	results := p.Search("nonexistent", 10)

	if len(results) != 0 {
		t.Errorf("got %d results for non-matching query", len(results))
	}
}

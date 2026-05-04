package federation

import (
	"fmt"
	"testing"

	"github.com/jenaiz/pcke/internal/query"
)

func TestMergeResults_Basic(t *testing.T) {
	results := []repoResult{
		{
			repo: "repoA",
			rows: []query.Row{
				{"id": "node1", "name": "func_a"},
				{"id": "node2", "name": "func_b"},
			},
		},
		{
			repo: "repoB",
			rows: []query.Row{
				{"id": "node1", "name": "func_c"},
			},
		},
	}

	frs := mergeResults(results, 0)
	if len(frs.Results) != 3 {
		t.Errorf("expected 3 results, got %d", len(frs.Results))
	}
	if len(frs.Repos) != 2 {
		t.Errorf("expected 2 repos, got %d", len(frs.Repos))
	}
	// Same id in different repos are NOT deduped.
	for _, r := range frs.Results {
		if r.Row["_repo"] == nil {
			t.Error("missing _repo annotation")
		}
	}
}

func TestMergeResults_DedupWithinRepo(t *testing.T) {
	results := []repoResult{
		{
			repo: "repoA",
			rows: []query.Row{
				{"id": "node1", "name": "func_a"},
				{"id": "node1", "name": "func_a_dup"}, // same id, same repo
			},
		},
	}

	frs := mergeResults(results, 0)
	if len(frs.Results) != 1 {
		t.Errorf("expected 1 result (dedup), got %d", len(frs.Results))
	}
}

func TestMergeResults_Limit(t *testing.T) {
	results := []repoResult{
		{
			repo: "repoA",
			rows: []query.Row{
				{"id": "n1"}, {"id": "n2"}, {"id": "n3"},
			},
		},
		{
			repo: "repoB",
			rows: []query.Row{
				{"id": "n4"}, {"id": "n5"},
			},
		},
	}

	frs := mergeResults(results, 2)
	if len(frs.Results) != 2 {
		t.Errorf("expected 2 results (limit), got %d", len(frs.Results))
	}
}

func TestMergeResults_WithErrors(t *testing.T) {
	results := []repoResult{
		{
			repo: "healthy",
			rows: []query.Row{{"id": "n1"}},
		},
		{
			repo: "broken",
			err:  errTest,
		},
	}

	frs := mergeResults(results, 0)
	if len(frs.Results) != 1 {
		t.Errorf("expected 1 result, got %d", len(frs.Results))
	}
	if len(frs.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(frs.Errors))
	}
	if frs.Errors[0].Repo != "broken" {
		t.Errorf("error repo: got %q", frs.Errors[0].Repo)
	}
}

func TestMergeResults_EmptyRows(t *testing.T) {
	results := []repoResult{
		{repo: "empty", rows: nil},
	}
	frs := mergeResults(results, 0)
	if len(frs.Results) != 0 {
		t.Errorf("expected 0 results, got %d", len(frs.Results))
	}
	// Repos with 0 rows should not appear in Repos list.
	if len(frs.Repos) != 0 {
		t.Errorf("expected 0 contributing repos, got %d", len(frs.Repos))
	}
}

var errTest = fmt.Errorf("test error")

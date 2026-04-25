package fts_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// CorpusQuery represents a single evaluation query from the corpus file.
type CorpusQuery struct {
	ID             string   `json:"id"`
	Text           string   `json:"text"`
	RelevantDocIDs []string `json:"relevant_doc_ids"`
	Tags           []string `json:"tags"`
}

// Corpus is the top-level structure of testdata/fts/queries.json.
type Corpus struct {
	Version     int           `json:"version"`
	Description string        `json:"description"`
	Queries     []CorpusQuery `json:"queries"`
}

// loadCorpus reads and parses the evaluation corpus file.
func loadCorpus(t *testing.T) *Corpus {
	t.Helper()

	data, err := os.ReadFile("../../../../testdata/fts/queries.json")
	if err != nil {
		// Try from project root (when running from different CWD).
		data, err = os.ReadFile("testdata/fts/queries.json")
		if err != nil {
			t.Fatalf("load corpus: %v (run from project root or internal/kdb/index/fts/)", err)
		}
	}

	var c Corpus
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}

	return &c
}

// PrecisionAtK computes Precision@K: the fraction of the top-K results that
// are in the relevant set. resultIDs are the ranked results from the search
// engine; relevantIDs are the ground truth relevant documents.
func PrecisionAtK(resultIDs, relevantIDs []string, k int) float64 {
	if k <= 0 || len(resultIDs) == 0 {
		return 0
	}

	relevant := make(map[string]bool, len(relevantIDs))
	for _, id := range relevantIDs {
		relevant[id] = true
	}

	hits := 0
	limit := k
	if limit > len(resultIDs) {
		limit = len(resultIDs)
	}

	for _, id := range resultIDs[:limit] {
		if relevant[id] {
			hits++
		}
	}

	return float64(hits) / float64(k)
}

// TestCorpusFileValid verifies the corpus file parses correctly and has
// the expected 20 queries.
func TestCorpusFileValid(t *testing.T) {
	c := loadCorpus(t)

	if c.Version != 1 {
		t.Errorf("corpus version = %d, want 1", c.Version)
	}

	if len(c.Queries) != 20 {
		t.Errorf("corpus has %d queries, want 20", len(c.Queries))
	}

	// Each query must have at least 5 relevant docs (for Precision@5).
	for _, q := range c.Queries {
		if q.ID == "" {
			t.Error("query with empty ID")
		}
		if q.Text == "" {
			t.Errorf("query %s has empty text", q.ID)
		}
		if len(q.RelevantDocIDs) < 5 {
			t.Errorf("query %s has %d relevant docs, want >= 5", q.ID, len(q.RelevantDocIDs))
		}
	}
}

// TestPrecisionAtKCalculation verifies the Precision@K metric calculation.
func TestPrecisionAtKCalculation(t *testing.T) {
	tests := []struct {
		name      string
		results   []string
		relevant  []string
		k         int
		wantPrecK float64
	}{
		{
			name:      "perfect",
			results:   []string{"a", "b", "c", "d", "e"},
			relevant:  []string{"a", "b", "c", "d", "e"},
			k:         5,
			wantPrecK: 1.0,
		},
		{
			name:      "none_relevant",
			results:   []string{"x", "y", "z"},
			relevant:  []string{"a", "b", "c"},
			k:         3,
			wantPrecK: 0.0,
		},
		{
			name:      "partial",
			results:   []string{"a", "x", "b", "y", "c"},
			relevant:  []string{"a", "b", "c", "d", "e"},
			k:         5,
			wantPrecK: 0.6,
		},
		{
			name:      "k_greater_than_results",
			results:   []string{"a", "b"},
			relevant:  []string{"a", "b", "c"},
			k:         5,
			wantPrecK: 0.4, // 2 hits / 5
		},
		{
			name:      "empty_results",
			results:   []string{},
			relevant:  []string{"a"},
			k:         5,
			wantPrecK: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PrecisionAtK(tt.results, tt.relevant, tt.k)
			if got != tt.wantPrecK {
				t.Errorf("PrecisionAtK = %f, want %f", got, tt.wantPrecK)
			}
		})
	}
}

// EvalReport holds the results of running the evaluation harness.
type EvalReport struct {
	Queries     int
	MeanPrecAt5 float64
	PerQuery    []QueryResult
}

// QueryResult holds the evaluation result for a single query.
type QueryResult struct {
	ID        string
	Text      string
	PrecAt5   float64
	ResultIDs []string
}

// String returns a human-readable summary of the evaluation report.
func (r *EvalReport) String() string {
	s := fmt.Sprintf("Precision@5 Evaluation: %d queries, mean=%.2f%%\n",
		r.Queries, r.MeanPrecAt5*100)
	for _, qr := range r.PerQuery {
		s += fmt.Sprintf("  %s [%.0f%%] %q\n", qr.ID, qr.PrecAt5*100, qr.Text)
	}
	return s
}

// RunEvaluation executes the evaluation harness. The searchFn takes a query
// string and returns ranked result document IDs.
func RunEvaluation(corpus *Corpus, searchFn func(query string) []string) *EvalReport {
	report := &EvalReport{
		Queries:  len(corpus.Queries),
		PerQuery: make([]QueryResult, len(corpus.Queries)),
	}

	totalPrec := 0.0
	for i, q := range corpus.Queries {
		results := searchFn(q.Text)
		p := PrecisionAtK(results, q.RelevantDocIDs, 5)

		report.PerQuery[i] = QueryResult{
			ID:        q.ID,
			Text:      q.Text,
			PrecAt5:   p,
			ResultIDs: results,
		}
		totalPrec += p
	}

	if report.Queries > 0 {
		report.MeanPrecAt5 = totalPrec / float64(report.Queries)
	}

	return report
}

// TestEvalHarnessSmoke verifies the harness runs end-to-end with a dummy
// search function. This is a smoke test; real evaluation happens once BM25
// is implemented (F1.T9).
func TestEvalHarnessSmoke(t *testing.T) {
	c := loadCorpus(t)

	// Dummy search: return the relevant doc IDs verbatim (perfect recall).
	perfectSearch := func(query string) []string {
		for _, q := range c.Queries {
			if q.Text == query {
				return q.RelevantDocIDs
			}
		}
		return nil
	}

	report := RunEvaluation(c, perfectSearch)

	if report.MeanPrecAt5 != 1.0 {
		t.Errorf("MeanPrecAt5 with perfect search = %f, want 1.0", report.MeanPrecAt5)
	}

	t.Logf("\n%s", report.String())
}

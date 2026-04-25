// Package query implements the query planner for kdb.
//
// Phase 1 provides a trivial single-field FTS planner: tokenize the query
// string, run BM25 scoring across the inverted index, and return ranked
// results. Multi-field and composite queries are post-v1.
//
// Phase 1 — Task F1.T10.
package query

import "github.com/jenaiz/pcke/internal/kdb/index/fts"

// Result is a single query result with a document ID and relevance score.
type Result struct {
	DocID uint64
	Score float64
}

// Planner executes FTS queries against an [fts.Index].
type Planner struct {
	index *fts.Index
}

// NewPlanner creates a query planner backed by the given FTS index.
func NewPlanner(index *fts.Index) *Planner {
	return &Planner{index: index}
}

// Search tokenizes the query and returns BM25-ranked results.
// At most limit results are returned. If limit <= 0, all results are returned.
func (p *Planner) Search(query string, limit int) []Result {
	tokens := fts.Tokenize(query)
	if len(tokens) == 0 {
		return nil
	}

	// Deduplicate query terms.
	seen := make(map[string]struct{}, len(tokens))
	var terms []string
	for _, tok := range tokens {
		if _, ok := seen[tok.Term]; !ok {
			seen[tok.Term] = struct{}{}
			terms = append(terms, tok.Term)
		}
	}

	bm25Results := p.index.ScoreBM25(terms)

	results := make([]Result, len(bm25Results))
	for i, r := range bm25Results {
		results[i] = Result{DocID: r.DocID, Score: r.Score}
	}

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return results
}

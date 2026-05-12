// Package rerank defines the optional vector re-ranker contract
// referenced in PRD v5.2 §4.4.
//
// The default build (`go build ./...`) compiles a no-op implementation:
// Reorder is a pass-through and Available reports false. The
// `-tags=rerank` build swaps the implementation for one that wires an
// embedding adapter (model loading remains the user's responsibility —
// PCKE bundles no models).
//
// The interface is deliberately narrow: a re-ranker may NEVER add
// sections the graph traversal did not already surface. This keeps
// provenance intact — every served fact is reachable from the query
// file through the typed-event graph.
package rerank

import "github.com/jenaiz/pcke/internal/retrieval"

// Reranker reorders an already-traversal-bounded slice of Sections
// based on the supplied query text.
//
// Contract:
//   - The returned slice must be a permutation of sections. Adding
//     or removing entries is forbidden; callers may rely on len-equality
//     and on every input ref being present in the output.
//   - Reorder must be deterministic for the same (query, sections)
//     input so retrieval is reproducible across runs.
//   - Implementations may return ctx-cancellation errors; in that
//     case the caller falls back to the input order.
type Reranker interface {
	// Available reports whether the re-ranker is ready to score
	// inputs. False means the caller should keep the input order.
	Available() bool

	// Reorder returns a permutation of sections ordered by relevance
	// to query. Empty sections returns an empty slice (no error).
	Reorder(query string, sections []retrieval.Section) ([]retrieval.Section, error)
}

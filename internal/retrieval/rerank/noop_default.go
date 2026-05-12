//go:build !rerank

package rerank

import "github.com/jenaiz/pcke/internal/retrieval"

// Default returns the build's default Reranker. The standard build
// returns a no-op implementation; `-tags=rerank` swaps in the adapter
// stub from rerank_enabled.go.
//
// The hardcoded false return value here is the entire reason the
// default build links zero embedding code: callers test
// `r := rerank.Default(); if r.Available() { ... }` and the dead
// branch optimises out.
func Default() Reranker { return noop{} }

type noop struct{}

func (noop) Available() bool { return false }

func (noop) Reorder(_ string, sections []retrieval.Section) ([]retrieval.Section, error) {
	return sections, nil
}

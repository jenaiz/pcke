// Package retrieval assembles ranked, budget-bounded context packages
// for AI agents from the typed-event graph.
//
// The package is the foundation of Phase 13 (PRD v5.2 §4): it scores
// candidate events by recency / severity / proximity / novelty, sums
// their word-count budget cost, and returns the highest-scoring set
// that fits the caller-supplied budget. Subsequent tasks (F13.T2/T3/
// T5) wire the engine into MCP tools (`get_context_for_file`,
// `get_context_for_diff`, proactive warnings) and the smart-sync
// command rewrite.
//
// # Naming
//
// The PRD sketches the path as `internal/context/`, but `package
// context` shadows the standard library. We use `internal/retrieval/`
// (`package retrieval`) here. The semantics, scoring formula, and
// task-numbering all match the PRD spec.
//
// # Scoring (PRD v5.2 §4.3)
//
//	Score = 0.25*recency + 0.35*severity + 0.25*proximity + 0.15*novelty
//
// Each component returns a value in [0, 1]; the weighted sum is
// therefore also in [0, 1]. Default weights are exposed via
// DefaultWeights() and can be overridden per-call.
//
// # Budget
//
// Section bodies are charged in approximate-token units defined as
// `word_count × 1.3`. This avoids a tokenizer dependency at the cost
// of ~10–15% accuracy versus tiktoken on English prose; for the
// purpose of "fit N items into a 2000-token budget" the approximation
// is more than precise enough, and benchmarks (T8 work) showed no
// material difference in selection outcomes versus a real tokenizer
// on PCKE's own corpus.
//
// # Versions
//
//   - F13.T1.1 (this commit): types + score + budget. No kdb dep.
//   - F13.T1.2: Engine.Assemble — kdb-backed integration.
package retrieval

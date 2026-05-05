// Package graph implements traversal primitives over the typed-event
// log stored by package event.
//
// The graph is implicit: nodes are typed references (Entity / Decision /
// etc.) addressed without a version; edges are Link events stored as
// l:<linkID>:v<N> records with a paired lr:<dst>:<edge>:<src> reverse
// index. Traversal is therefore deterministic and bounded — no
// embeddings, no similarity scores.
//
// The package exposes three operations:
//
//   - Neighbors(start, opts)      — 1-hop refs in a given direction
//   - Reachable(start, opts)      — BFS up to MaxDepth                (commit 2)
//   - ImpactRadius(target, depth) — Reverse Reachable shorthand        (commit 2)
//
// Direction:
//
//   - Forward — follow edges from src to dst
//   - Reverse — follow edges from dst to src (impact-radius queries)
//   - Both    — visit both directions; the visited set keys on
//     (ref, direction) so the same ref reached from both
//     sides is counted twice rather than blocking the
//     second visit
//
// MaxDepth defaults to 5; MaxVisited (the memory bound) defaults to
// 10 000 nodes. Both can be overridden per call.
//
// EdgeTypes is an inclusion filter: empty means "all edge types".
// A non-empty list restricts traversal to links whose EdgeType is
// one of the listed strings.
//
// AsOf (added in commit 3) pins the traversal to a point in the past:
// the version of every link that was active at AsOf is used, and edges
// whose lifecycle was Superseded at that time are skipped.
package graph

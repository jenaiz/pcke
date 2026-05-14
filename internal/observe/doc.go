// Package observe implements the Phase 14 (PRD v5.2 §5.6 F14.T2) async
// observation collector.
//
// The collector accepts SessionStart / Call records from MCP handlers
// without blocking, batches them, and persists them as Observation +
// Link events through the existing event.Store. Writes ride the kdb
// group-commit path so durability is bounded by [tx] group_commit_ms
// — the documented "best-effort observation" semantics.
//
// The collector is intentionally thin: it owns no business logic about
// what to record, just the buffering, batching and durability seams.
// Higher-level packages (retrieval/session) translate domain events
// into the simple Call / SessionStart structs this package consumes.
//
// All exported types are safe for concurrent use.
package observe

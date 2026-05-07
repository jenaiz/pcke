// Package decisions performs scan-time decision backfill: it harvests
// architectural decisions, rules, and constraints from artifacts that
// already exist in the repo (ADR files, source-code annotations, commit
// messages, doc headings) and writes them as Decision events into the
// typed-event log.
//
// Without this backfill, fresh v0.10.0 databases ship with empty d:
// records — the graph has Entity and Link records from migrations but
// no decisions to surface, which defeats the purpose of "every served
// fact is graph-reachable" (PRD v5.2 §1.3 design principle 2).
//
// Per PRD v5.2 §3.7 the supported sources are:
//
//	docs/adr/*.md             severity=must,   scope=global, source=adr
//	@pcke-rule annotations    severity=should, scope=file,   source=annotation
//	(?i)(decision|adr|rfc):   severity=should, scope=global, source=commit
//	architecture-doc headings severity=should, scope=global, source=doc
//
// Each source is implemented as a Backfill* function that takes a
// concrete input (root path / annotations slice / git intel / doc
// path) and the event.Store, and writes Decisions inside a single
// transaction.
//
// Backfill is idempotent: each Decision's id derives deterministically
// from its source artifact, and the existing event log skips an append
// if a record at e:<id>:v1 already exists (writes will produce v2/v3
// only when the underlying artifact actually changes between scans —
// which the v0.10.0 implementation does not yet detect; revisit in
// v0.11+).
package decisions

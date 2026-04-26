// Package query implements the pcke query DSL: lexer, parser, type checker,
// planner, and executor for structured queries over the knowledge base.
//
// The DSL supports SQL-like queries over typed collections (nodes, evolution,
// constraints, notes, relations) with WHERE filters, ORDER BY, and LIMIT.
// The planner selects optimal access strategies (index seek, range scan, or
// full scan) based on available indexes and query predicates.
//
// Phase 3 — Tasks F3.T1–F3.T6. See PRD §4.16.
package query

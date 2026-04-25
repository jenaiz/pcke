// Package fts implements a full-text search engine for kdb.
//
// The engine uses an inverted index with tiered segments that are periodically
// merged during checkpoint. This design avoids write amplification on every
// commit while maintaining query performance — segments are flushed in-memory
// first and only written to disk at checkpoint boundaries.
//
// BM25 scoring is used for relevance ranking. The tokenizer handles
// camelCase, snake_case, and CJK text to support code-oriented queries.
//
// Phase 1 — Tasks F1.T4–F1.T10.
package fts

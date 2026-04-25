// Package query provides a trivial query planner for kdb's full-text search.
//
// The planner tokenizes a user query, deduplicates terms, and delegates
// scoring to the [fts.Index] BM25 implementation. It exists as a separate
// package to keep the FTS engine independent of CLI concerns and to serve
// as the integration point for future query planning features (multi-field,
// filters, phrase queries).
package query

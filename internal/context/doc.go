// Package context implements the contextual intelligence engine for pcke.
//
// It provides ranked, budget-constrained context assembly for MCP tool
// responses. The engine scores knowledge items by recency, severity,
// proximity, and novelty, then truncates output to a configurable token budget.
package context

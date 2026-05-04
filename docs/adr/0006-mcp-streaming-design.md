# ADR-0006: MCP Streaming Design

> **Status:** Accepted
> **Date:** 2026-04-27
> **Authors:** jenaiz
> **Supersedes:** —

## Context

The `recall` and `query` MCP tools return all results in a single JSON response. For large knowledge bases (hundreds of nodes), this means the agent waits for the entire result set before processing anything. Agents benefit from receiving partial results early — they can begin reasoning while more results stream in.

The MCP specification supports progressive results via chunked tool responses. The mcp-go library (v0.49.0) handles stdio transport framing, but our tool handlers currently return a single `CallToolResult`. We need a streaming layer that emits chunked responses when result sets exceed a threshold.

## Decision

Implement a `StreamWriter` abstraction in `internal/mcp/stream.go` that:

1. **Threshold activation**: streaming activates automatically when the result set exceeds 50 items. Below that threshold, the existing single-response path is used (no behavioral change).

2. **Chunked emission**: results are grouped into chunks of 20 items each. Each chunk is a valid JSON array fragment, allowing the agent to parse incrementally.

3. **Backpressure via context cancellation**: if the client disconnects or cancels, the `context.Context` propagates cancellation and the StreamWriter stops emitting. No goroutine leak.

4. **Tool adapter pattern**: existing tool handlers (`handleRecall`, `handleGetModuleContext`) gain a streaming-aware wrapper. If the result count exceeds the threshold, the wrapper switches to chunked mode; otherwise it returns the existing single result.

5. **No protocol-level streaming**: since stdio transport in mcp-go v0.49.0 sends one JSON-RPC response per tool call, "streaming" is implemented as a single response containing a chunked JSON array with metadata (total count, chunk index). True progressive delivery would require protocol changes in a future mcp-go version. This design prepares the abstraction without breaking the current transport.

## Consequences

### Positive

- Agents get structured, chunk-annotated results they can process progressively
- No change to small result sets (< 50 items)
- Clean cancellation path prevents resource leaks
- Abstraction is ready for true streaming when mcp-go adds support

### Negative

- Single stdio response is still atomic at the transport level — chunking is logical, not physical
- Adds a layer of indirection in tool handlers

### Risks

- Chunk size (20) may need tuning; we expose it via config if needed later
- If mcp-go adds native streaming, we may need to refactor the adapter

## Alternatives Considered

1. **Wait for mcp-go native streaming**: Rejected — no timeline for the feature; our logical chunking adds value now.
2. **Pagination via cursor**: Rejected — requires stateful sessions and multiple round-trips; prompt templates and subscriptions in Phase 5 already add complexity.
3. **Always stream regardless of size**: Rejected — unnecessary overhead for small results; threshold approach is simpler.

## References

- PRD v3.1: `PRDs/PRD_PCKE_v3_1.md` §5.6.2
- Phase 5 prompt: `.github/prompts/phase-5-advanced-mcp.prompt.md`
- MCP specification: progressive results

# Advanced MCP Features

pcke v0.5 (originally tagged v1.1; see ADR-0008 for the version reset)
extends the MCP (Model Context Protocol) server with streaming,
subscriptions, prompt templates, and proactive context suggestions.

## Starting the Server

```bash
pcke serve
```

This starts the MCP server on stdio transport. It blocks until stdin closes
or a termination signal is received. The server requires an initialised
knowledge base (`pcke init` + `pcke scan`).

See [Getting Started](getting-started.md) for editor configuration examples.

---

## Streaming Responses

Large query results are delivered as chunked JSON, allowing agents to begin
processing before the full result set is assembled.

### How it works

When a tool call (`recall`, `get_module_context`) produces more than **50
results**, the response switches to chunked mode. Each chunk contains a
subset of results with metadata:

```json
[
  {
    "total": 150,
    "chunk_index": 0,
    "chunk_count": 8,
    "items": ["...", "..."]
  },
  {
    "total": 150,
    "chunk_index": 1,
    "chunk_count": 8,
    "items": ["...", "..."]
  }
]
```

Below the threshold (≤ 50 results), responses use the existing single-result
format — no behavioral change for small result sets.

### Configuration

Thresholds can be adjusted in `.pcke/config.toml`:

```toml
[mcp]
stream_threshold = 50   # minimum items to trigger chunking
chunk_size = 20         # items per chunk
```

---

## Subscriptions

Agents can subscribe to knowledge base change events and receive
notifications in real time.

### Event types

| Event | Fired when |
|-------|-----------|
| `knowledge.updated` | Knowledge base content changes |
| `scan.completed` | `pcke scan` finishes |
| `rule.added` | A new `@pcke-rule` annotation is detected |
| `module.changed` | Module structure or dependencies change |

### MCP notifications

When events fire, connected MCP clients receive a JSON-RPC notification:

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/knowledge/changed",
  "params": {
    "type": "scan.completed",
    "detail": "42 nodes created, 3 updated"
  }
}
```

### Lifecycle

Subscriptions are scoped to the MCP connection lifetime — they are not
persisted. When the agent disconnects, subscriptions are automatically
cleaned up with no goroutine leaks.

---

## Prompt Templates

Pre-built context packages that combine multiple knowledge sources into a
single, prompt-ready payload.

### Built-in templates

| Template | Purpose | Accepts `module` arg |
|----------|---------|---------------------|
| `onboarding` | New developer orientation: architecture, conventions, decisions | No |
| `review` | Code review: constraints, history | Yes |
| `debug` | Debugging: architecture, constraints scoped to a module | Yes |
| `refactor` | Refactoring: architecture, conventions, constraints | No |

### Usage via MCP

List available templates:

```
prompts/list → returns all 4 built-in templates
```

Get a resolved template:

```json
{
  "method": "prompts/get",
  "params": {
    "name": "review",
    "arguments": { "module": "internal/kdb" }
  }
}
```

The response contains a `PromptMessage` with the assembled context as a
single text block.

### Custom templates

Define custom templates in `.pcke/templates/` as TOML files:

```toml
name = "security-review"
description = "Security-focused code review context"
sections = ["constraints", "modules", "history"]
```

Custom templates are discovered at server startup and registered alongside
built-in templates.

---

## Proactive Context (opt-in)

When enabled, the MCP server analyzes tool queries and suggests relevant
constraints and history without being explicitly asked.

### How it works

1. An agent calls `recall` or `get_module_context` with a query mentioning
   a module (e.g., "tell me about internal/kdb")
2. pcke detects the module reference and loads associated constraints and
   history
3. The response includes a `suggested_context` field with the extra
   information

### Configuration

Proactive context is **disabled by default**. Enable it in
`.pcke/config.toml`:

```toml
[mcp]
proactive_context = true
```

Or via environment variable:

```sh
export PCKE_MCP_PROACTIVE_CONTEXT=true
```

When disabled, tool responses are identical to v0.4 (pre-pivot v1.0) — no
extra fields are added.

---

## Subgraph Retrieval (v0.11)

v0.11 adds two ranked, budget-bounded retrieval tools that walk the
typed-event graph instead of scanning the flat knowledge base.

### `get_context_for_file`

Ask for the 2-hop neighborhood of a single file: its direct imports,
its reverse callers, plus any decisions linked to it via
`decision_link`. The engine scores each section with the formula

```
Score = 0.25*recency + 0.35*severity + 0.25*proximity + 0.15*novelty
```

and admits the highest-scoring set that fits the requested token
budget.

```json
{
  "method": "tools/call",
  "params": {
    "name": "get_context_for_file",
    "arguments": {
      "file_path": "internal/kdb/btree/split.go",
      "budget": 2000,
      "workflow": "bugfix"
    }
  }
}
```

The response is streamed — one JSON object per ranked section, ending
in a `{"_summary": true, ...}` item with `tokens_used`, `budget_limit`,
`truncated`, `warnings`, and `section_count`.

> **Populate the graph first.** The neighborhood comes from import
> relations, which are only written by `pcke scan --deep`. Deep analysis
> supports Go, Java, JavaScript, and Python; for other languages a file
> still resolves to its own entity but has no linked neighbors. If a file
> isn't in the index at all, the summary `warnings` say so and point you
> at `pcke scan`.

Parameters:

| Parameter | Description |
|-----------|-------------|
| `file_path` (required) | Repository-relative path |
| `budget` | Approximate token budget; 0 = engine default (2000) |
| `workflow` | `explore` \| `bugfix` \| `feature` \| `review` \| `refactor` \| `test` |
| `focus` | `all` \| `constraints` \| `history` \| `patterns` \| `impact` |
| `already_served` | CSV refs to deprioritise (novelty 0) |
| `session_id` | Opaque id; refs the server has already streamed on this session are added to `already_served` automatically |

### `get_context_for_diff`

Same engine, fed with a set of changed files. If `changed_files` is
omitted, the server reads `git status` for the server's root and uses
every path with a non-clean state (untracked-only paths excluded).

```json
{
  "name": "get_context_for_diff",
  "arguments": {
    "budget": 2500
  }
}
```

The summary item echoes `changed_files` back so the caller can confirm
which paths the engine actually traversed.

### Session-scoped novelty

Pass the same `session_id` across multiple `get_context_for_file` /
`get_context_for_diff` calls and the engine treats every ref it has
already served on that session as `novelty = 0`. As of Phase 14 the
session is kdb-backed (`internal/retrieval/session` → `PersistentSession`
writing through the observation collector), so the accumulated set
survives a `pcke serve` restart; inspect it with `pcke sessions show`.

### Proactive warnings

When proactive context is enabled, `SuggestContext` now also includes
a `warnings` array listing must-severity decisions reachable from the
matched module via `decision_link`. Each warning carries `did`,
`title`, `body`, `severity`, and `source` so the agent can cite the
rule it just received.

### Architecture Quick Reference

`pcke sync` (v0.11+) writes a graph-derived **Architecture Quick
Reference** block into `.github/copilot-instructions.md` and
`.claude/CLAUDE.md`. The block lists:

- **Entry points** — entities with import fan-in ≤ 1 and fan-out ≥ 3.
- **Core modules** — directories ranked by aggregate incoming-import
  fan-in.
- **Decision hotspots** — files targeted by ≥ 3 must-severity
  decisions.

The block is omitted on legacy knowledge bases without a typed-event
log, so the rest of the rendered output is unchanged.

### Optional vector re-ranker (`-tags=rerank`)

The default build links no embedding code; `Reranker.Available()` is
hardcoded `false`. Building with `-tags=rerank` swaps in an adapter
stub that callers can replace with a real backend (ONNX, external
HTTP, etc.). No model is bundled in either build path. The re-ranker
may only permute the already-retrieved subgraph — it cannot add or
remove sections, so provenance is preserved.

```bash
go build -tags=rerank ./...
```

## Full MCP configuration reference

```toml
[mcp]
read_timeout_sec = 30       # MCP read timeout
proactive_context = false   # opt-in proactive suggestions
stream_threshold = 50       # chunking threshold (items)
chunk_size = 20             # items per chunk

# v0.11+ — optional re-ranker (requires -tags=rerank build)
[mcp.rerank]
enabled = false
backend = "onnx"            # or "external"
model = ""
```

All settings can be overridden with `PCKE_MCP_*` environment variables.

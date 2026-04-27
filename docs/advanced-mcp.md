# Advanced MCP Features

pcke v1.1 extends the MCP (Model Context Protocol) server with streaming,
subscriptions, prompt templates, and proactive context suggestions.

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

When disabled, tool responses are identical to v1.0 — no extra fields are
added.

---

## Full MCP configuration reference

```toml
[mcp]
read_timeout_sec = 30       # MCP read timeout
proactive_context = false   # opt-in proactive suggestions
stream_threshold = 50       # chunking threshold (items)
chunk_size = 20             # items per chunk
```

All settings can be overridden with `PCKE_MCP_*` environment variables.

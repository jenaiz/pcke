# Getting Started

## Installation

```bash
go install github.com/jenaiz/pcke/cmd/pcke@latest
```

Or build from source:

```bash
git clone https://github.com/jenaiz/pcke.git
cd pcke
make build
# Binary at ./bin/pcke
```

**Note:** CGO_ENABLED=1 is required (tree-sitter dependency for AST extraction).

## First scan

```bash
cd /path/to/your/repo
pcke scan
```

This creates a `.pcke/` directory with the knowledge base. The first scan
indexes all files; subsequent scans are incremental.

Use `pcke scan --full` to force a full rescan.

Use `pcke scan --deep` to also extract import relations, which the
`get_context_for_file` tool traverses to build a file's neighborhood.
Deep analysis is AST-based and currently supports **Go, Java, JavaScript,
and Python**; other languages (C/C++, Rust, …) are still indexed as
file-level entities but produce no import edges.

## Generate context files

```bash
pcke sync
```

Generates `.context/` Markdown files for AI assistants (Copilot, Cursor, etc.).

## Search

```bash
pcke recall "authentication middleware"
```

Full-text search with BM25 scoring. Use `--limit` to control results.

## Query

```bash
pcke query 'type = "module" AND tags CONTAINS "auth"'
```

Structured queries using the query DSL. See [Query Language](query-language.md).

## Explore the graph (v0.10+)

After `pcke scan`, the repo is also indexed as a typed-event graph:
entities, decisions, and the edges between them. A few starting points:

```bash
# What does this file pull in?
pcke graph neighbors e:cmd/pcke/main.go --depth=2

# What depends on this file?
pcke graph impact e:internal/kdb/btree --depth=3

# What rules apply here?
pcke decision list --severity=must
pcke decision show adr:0008-context-graph-pivot

# How did this file evolve?
pcke history e:internal/kdb/db.go
```

See the [Graph Guide](graph-guide.md) for worked examples (impact
analysis, time-travel, audit patterns) and
[Query Language](query-language.md) for the `TRAVERSE` / `AS OF` DSL.

## View diagnostics

```bash
pcke diagnostics
pcke diagnostics --format=json
```

## Run migrations

```bash
pcke migrate
```

See [Schema Migrations](schema-migrations.md).

## Start the MCP server

```bash
pcke serve
```

Starts the MCP (Model Context Protocol) server on stdio transport. AI agents
(Copilot, Cursor, Claude) connect via this command to access the knowledge base.

Configure your editor to use it — for example in `.vscode/mcp.json`:

```json
{
  "servers": {
    "pcke": {
      "type": "stdio",
      "command": "pcke",
      "args": ["serve"]
    }
  }
}
```

The server exposes tools (`recall`, `get_module_context`, `get_constraints`,
`get_history`), resources (`pcke://architecture`, `pcke://constraints`,
`pcke://decisions`), and prompt templates (`onboarding`, `review`, `debug`,
`refactor`).

See [Advanced MCP Features](advanced-mcp.md) for streaming, subscriptions,
and proactive context.

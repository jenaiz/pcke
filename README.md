# pcke — Project Context & Knowledge Engine

> **Status: v1.0** (all phases complete)

**pcke** is a Long-Term Engineering Memory — a local system that extracts
knowledge from codebases and serves it to AI coding agents (GitHub Copilot,
Claude Code) so they can operate with the context of a Senior Engineer who has
years of project history.

- **Zero token cost.** pcke never calls an LLM.
- **Single binary, zero dependencies.** No Docker, no cloud, no API keys.
- **Custom storage engine (`kdb`).** B+tree, WAL, inverted index, query
  language — built from scratch.
- **MCP server.** Exposes project knowledge to AI agents via the Model Context Protocol.

## Quickstart

```bash
git clone https://github.com/jenaiz/pcke.git
cd pcke
make verify   # lint + test + build

# Scan the repository and build the knowledge base
./bin/pcke scan

# Deep analysis with AST extraction (requires C compiler for tree-sitter)
./bin/pcke scan --deep

# Generate context files for AI agents
./bin/pcke sync

# Full-text search
./bin/pcke recall "error handling strategy"

# Offline compaction (reclaim space after deletions)
./bin/pcke compact

# Start MCP server (stdio transport)
./bin/pcke serve
```

Requirements: **Go 1.25+**, a **C compiler** (for tree-sitter / CGo), [golangci-lint](https://golangci-lint.run/) v2.

> **Note:** `pcke` uses [go-git](https://github.com/go-git/go-git) for Git
> history analysis and [go-tree-sitter](https://github.com/smacker/go-tree-sitter)
> for AST extraction (pulled automatically by `go mod tidy`).

## Using pcke with AI agents (MCP)

pcke includes a built-in [MCP](https://modelcontextprotocol.io/) server that
exposes project knowledge to AI coding agents over stdio.

### Setup

1. Build pcke and scan your project:

```bash
cd /path/to/your-project
/path/to/pcke scan --deep
```

2. Configure your AI agent to use pcke as an MCP server.

**GitHub Copilot** — add to `.vscode/mcp.json` in your project:

```json
{
  "servers": {
    "pcke": {
      "type": "stdio",
      "command": "/path/to/pcke",
      "args": ["serve"]
    }
  }
}
```

**Claude Code** — add to `.mcp.json` in your project:

```json
{
  "mcpServers": {
    "pcke": {
      "command": "/path/to/pcke",
      "args": ["serve"]
    }
  }
}
```

### Available MCP tools

| Tool | Description |
|------|-------------|
| `recall` | Semantic search across knowledge nodes (substring matching with weighted scoring) |
| `get_module_context` | Get all entities, dependencies, and metadata for a specific module |
| `get_constraints` | Infer Go and infrastructure constraints from the knowledge base |
| `get_history` | Get evolution history for a specific file path |

### Available MCP resources

| Resource URI | Description |
|-------------|-------------|
| `pcke://architecture` | Rendered architecture overview of the project |
| `pcke://constraints` | Rendered constraints and conventions |
| `pcke://decisions` | Rendered design decisions and ADRs |

## Documentation

| Document | Purpose |
|----------|---------|
| [PRD v3.1](PRDs/PRD_PCKE_v3_1.md) | Architecture & design decisions (what/why) |
| [Execution Plan](PRDs/PRD_PCKE_v3_1_EXECUTION_PLAN.md) | Implementation plan (how/when) |
| [Architecture notes](docs/architecture.md) | Build tags, component map, operational notes |
| [Documentation site](docs/index.md) | Getting started, API reference, query language, annotations |
| [Contributing](CONTRIBUTING.md) | Dev workflow, conventions, CI gates |

## Project phases

| Phase | Goal | Status |
|-------|------|--------|
| −1 | Bootstrap (CI, lint, release pipeline) | **complete** |
| 0 | Storage engine + CLI scan/sync | **complete** |
| 1 | Search & checkpointing | **complete** |
| 2 | Deep analysis & MCP | **complete** |
| 3 | Query language & polish | **complete** |
| 4 | v1.0 | **complete** |

## What's implemented

### Storage engine (`internal/kdb`)

A crash-safe embedded key-value store built from scratch:

- **B+tree** — Get/Put/Delete with 50/50 leaf splits, merge/redistribution on delete, overflow pages, and cursor iteration.
- **Write-Ahead Log (WAL)** — Append-only with CRC32C per record, fsync, segment rotation, and linear replay on open.
- **Checkpoint** — Fuzzy checkpoint flushes dirty pages and rotates WAL segments.
- **Buffer pool** — Pin/unpin page cache with clock-sweep eviction, adaptive sizing (> 90% hit rate target).
- **Freelist** — B+tree-backed page allocator (migrated from linked-list bootstrap).
- **Double-meta pages** — Atomic generation-based swap for crash recovery.
- **Transactions** — `View` (concurrent readers) and `Update` (exclusive writer) with WAL-first mutation, group commit, auto-commit, and meta swap.
- **CoW snapshot isolation** — readers see a consistent snapshot without blocking writers.
- **Secondary indexes** — by_module, by_tag, by_file, by_type for fast lookups.
- **Schema migrations** — versioned, idempotent, chunked (`pcke migrate`).
- **Offline compaction** — `pcke compact` copies live keys to a fresh file, reclaiming space.
- **Binary encoding** — Varint, little-endian, CRC32C, tagged record schema v1.
- **File locking** — Cross-platform `flock` with LOCK/PID single-process guard.

### Full-Text Search (`internal/kdb/index/fts`)

pcke includes a built-in full-text search engine optimized for code knowledge:

- **BM25 ranking** — relevance scoring tuned for engineering documentation.
- **Code-aware tokenizer** — splits camelCase, snake_case, and CJK text correctly.
- **Inverted index** — tiered segments with delta-encoded posting compression.
- **Tombstones** — deleted documents are excluded from queries and cleaned up on merge.
- **Recall command** — `pcke recall "how does authentication work"` returns ranked results.

#### Quick example

```bash
pcke scan --deep
pcke recall "error handling strategy"
```

### Deep analysis (`internal/analysis`)

- **tree-sitter AST extraction** — parses Go source files to extract functions, types, interfaces, and structs as knowledge entities.
- **Relations populator** — automatically discovers import relationships between modules.
- **Git history analysis** — extracts commit history, branch detection, and rename tracking.
- **Secrets detection** — scans for accidentally committed credentials (AWS keys, tokens, etc.).

### MCP server (`internal/mcp`)

- **4 tools** — `recall`, `get_module_context`, `get_constraints`, `get_history`.
- **3 resources** — `pcke://architecture`, `pcke://constraints`, `pcke://decisions`.
- **stdio transport** — compatible with VS Code, Claude Code, and any MCP client.

### CLI (`cmd/pcke`)

Cobra-based with subcommands: `init`, `scan`, `scan --deep`, `sync`, `rule`, `note`, `status`, `modules`, `diagnostics`, `config`, `recall`, `compact`, `serve`, `query`, `explain`, `export`, `migrate`.

### Configuration (`internal/config`)

Layered TOML config: CLI flags > environment > repo-level > user-level > defaults.

### Performance

pcke is designed for fast, low-overhead operation on real-world codebases:

| Metric | Target | Verified |
|--------|--------|----------|
| Incremental scan (10K files, no changes) | < 500 ms | ✓ |
| Full scan (10K files, cold) | < 10 s | ✓ |
| FTS query latency (p99, 10K nodes) | < 50 ms | ✓ |
| Binary size (stripped) | < 30 MB | ✓ |
| Memory peak (full scan, 10K files) | < 200 MB | ✓ |
| Buffer pool hit rate (steady-state) | > 90% | ✓ |

Benchmarks run on every commit via `BenchmarkCritical*` with a 10% regression gate.

### Schema Migrations

When pcke's internal storage format changes between versions, the `migrate`
command handles the upgrade:

```bash
pcke migrate
```

Migrations are versioned, chunked (safe for large databases), and idempotent
(running twice has the same effect as running once).

## License

[Apache-2.0](LICENSE)

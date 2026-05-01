# pcke — Project Context & Knowledge Engine

> **Status: v1.2** (CLI Track complete)

**pcke** is a Long-Term Engineering Memory — a local system that extracts
knowledge from codebases and serves it to AI coding agents (GitHub Copilot,
Claude Code) so they can operate with the context of a Senior Engineer who has
years of project history.

- **Zero token cost.** pcke never calls an LLM.
- **Single binary, zero dependencies.** No Docker, no cloud, no API keys.
- **Custom storage engine (`kdb`).** B+tree, WAL, inverted index, query
  language — built from scratch.
- **MCP server.** Exposes project knowledge to AI agents via the Model Context Protocol.

## Installation

### Homebrew (macOS/Linux)

```bash
brew install jenaiz/tap/pcke
```

### go install

```bash
go install github.com/jenaiz/pcke/cmd/pcke@latest
```

### Install script (macOS/Linux)

```bash
curl -sSfL https://raw.githubusercontent.com/jenaiz/pcke/main/install.sh | sh
```

To install a specific version:

```bash
VERSION=v1.2.1 curl -sSfL https://raw.githubusercontent.com/jenaiz/pcke/main/install.sh | sh
```

### Container

```bash
docker run --rm -v "$PWD:/project" -w /project ghcr.io/jenaiz/pcke scan
```

### Binary download

Download the appropriate archive from [GitHub Releases](https://github.com/jenaiz/pcke/releases):

| Platform | Archive |
|----------|---------|
| macOS (Apple Silicon) | `pcke_*_darwin_arm64.tar.gz` |
| macOS (Intel) | `pcke_*_darwin_amd64.tar.gz` |
| Linux (x86_64) | `pcke_*_linux_amd64.tar.gz` |
| Linux (ARM64) | `pcke_*_linux_arm64.tar.gz` |
| Windows (x86_64) | `pcke_*_windows_amd64.zip` |
| Windows (ARM64) | `pcke_*_windows_arm64.zip` |

### From source

```bash
git clone https://github.com/jenaiz/pcke.git
cd pcke
make install
```

### Verify with cosign

All release checksums are signed with [cosign](https://github.com/sigstore/cosign) keyless (OIDC):

```bash
cosign verify-blob \
  --certificate-identity-regexp="github.com/jenaiz/pcke" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com" \
  --signature checksums.txt.sig \
  checksums.txt
```

## Quickstart

```bash
# Install pcke (see Installation above for other methods)
brew install jenaiz/tap/pcke

# Scan the repository and build the knowledge base
pcke scan

# Deep analysis with AST extraction (requires C compiler for tree-sitter)
pcke scan --deep

# Generate context files for AI agents
pcke sync

# Full-text search
pcke recall "error handling strategy"

# Watch for changes and auto-scan
pcke watch

# Interactive query shell
pcke shell

# Offline compaction (reclaim space after deletions)
pcke compact

# Start MCP server (stdio transport)
pcke serve
```

Requirements: **Go 1.25+** (only for building from source), a **C compiler** (for tree-sitter / CGo deep scans), [golangci-lint](https://golangci-lint.run/) v2 (for development).

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

### Advanced MCP Features (v1.1)

pcke v1.1 introduces advanced MCP capabilities for richer AI agent integration:

- **Streaming responses.** Large query results are delivered progressively,
  so agents can start processing before the full result set is ready.
- **Subscriptions.** Agents can subscribe to knowledge base changes and
  receive real-time notifications when `pcke scan` completes or rules are
  updated.
- **Prompt templates.** Pre-built context packages (`onboarding`, `review`,
  `debug`, `refactor`) that combine architecture, conventions, and decisions
  into a single prompt-ready payload.
- **Proactive context.** (opt-in) pcke can suggest relevant constraints and
  history when an agent queries a module, without being explicitly asked.

Enable advanced features in `.pcke/config.toml`:

```toml
[mcp]
proactive_context = true
```

See [Advanced MCP documentation](docs/advanced-mcp.md) for details.

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
| 5 | Advanced MCP (v1.1) | **complete** |

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

Cobra-based CLI with 22+ commands:

| Command | Description |
|---------|-------------|
| `pcke init` | Initialize pcke in the current repository |
| `pcke scan` | Scan the repository and update the knowledge base |
| `pcke scan --deep` | Deep scan with AST extraction (requires CGo) |
| `pcke sync` | Generate output files (.context/, copilot-instructions, etc.) |
| `pcke recall` | Full-text search with BM25 scoring |
| `pcke query` | Query the knowledge base using the pcke DSL |
| `pcke explain` | Show execution plan for a query |
| `pcke export` | Export query results as JSON or YAML |
| `pcke shell` | Interactive query REPL |
| `pcke watch` | Watch for file changes and auto-scan |
| `pcke serve` | Start MCP server on stdio |
| `pcke note add/list/remove` | Manage project notes |
| `pcke rule add/list/remove` | Manage project rules |
| `pcke relations list/graph` | Explore module dependencies |
| `pcke schema` | Inspect the knowledge base schema |
| `pcke modules` | List detected modules |
| `pcke status` | Show knowledge base status |
| `pcke diagnostics` | Show database diagnostics |
| `pcke compact` | Compact the database to reclaim space |
| `pcke migrate` | Run schema migrations |
| `pcke config get/set/list` | View and manage configuration |
| `pcke clean` | Remove the knowledge base |

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

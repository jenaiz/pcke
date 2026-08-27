# pcke — Project Context & Knowledge Engine

> **Status: v0.13.0 — durable code memory shipped.** All post-pivot phases
> (11–15) are complete. The pivot is ratified by
> [ADR-0008](docs/adr/0008-context-graph-pivot.md) (amended by
> [ADR-0009](docs/adr/0009-durable-memory-corrections.md)). The next milestone
> is the `v1.0.0` stable release, gated on the acceptance demo in
> [PRD v5.2 §8](PRDs/PRD_PCKE_v5_2_DURABLE_MEMORY.md).

**pcke gives every repo a durable, queryable memory.** Decisions, code
structure, change history, and agent interactions are stored as typed,
versioned events in a local graph the developer owns. Retrieval is by
traversal, grounded by provenance — not vector similarity, not LLM-summarized
fragments.

- **Local, deterministic core.** Default builds compute the graph from code,
  git, and annotations. No LLM, no network, no API keys.
- **Single binary.** No Docker, no cloud.
- **Custom storage engine (`kdb`).** B+tree, WAL, inverted index, query
  language — built from scratch.
- **MCP server.** Exposes the graph to AI coding agents (GitHub Copilot,
  Claude Code) over the Model Context Protocol.
- **Optional vector re-ranker** (post-Phase 13, opt-in via `-tags=rerank`).
  Operates only over a subgraph already extracted by traversal — never as
  primary retrieval.

## Pre-pivot version note

Tags `v1.0.0` through `v2.0.0` predate the v1.0 stability commitment. They
are superseded by parallel `v0.4.0`–`v0.9.0` tags pointing at the same commits
and are marked withdrawn via `retract` in `go.mod`. Old tags remain in git
history; binary releases under the old names are retained but flagged as
superseded. New consumers should pin to `v0.9.0` or use `@latest` (now `v0.13.0`).

`v2.0.0` cannot be retracted in `go.mod` because the module path lacks a
`/v2` suffix; it was already unreachable via `go install`. Binary/Homebrew
users on `v2.0.0` should switch to `v0.9.0`.

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
VERSION=v0.9.0 curl -sSfL https://raw.githubusercontent.com/jenaiz/pcke/main/install.sh | sh
```

### Container

```bash
docker run --rm -v "$PWD:/project" -w /project ghcr.io/jenaiz/pcke scan
```

### Binary download

Download the appropriate archive from
[GitHub Releases](https://github.com/jenaiz/pcke/releases):

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

All release checksums are signed with [cosign](https://github.com/sigstore/cosign)
keyless (OIDC):

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

Requirements: **Go 1.25+** (only for building from source), a **C compiler**
(for tree-sitter / CGo deep scans), [golangci-lint](https://golangci-lint.run/)
v2 (for development).

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
| `recall` | Search the knowledge base for files, modules, and code entities |
| `get_context_for_file` | Ranked, budget-bounded context subgraph for a single file: 2-hop neighborhood, applicable decisions, linked references (run `pcke scan --deep` to populate import relations) |
| `get_context_for_diff` | Ranked context for the union of subgraphs around changed files (auto-detects git worktree status when none given) |
| `set_workflow` | Set the active workflow (bugfix / feature / review / refactor / …) for a session; tunes ranker weights and edge priorities |
| `get_module_context` | Summary of a module: files, dependencies, stability, entities |
| `get_constraints` | Engineering constraints and rules inferred from the knowledge base |
| `get_history` | Change history (version chain / evolution logs) for a file or key |

Federation tools (`query_federation`, `get_cross_repo_deps`) remain registered
but are **deprecated** (see [ADR-0008](docs/adr/0008-context-graph-pivot.md)).

### Available MCP resources

| Resource URI | Description |
|-------------|-------------|
| `pcke://architecture` | Rendered architecture overview of the project |
| `pcke://constraints` | Rendered constraints and conventions |
| `pcke://decisions` | Rendered design decisions and ADRs |

## Documentation

| Document | Purpose |
|----------|---------|
| [Architecture Decision Records](docs/adr/) | All ADRs, including [0008 (pivot)](docs/adr/0008-context-graph-pivot.md) and [0009 (corrections)](docs/adr/0009-durable-memory-corrections.md) |
| [Architecture notes](docs/architecture.md) | Build tags, component map, operational notes |
| [Documentation site](docs/index.md) | Getting started, API reference, query language, annotations |
| [Contributing](CONTRIBUTING.md) | Dev workflow, conventions, frozen/parked work |

## Project phases (roadmap to v1.0.0)

| Version | Phase | Name | Status |
|---------|-------|------|--------|
| v0.0.x – v0.3.x | -1 to 3 | Bootstrap → MCP → Query | DONE (pre-pivot) |
| v0.4.0 | 4 | v1 release (was `v1.0.0`) | DONE (retag) |
| v0.5.x | 5 | Advanced MCP | DONE (retag) |
| v0.6.x | CLI/Dist | DX & distribution | DONE (retag) |
| v0.7.0 | 6 | Onboarding | DONE (retag) |
| v0.8.0 | 7 | Schema Evolution | DONE (retag) |
| v0.9.0 | 8 | Federation (**deprecated**) | DONE (retag) |
| v0.9.1 | 11 | Pivot Reset | DONE |
| v0.10.0 | 12 | Graph Foundation | DONE — typed-event log, graph traversal, TRAVERSE/AS OF DSL, `pcke graph`/`decision`/`history` CLI |
| v0.11.0 | 13 | Subgraph Retrieval | DONE |
| v0.12.0 | 14 | Durable Sessions | DONE |
| **v0.13.0** | 15 | **Workflow Awareness** | **DONE** — `set_workflow` MCP tool, recipes, anticipatory context, `pcke context` CLI |
| **v1.0.0** | — | **Stable release: Durable Code Memory** | target |

Phases 9 (Local Embeddings as bundled feature) and 10 (IDE Extensions) remain
post-1.0; embeddings will land as an opt-in `-tags=rerank` re-ranker per
[ADR-0009 §2](docs/adr/0009-durable-memory-corrections.md).

## What's implemented today (v0.13.0)

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

- **BM25 ranking** — relevance scoring tuned for engineering documentation.
- **Code-aware tokenizer** — splits camelCase, snake_case, and CJK text correctly.
- **Inverted index** — tiered segments with delta-encoded posting compression.
- **Tombstones** — deleted documents are excluded from queries and cleaned up on merge.

### Deep analysis (`internal/analysis`)

- **tree-sitter AST extraction** — parses Go source files to extract functions, types, interfaces, and structs as knowledge entities.
- **Relations populator** — automatically discovers import relationships between modules.
- **Git history analysis** — extracts commit history, branch detection, and rename tracking.
- **Secrets detection** — scans for accidentally committed credentials.

### MCP server (`internal/mcp`)

- **4 tools**, **3 resources** (see table above).
- **stdio transport** — compatible with VS Code, Claude Code, and any MCP client.
- **Streaming, subscriptions, prompt templates** added in v0.5 (was v1.1).

### Federation (`internal/federation`) — **deprecated**

Multi-repo intelligence shipped in v0.9.0 (was v2.0.0). It is frozen as of
v0.9.1 and receives no new features. See [ADR-0008 §4.1](docs/adr/0008-context-graph-pivot.md)
for rationale. Removal after v1.0.0 is contingent on adoption signals.

### CLI (`cmd/pcke`)

Cobra-based CLI with 22+ commands (`pcke init`, `scan`, `sync`, `recall`,
`query`, `serve`, `note`, `rule`, `relations`, `schema`, `migrate`,
`compact`, `shell`, `watch`, `onboard`, `federation`, `status`, `diagnostics`,
`config`, `clean`, `export`, `explain`, `modules`).

### Configuration (`internal/config`)

Layered TOML config: CLI flags > environment > repo-level > user-level > defaults.

### Performance

| Metric | Target | Verified |
|--------|--------|----------|
| Incremental scan (10K files, no changes) | < 500 ms | ✓ |
| Full scan (10K files, cold) | < 10 s | ✓ |
| FTS query latency (p99, 10K nodes) | < 50 ms | ✓ |
| Binary size (stripped) | < 30 MB | ✓ |
| Memory peak (full scan, 10K files) | < 200 MB | ✓ |
| Buffer pool hit rate (steady-state) | > 90% | ✓ |

Benchmarks run on every commit via `BenchmarkCritical*` with a 10% regression
gate.

## License

[Apache-2.0](LICENSE)

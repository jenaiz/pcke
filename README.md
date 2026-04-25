# pcke — Project Context & Knowledge Engine

> **Status: pre-alpha** (Phase 0 — storage engine)

**pcke** is a Long-Term Engineering Memory — a local system that extracts
knowledge from codebases and serves it to AI coding agents (GitHub Copilot,
Claude Code) so they can operate with the context of a Senior Engineer who has
years of project history.

- **Zero token cost.** pcke never calls an LLM.
- **Single binary, zero dependencies.** No Docker, no cloud, no API keys.
- **Custom storage engine (`kdb`).** B+tree, WAL, inverted index, query
  language — built from scratch.

## Quickstart

```bash
git clone https://github.com/jenaiz/pcke.git
cd pcke
make verify   # lint + test + build

# Scan the repository and build the knowledge base
./bin/pcke scan

# Generate context files for AI agents
./bin/pcke sync
```

Requirements: **Go 1.23+**, [golangci-lint](https://golangci-lint.run/) v1.61+.

> **Note:** `pcke` uses [go-git](https://github.com/go-git/go-git) for Git
> history analysis (pulled automatically by `go mod tidy`).

## Documentation

| Document | Purpose |
|----------|---------|
| [PRD v3.1](PRDs/PRD_PCKE_v3_1.md) | Architecture & design decisions (what/why) |
| [Execution Plan](PRDs/PRD_PCKE_v3_1_EXECUTION_PLAN.md) | Implementation plan (how/when) |
| [Architecture notes](docs/architecture.md) | Build tags, component map, operational notes |
| [Contributing](CONTRIBUTING.md) | Dev workflow, conventions, CI gates |

## Project phases

| Phase | Goal | Status |
|-------|------|--------|
| −1 | Bootstrap (CI, lint, release pipeline) | **complete** |
| 0 | Storage engine + CLI scan/sync | **in progress** |
| 1 | Search & checkpointing | not started |
| 2 | Deep analysis & MCP | not started |
| 3 | Query language & polish | not started |
| 4 | v1.0 | not started |

## What's implemented

### Storage engine (`internal/kdb`)

A crash-safe embedded key-value store built from scratch:

- **B+tree** — Get/Put/Delete with 50/50 leaf splits, overflow pages, and cursor iteration.
- **Write-Ahead Log (WAL)** — Append-only with CRC32C per record, fsync, and linear replay on open.
- **Buffer pool** — Pin/unpin page cache with dirty tracking and batch flush.
- **Freelist** — B+tree-backed page allocator (migrated from linked-list bootstrap).
- **Double-meta pages** — Atomic generation-based swap for crash recovery.
- **Transactions** — `View` (concurrent readers) and `Update` (exclusive writer) with WAL-first mutation, auto-commit, and meta swap.
- **Binary encoding** — Varint, little-endian, CRC32C, tagged record schema v1.
- **File locking** — Cross-platform `flock` with LOCK/PID single-process guard.

### CLI (`cmd/pcke`)

Cobra-based with subcommands: `init`, `scan`, `sync`, `rule`, `note`, `status`, `modules`, `diagnostics`, `config`.
Most commands are stubs awaiting the analysis and output layers.

### Configuration (`internal/config`)

Layered TOML config: CLI flags > environment > repo-level > user-level > defaults.

## License

[Apache-2.0](LICENSE)

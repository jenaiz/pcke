# pcke — Architecture (Phase −1 stub)

This document is a living companion to [PRD v3.1](../PRDs/PRD_PCKE_v3_1.md)
and the [Execution Plan](../PRDs/PRD_PCKE_v3_1_EXECUTION_PLAN.md). It will be
expanded during Phase 0 (Plan §14, "Docs por fase").

For the authoritative architecture, **always read the PRD first**. This file
records implementation-time decisions, build-tag conventions, and
operational notes that don't belong in the PRD itself.

---

## Component map (post-Phase 0 target)

```
cmd/pcke/                  — CLI entry point (Cobra wiring lands in Phase 0)
internal/log/              — slog factory; subsystem-tagged loggers + redaction
internal/kdb/page/         — page manager, buffer pool, freelist
internal/kdb/btree/        — B+tree engine
internal/kdb/wal/          — WAL writer, segment, recovery, checkpoint
internal/kdb/tx/           — transaction manager, double-meta swap, snapshot
internal/kdb/index/fts/    — full-text search (Phase 1)
internal/kdb/index/        — secondary indexes (by_module, by_tag, …)
internal/kdb/query/        — query DSL (Phase 3)
internal/kdb/encoding/     — record encoding schema v1
internal/kdb/diagnostics/  — Stats surface + pcke diagnostics support
internal/kdb/lock/         — flock + LOCK/PID single-process guard
internal/analysis/         — file-tree scan, go-git, secrets filter
internal/output/           — Markdown context + agent instructions
internal/mcp/              — MCP server (Phase 2)
```

The Phase −1 tree contains `.gitkeep` files in every directory above. Real
code lands task-by-task per the DAG in Plan §4.

---

## Build tags

### `kdbdebug`

Activates two pieces of code that must never ship in releases:

1. **Invariant assertions** inside the buffer pool, B+tree, WAL, and transaction
   manager. Asserts the post-conditions of every mutation (sortedness,
   balance, no orphan pages, WAL-before-data ordering, freelist coverage).
2. **Crash-injection hooks** registered at fixed sites (pre-WAL-write,
   pre-fsync-WAL, post-fsync-WAL-pre-meta, pre-meta-swap, post-meta-swap, …).
   Reads the `PCKE_CRASH_AT` env var; if it matches a hook name, calls
   `os.Exit(137)` to simulate `SIGKILL`. Used by the crash harness in
   `internal/kdb/testutil/crashsim/` (Plan §7.2).

Build instructions:

```bash
go test -tags=kdbdebug ./...
make test-debug
```

Releases must **not** carry the tag. CI explicitly omits it from `make build`.
The flag is documented here so contributors understand why some files have
extra `//go:build kdbdebug` blocks.

---

## Concurrency, by phase

The PRD (§4.6) describes the long-term concurrency model: snapshot isolation
for readers, single writer, copy-on-write of meta. The Execution Plan (§4.6)
slices this delivery across phases:

| Phase | Reader/writer contract | Implementation |
|-------|------------------------|----------------|
| 0     | Reader–writer exclusion (RWMutex on DB) | Single `Update` mutex; `View` shares a read lock |
| 2     | True snapshot isolation (PRD §4.6 contract) | CoW over meta, page-version tracking |

The public API (`(*DB).View`, `(*DB).Update`) does not change between phases;
only the internals get stronger.

---

## Logging (Phase −1)

`internal/log.Logger(subsystem)` is the only entry point. Subsystems follow
the convention `kdb.<area>` and `pcke.<area>`. Levels are configured via:

- `PCKE_LOG_LEVEL` — global default, `info` if unset.
- `PCKE_LOG_LEVEL_<NORMALISED_SUBSYSTEM>` — per-subsystem override (e.g.
  `PCKE_LOG_LEVEL_KDB_WAL=debug`).

Attribute keys matching `(?i)(secret|token|key|password|credential)` are
replaced with `[REDACTED]` before output. This is defence-in-depth on top of
the path/content filters in `internal/analysis/secrets.go` (Phase 0).

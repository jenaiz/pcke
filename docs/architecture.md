# pcke — Architecture

This document records implementation-time decisions, build-tag conventions,
and operational notes for pcke.

---

## Component map

```
cmd/pcke/                  — CLI entry point (Cobra)
internal/log/              — slog factory; subsystem-tagged loggers + redaction
internal/config/           — TOML configuration loader
internal/kdb/              — embedded key-value storage engine
internal/kdb/page/         — page manager, checksum
internal/kdb/bufpool/      — buffer pool with clock-sweep eviction + adaptive sizing
internal/kdb/btree/        — B+tree engine (split, merge, cursor)
internal/kdb/wal/          — WAL writer, segments, recovery, group commit
internal/kdb/tx/           — transactions (ReadTx, WriteTx, group commit)
internal/kdb/freelist/     — B+tree freelist (page allocation)
internal/kdb/index/        — secondary indexes (by_module, by_tag, by_file, by_type)
internal/kdb/index/fts/    — full-text search (BM25, tokenizer, postings)
internal/kdb/query/        — internal query engine for kdb
internal/kdb/encoding/     — record encoding schema v1, varint, CRC32C
internal/kdb/diagnostics/  — Stats surface + pcke diagnostics support
internal/kdb/lock/         — flock + LOCK single-process guard
internal/kdb/migrate/      — schema migration engine (versioned, idempotent)
internal/kdb/testutil/     — crash simulation harness
internal/analysis/         — file-tree scan, go-git, secrets filter, heuristics
internal/analysis/ast/     — AST entity extraction (Go, Python, JS/TS, Java)
internal/analysis/annotations/ — in-code annotations (@pcke-rule, @pcke-lesson)
internal/output/           — Markdown context + agent instructions
internal/mcp/              — MCP server (stdio transport)
internal/query/            — query DSL (lexer, parser, planner, executor)
```

---

## Storage engine

### File layout

```
<repo>/.pcke/
  ├─ data.kdb            (main data file, grows in 16-page chunks)
  ├─ LOCK                (exclusive flock file)
  ├─ wal-00000001.log    (WAL segments, append-only)
  └─ wal-00000002.log
```

### Double-meta atomic swap

Pages 0 and 1 hold meta-A and meta-B. Each meta has a monotonically increasing
generation counter. Writes always target the inactive slot (lower generation).
On recovery, the meta with the highest valid CRC is selected.

### Buffer pool + adaptive sizing (Phase 4)

The buffer pool uses clock-sweep (second-chance) eviction. Phase 4 adds
`DynamicPool` which wraps the base pool with adaptive sizing:

- Tracks delta hit rate per sample window.
- Grows the pool when hit rate drops below 70% (low watermark).
- Shrinks the pool when hit rate exceeds 95% for 10+ consecutive samples.
- Bounded by configurable [MinPages, MaxPages] (defaults: 64–4096).

### Group commit (Phase 4)

WAL now supports `BatchAppend` which writes multiple records with a single
fsync. `WriteTx` in group-commit mode defers WAL writes until `Commit()`,
reducing sync overhead by ≥ 2x on workloads with many small writes per
transaction.

### Schema migrations (Phase 4)

The `internal/kdb/migrate` package provides a versioned, idempotent migration
engine. Schema version is tracked in the meta page. The `pcke migrate` command
applies pending migrations in order.

---

## Build tags

### `kdbdebug`

Activates two pieces of code that must never ship in releases:

1. **Invariant assertions** inside the buffer pool, B+tree, WAL, and transaction
   manager.
2. **Crash-injection hooks** at fixed sites. Reads `PCKE_CRASH_AT` env var;
   if it matches a hook name, calls `os.Exit(137)` to simulate `SIGKILL`.

Build instructions:

```bash
go test -tags=kdbdebug ./...
make test-debug
```

Releases must **not** carry the tag.

---

## Concurrency

| Phase | Reader/writer contract | Implementation |
|-------|------------------------|----------------|
| 0     | Reader–writer exclusion (RWMutex on DB) | Single `Update` mutex; `View` shares a read lock |
| 2     | True snapshot isolation (PRD §4.6 contract) | CoW over meta, independent reader buffer pools |

The public API (`(*DB).View`, `(*DB).Update`) does not change between phases.

---

## Logging

`internal/log.Logger(subsystem)` is the only entry point. Configuration:

- `PCKE_LOG_LEVEL` — global default, `info` if unset.
- `PCKE_LOG_LEVEL_<NORMALISED_SUBSYSTEM>` — per-subsystem override.

Attribute keys matching `(?i)(secret|token|key|password|credential)` are
replaced with `[REDACTED]`.

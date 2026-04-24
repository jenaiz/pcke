# PRD: Project Context & Knowledge Engine (PCKE)

> **Version:** 3.1
> **Status:** Draft — Engineering Review
> **Date:** April 2026
> **Authors:** Jesus Navarrete & AI-assisted
> **Changes vs. v3.0:** Heavy expansion of `kdb` (concurrency model, transactions, WAL rigor, buffer pool, overflow, freelist, observability). Added system-wide invariants, secrets/security section, macOS durability caveats, scan incrementality semantics, multi-branch handling. Resolved several open questions into decisions. Fixed inconsistencies (Phase 0 WAL, tags model, Phase 2 "branch-aware context", `relations` population).

---

## 1. Product Vision

### 1.1 Problem Statement

Every AI chat session starts from zero. An LLM working on a codebase has no memory of past architectural decisions, recurring bugs, business rules embedded in code, or the reasoning behind refactors. Developers waste tokens re-explaining context that already exists in the repository's code, commits, and tribal knowledge.

### 1.2 What PCKE Is

PCKE is a **Long-Term Engineering Memory** — a local system composed of two core components:

1. **`kdb`** — A purpose-built knowledge database engine, written from scratch in Go, optimized for storing and querying structured engineering knowledge.
2. **`pcke`** — A CLI + MCP application that extracts knowledge from codebases and serves it to AI agents and developers alike.

Together, they enable AI coding agents (GitHub Copilot, Claude Code) to operate with the context of a Senior Engineer who has years of project history — **without consuming a single token** for context retrieval.

### 1.3 What PCKE Is NOT

- **Not an LLM wrapper.** PCKE never calls an LLM. Zero token cost at runtime.
- **Not a SaaS.** It's a local binary. Your code never leaves your machine.
- **Not a wrapper over existing databases.** `kdb` is a custom storage engine built from the ground up — B+tree, inverted index, WAL, and query language included.

### 1.4 Key Differentiators

| Property | PCKE | Typical RAG tools |
|----------|------|-------------------|
| Token cost | Zero | Per-query embedding + LLM |
| External dependencies | None (single binary) | Vector DB, API keys, cloud services |
| Database | Custom-built `kdb` engine | Third-party (Postgres, Qdrant, Pinecone) |
| Privacy | Code never leaves local | Often requires external API calls |
| Setup | `pcke init` | DB install, API config, indexing pipeline |
| Query interface | CLI + Query language + MCP | API-only or GUI-only |

### 1.5 Competitive Landscape

Concrete tools in the adjacent space and how PCKE relates to each:

| Tool | What it does | How PCKE differs |
|------|--------------|------------------|
| `.cursorrules`, `.github/copilot-instructions.md` | Static text files read by agents | PCKE generates them *from* extracted knowledge; adds historical context and query interface |
| `repomix`, `aider --repo-map` | Flatten repo for LLM consumption | PCKE is persistent, queryable, and history-aware; not a flatten-on-demand tool |
| Sourcegraph Cody, Greptile | Cloud-hosted code intelligence | PCKE is local-only, no code leaves machine, no subscription |
| Cursor / Continue context engines | IDE-bundled RAG | PCKE is IDE-agnostic; exposes via MCP, usable by any agent |
| GitHub Copilot Workspace | GitHub-hosted, GitHub-specific | PCKE is self-hosted and works with any repo/any agent |

---

## 2. Design Principles

1. **Zero Token Cost** — PCKE never calls an LLM. All extraction is deterministic: AST parsing, Git analysis, heuristics, and developer input.
2. **Code as Truth** — The repository is the primary source of reality. All inferred knowledge must be traceable to actual code.
3. **History as Narrative** — Git history reveals *why* code evolved. PCKE mines commits, diffs, and change patterns to build temporal understanding.
4. **Developer as Oracle** — Code cannot express every decision. The developer provides rules, annotations, and lessons that augment automated extraction.
5. **Own Your Storage** — No third-party databases. `kdb` is built from scratch, giving full control over performance, format, and evolution.
6. **Single Binary, Zero Dependencies** — One executable. No PostgreSQL, no Docker, no runtime. Install and run.
7. **Durability before Features** — User writes (rules, notes) are never lost to a crash, even in early phases. See §4.6 and §8.
8. **Observable by Default** — `kdb` exposes internal state (page counts, WAL size, buffer pool hit rate) through diagnostic commands. Debugging a home-grown DB without introspection is impossible.

---

## 3. System Architecture

PCKE is two components in one binary:

```
┌─────────────────────────────────────────────────────────────────────┐
│                           pcke binary                                │
│                                                                     │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │                    Application Layer (pcke)                    │  │
│  │                                                               │  │
│  │  ┌─────────┐  ┌──────────┐  ┌─────────┐  ┌──────────────┐   │  │
│  │  │   CLI   │  │ Analysis │  │  Output  │  │  MCP Server  │   │  │
│  │  │ (Cobra) │  │  Engine  │  │  System  │  │   (stdio)    │   │  │
│  │  └────┬────┘  └────┬─────┘  └────┬────┘  └──────┬───────┘   │  │
│  │       │             │             │              │            │  │
│  │       └─────────────┴──────┬──────┴──────────────┘            │  │
│  └────────────────────────────┼──────────────────────────────────┘  │
│                               │                                     │
│  ┌────────────────────────────┼──────────────────────────────────┐  │
│  │                    Storage Layer (kdb)                         │  │
│  │                                                               │  │
│  │  ┌──────────────┐  ┌───────────────┐  ┌──────────────────┐   │  │
│  │  │ Query Engine │  │ Inverted Index│  │  B+Tree Engine   │   │  │
│  │  │ (parser +    │  │ (tokenizer +  │  │  (pages, nodes,  │   │  │
│  │  │  planner)    │  │  BM25 scorer) │  │   splits/merge)  │   │  │
│  │  └──────┬───────┘  └───────┬───────┘  └────────┬─────────┘   │  │
│  │         │                  │                    │             │  │
│  │         └──────────────────┴────────┬───────────┘             │  │
│  │                                     │                         │  │
│  │  ┌─────────────────────────────────────────────────────────┐  │  │
│  │  │        Transaction Manager + Buffer Pool + WAL           │  │  │
│  │  │           (LSN tracking, fsync, recovery)                │  │  │
│  │  └─────────────────────────────────────────────────────────┘  │  │
│  └───────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
```

### 3.1 Component Separation

| Layer | Package | Responsibility |
|-------|---------|---------------|
| `kdb` | `internal/kdb/` | Storage engine: B+tree, paging, WAL, inverted index, BM25, query parser, transactions |
| `pcke` | `cmd/`, `internal/analysis/`, `internal/output/`, `internal/mcp/` | Application: CLI, extraction, Markdown generation, MCP server |

`kdb` has **zero knowledge of PCKE's domain**. It's a generic embedded knowledge database that could theoretically be used by other projects. `pcke` uses `kdb` as its storage backend.

---

## 4. `kdb` — Knowledge Database Engine

### 4.1 Design Goals & Non-Goals

**Goals**

- **Embedded:** No separate process. Linked directly into the application.
- **Single-writer, concurrent-readers with snapshot isolation:** One writer transaction at a time; readers see a consistent snapshot regardless of concurrent writes.
- **Crash-safe:** WAL + page checksums ensure no silent data corruption on unexpected shutdown, process kill, or power loss (within the limits of the underlying filesystem — see §4.12).
- **ACID transactions** at the public API boundary, spanning multiple collections/indexes.
- **Full-text search:** Built-in inverted index with BM25 scoring.
- **Graph-aware:** First-class support for relationships between records.
- **Queryable:** Built-in query language for complex lookups.
- **Observable:** Diagnostic surface for page/WAL/buffer-pool state.

**Non-Goals (v1)**

- Multi-writer concurrency. Writes are serialized.
- Replication / cluster. Single-node only.
- Network protocol. `kdb` is a library.
- SQL compatibility. Custom query language only.
- Secondary indexes on arbitrary expressions. Only the fixed set in §4.9.
- Schema evolution via online ALTER. Migrations are rebuild-based (see §4.14).
- Hot backup. Backups are taken with the DB closed (or via filesystem snapshot).

### 4.2 System-Wide Invariants

These hold at all times, including immediately after crash recovery:

1. **Page integrity:** Every non-free page read from disk has a valid CRC32 checksum matching its header.
2. **Durability of committed transactions:** Any transaction whose commit record was `fsync`'d to the WAL is fully reflected in the DB after recovery.
3. **Atomicity:** A transaction is either fully applied or fully absent. No partial multi-page writes are observable.
4. **WAL ≥ data:** For any page version `P@LSN_p` persisted in a data file, all WAL entries with `LSN ≤ LSN_p` must be either checkpointed or still in the WAL. The active WAL is never deleted below the oldest dirty page's LSN.
5. **Reader isolation:** A reader transaction opened at LSN `L_r` observes exactly the committed state at `L_r`, regardless of concurrent writer activity.
6. **Index consistency:** For every record `R` returned by a primary B+tree scan, every inverted-index hit and secondary-index hit is consistent with `R` (no dangling index entries, no missing index entries).
7. **Freelist integrity:** Every page in the DB file is exactly one of: in use by a tree, overflow chain, or on the freelist. No leaks, no double-allocations.
8. **Meta atomicity:** The meta page is always readable as a valid snapshot; updates use a double-meta scheme (see §4.7).

These invariants are the acceptance criteria for the test suite (§9).

### 4.3 Storage Layout (On-Disk)

```
.pcke/
├── data/
│   ├── nodes.kdb            # B+tree: knowledge_nodes primary store
│   ├── evolution.kdb        # B+tree: evolution_logs
│   ├── constraints.kdb      # B+tree: engineering rules
│   ├── relations.kdb        # B+tree: graph edges
│   └── notes.kdb            # B+tree: developer annotations
├── index/
│   ├── fts/                 # Full-text search inverted index
│   │   ├── terms.kdb        # B+tree: term → term_id + posting list head
│   │   ├── postings.kdb     # Posting list segments (append-only)
│   │   └── fieldnorms.kdb   # B+tree: (doc_id, field_id) → length
│   └── secondary/
│       ├── by_module.kdb    # B+tree: module_name → node_id (duplicates allowed)
│       ├── by_file.kdb      # B+tree: file_path → node_id
│       ├── by_type.kdb      # B+tree: node_type → node_id
│       └── by_tag.kdb       # B+tree: tag → note_id
├── wal/
│   ├── 000001.wal           # Active WAL segment
│   └── ...                  # Rotated segments, truncated after checkpoint
├── meta.kdb                 # Double-meta page file (atomic commit point)
└── LOCK                     # flock + PID for single-process enforcement
```

**Single-process enforcement.** On `Open()`, `kdb` acquires an exclusive `flock` on `LOCK` and writes its PID. A second process fails fast with `ErrDBLocked`. Prevents accidental concurrent access from e.g. two CLI invocations.

**Endianness.** All integers on disk are **little-endian**, explicitly. All page/record encoders/decoders assert this regardless of host architecture.

### 4.4 Page Manager

The lowest level of `kdb`. All persistent data lives in fixed-size **pages**.

- **Page size:** 4096 bytes. Fixed for v1 (see Decisions §10 #4). Rationale: alignment with OS page size and SSD sectors; simplifies reasoning about torn writes.
- **Page types:** `Meta`, `Internal` (B+tree branch), `Leaf` (B+tree data), `Overflow`, `PostingSegment`, `Freelist`, `Free`.
- **Page header (24 bytes):**

```
┌──────┬──────┬──────────┬──────────┬──────────┬──────────┐
│ Magic│ Type │  Flags   │ Checksum │   LSN    │ Reserved │
│ (4 B)│ (1 B)│  (1 B)   │ (4 B)    │ (8 B)    │ (6 B)    │
└──────┴──────┴──────────┴──────────┴──────────┴──────────┘

Magic:    0x4B444250  ("KDBP")
Checksum: CRC32C of bytes [0..4] and [12..4096] with checksum field zeroed
LSN:      Log Sequence Number of the last write that modified this page
```

- **Usable data:** `4096 − 24 = 4072` bytes per page.
- **Free list:** See §4.8.
- **Buffer pool:** See §4.5.
- **File growth:** Allocate from freelist if available, otherwise append at EOF and extend file in chunks (16 pages at a time to reduce `ftruncate` overhead).

```go
type Page struct {
    ID       uint64
    Type     PageType
    Flags    uint8
    Checksum uint32
    LSN      uint64
    Data     [4072]byte
}
```

### 4.5 Buffer Pool

An in-memory cache of pages, owned by the page manager.

- **Size:** Configurable. Default: `min(256 MB, 25% of available RAM)`. Minimum: 64 pages (256 KB).
- **Replacement policy:** **Clock-sweep** (approximation of LRU), chosen over strict LRU to avoid a global mutex on every access. Each frame has a reference bit; the sweep hand clears the bit and only evicts frames whose bit is already 0.
- **Pinning:** Callers (B+tree traversal, WAL flush) pin frames to prevent eviction mid-operation. Pin count is atomic; cannot evict a frame with `pin_count > 0`.
- **Dirty page handling:** A dirty frame can only be written to disk **after** all WAL entries with LSN ≤ the frame's page LSN have been `fsync`'d. This enforces **WAL-before-data** (invariant #4).
- **Concurrency:** Per-frame RWMutex. Readers take read lock, writers take write lock. Buffer-pool metadata (frame table, clock hand) is protected by a separate mutex, held only during frame lookup/replacement.
- **Metrics surface:** Hit rate, pin conflicts, dirty-page count, eviction rate. Exposed via `pcke diagnostics` (see §4.13).

### 4.6 Transactions & Concurrency Model

The public contract of `kdb` is **transactional**. All operations — including multi-tree writes and index updates — execute inside a transaction.

**Public API:**

```go
db.View(func(tx *ReadTx) error { ... })       // Read-only, snapshot-isolated
db.Update(func(tx *WriteTx) error { ... })    // Read-write, serialized
```

**Concurrency semantics:**

- **Single active writer.** `db.Update` acquires a global writer mutex. Attempts to start a second writer block until the first commits or aborts.
- **Many concurrent readers.** `db.View` never blocks a writer and is never blocked by a writer.
- **Snapshot isolation for readers.** A `ReadTx` captures the current committed meta LSN on open. All page reads within the transaction resolve to the version visible at that LSN. Implementation: pages are copy-on-write within a write transaction; old versions remain reachable via the previous meta page's tree roots until all readers holding older snapshots release them.
- **Writer aborts.** If `Update` returns an error (or panics), the transaction is rolled back: no WAL commit record is written, in-memory dirty pages are discarded, and any newly allocated pages are returned to the freelist.
- **Durability on commit.** A successful `Update` guarantees its changes are durable before `Commit()` returns (group commit allowed — see §4.7).

**Rationale for the hybrid CoW + WAL design:** Pure CoW (Bolt-style) gives clean snapshot isolation and no WAL replay needed, but forces whole-tree copy-on-write which amplifies writes on small, frequent updates (exactly PCKE's pattern: many small index updates per scan). WAL + in-place updates (SQLite WAL-mode style) reduces write amplification for small transactions while still enabling concurrent readers via page-version tracking. `kdb` takes the WAL+in-place path for this reason. The trade-off is that recovery is more complex; that complexity is accepted and budgeted.

**Transaction lifecycle (write):**

```
Update(fn):
  1. Acquire writer mutex
  2. Begin: allocate transaction LSN, capture undo log
  3. fn(tx) runs, calling Put/Delete on trees and indexes
     → each mutation: WAL-log entry appended; dirty page staged in buffer pool
  4. Commit:
       a. Append COMMIT record with LSN
       b. fsync WAL (F_FULLFSYNC on macOS)
       c. Update the pending-commit meta fields in memory
       d. Atomically swap active meta page (see §4.7)
  5. Release writer mutex
```

**Atomicity across trees.** Because all mutations during a transaction are logged to the same WAL stream and a single COMMIT record gates visibility, a multi-tree transaction is atomic: either the COMMIT record is durably persisted (and recovery replays all its entries) or not (and recovery discards the partial prefix).

### 4.7 Write-Ahead Log (WAL)

Provides atomicity and durability. Uses **LSN-based physical-logical logging** (a small subset of ARIES).

**LSN (Log Sequence Number).** Monotonically increasing 64-bit counter. Every WAL record and every page write carries an LSN. On recovery, replay only entries with `LSN > last_checkpoint_lsn`.

**WAL record types:**

| Type | Purpose |
|------|---------|
| `BEGIN` | Marks start of a transaction |
| `PUT` | Record inserted/updated: `(tree_id, key, value)` |
| `DELETE` | Record deleted: `(tree_id, key)` |
| `PAGE_ALLOC` / `PAGE_FREE` | Freelist mutation |
| `COMMIT` | Durable commit point for a transaction |
| `ABORT` | Transaction aborted (rarely written; usually just absence of COMMIT) |
| `CHECKPOINT_BEGIN` / `CHECKPOINT_END` | Bracket a fuzzy checkpoint |

**Record format:**

```
┌──────┬──────┬──────┬──────┬──────────┬──────────┬──────────┬──────────┐
│Magic │ LSN  │ TxID │ Type │ PrevLSN  │ Length   │ Payload  │ CRC32C   │
│(2 B) │(8 B) │(8 B) │(1 B) │ (8 B)    │ (4 B)    │ (var)    │ (4 B)    │
└──────┴──────┴──────┴──────┴──────────┴──────────┴──────────┴──────────┘
```

- `PrevLSN` chains records of the same transaction for rollback.
- `CRC32C` covers everything from `Magic` to end of `Payload`; a corrupt/torn tail record is detected and ignored during recovery.

**Segmentation.** WAL is a sequence of files (`000001.wal`, `000002.wal`, …), each up to **16 MB** by default. Rotated when full. Old segments are deleted after a checkpoint confirms all their records are applied to data files.

**Group commit.** The writer can batch multiple logical operations into one `fsync`. Within a single `Update` closure, all mutations share one commit fsync — there is no per-mutation fsync. Cross-transaction group commit is **not** implemented in v1 (only one writer at a time; no batching needed).

**Checkpointing.** Fuzzy checkpoint, triggered by any of:

- WAL size exceeds 32 MB (tunable)
- Time since last checkpoint exceeds 60 s (tunable)
- `db.Checkpoint()` called explicitly

Checkpoint steps:

1. Write `CHECKPOINT_BEGIN` with a list of currently-dirty pages (the *dirty page table*).
2. Flush all dirty pages to data files (respecting WAL-before-data).
3. `fsync` data files.
4. Write `CHECKPOINT_END` with `oldest_dirty_lsn` = min LSN among still-dirty pages (or `COMMIT` LSN if none).
5. `fsync` WAL.
6. Update meta page's `last_checkpoint_lsn`; swap meta.
7. Delete WAL segments whose records are all `< last_checkpoint_lsn`.

**Recovery (startup):**

1. Read meta page; obtain `last_checkpoint_lsn`.
2. Scan WAL from the oldest segment containing `last_checkpoint_lsn`.
3. **Analysis pass:** identify committed vs. uncommitted transactions (COMMIT records present or absent).
4. **Redo pass:** for every record of a committed transaction with `LSN > page_lsn` of the target page, re-apply.
5. **Undo pass:** for every record of an uncommitted transaction, follow `PrevLSN` chain and revert.
6. Recovery completes when all WAL is consumed; write a `CHECKPOINT_END` to mark the new clean state.

**Torn-write defense.** If a 4 KB page write is torn (partially persisted), the page's CRC32C will fail on next read. The recovery/read path detects this and fetches the latest version from the WAL redo log. Combined with per-page LSNs, this yields correct recovery without full-page-writes-to-WAL (a simpler approach than PostgreSQL's `full_page_writes`, at the cost of requiring WAL to retain *all* page mutations since last checkpoint).

**Meta page atomicity.** The meta file holds **two** meta pages, `meta_a` and `meta_b`, at fixed offsets 0 and 4096. Each has a generation counter and checksum. On commit, the writer writes the inactive slot, `fsync`s, then updates the "active generation" pointer (itself a single 8-byte atomic write at offset 8192). Readers always pick the meta page with the higher valid generation. This guarantees atomic meta swap without relying on 4 KB page atomicity.

### 4.8 B+Tree Engine

**Node layout:**

- **Internal node:** Sorted list of `(key, child_page_id)` pairs + rightmost child pointer.
- **Leaf node:** Sorted list of `(key, value_or_overflow_ref)` pairs + `next_leaf_page_id` and `prev_leaf_page_id` pointers for range scans.
- **Key storage:** Variable-length, stored inline up to a threshold. If `key_len > 1/4 of page usable space`, the key goes to an overflow page and the node stores an 8-byte overflow reference. This bounds the minimum fanout.
- **Value storage:** Inline if `value_len ≤ 1/4 usable space`; otherwise overflow chain (see §4.10).
- **Fanout target:** ≥ 50 keys per internal node under typical PCKE key sizes (most keys are `uint64` IDs or short strings, 8–32 bytes). Max node capacity is page-usable-space bound, not a fixed constant.

**Key types.** All keys are opaque byte slices. The tree does not interpret them. Callers (schema layer in `pcke`) encode typed keys as lexicographic-sortable byte sequences:

- `uint64` → big-endian 8 bytes (sort matches numeric order)
- `string` → UTF-8 bytes, optionally prefixed with length for composite keys
- Composite → field-separator `0x00`, with escaping for embedded nulls

**Operations:**

| Operation | Complexity | Notes |
|-----------|-----------|-------|
| `Get(key)` | O(log n) | Point lookup via descending traversal |
| `Put(key, value)` | O(log n) | Insert or update; splits bubble up to root |
| `Delete(key)` | O(log n) | Removal; merges/redistributes if underflow |
| `Range(lo, hi)` | O(log n + k) | Seek to `lo`, walk leaves via `next_leaf_page_id` |
| `Scan(prefix)` | O(log n + k) | Seek to prefix, walk until key no longer matches |

**Duplicates.** Primary trees disallow duplicates (key = record ID, unique). Secondary indexes allow duplicates by encoding `(index_key, primary_key)` as the composite B+tree key with empty value. This naturally sorts duplicate entries and allows efficient range scans per `index_key`.

**Splits.** Triggered when inserting into a full node.

- **Default split:** 50/50 by byte count of entries.
- **Monotonic-insert heuristic:** If the insert position is the rightmost slot, use a 90/10 split instead. Matches the `auto_increment uint64` primary-key pattern (common in PCKE) and reduces subsequent splits.

**Merges and redistribution.** On delete, if a node falls below `merge_threshold = 1/3 usable space`:

1. Attempt to redistribute with the left or right sibling if either has > `merge_threshold + epsilon` fill.
2. Otherwise, merge with a sibling.
3. If the root becomes a single internal node with one child, collapse it.

**Cursor stability.** A `Cursor` is a typed iterator opened within a transaction. Within a read transaction, cursors see the snapshot at transaction start; concurrent writer activity has no effect on them (CoW preserves old versions). Within a write transaction, cursor invalidation after modifications is the caller's responsibility (documented).

### 4.9 Secondary Indexes

B+tree indexes on specific fields. Maintained **inside the same transaction** as the primary write — no async indexing, no eventual consistency.

| Index | Key encoding | Duplicates |
|-------|-------------|------------|
| `by_module` | `module_name \0 node_id` | Yes |
| `by_file` | `file_path \0 node_id` | Yes |
| `by_type` | `node_type \0 node_id` | Yes |
| `by_tag` | `tag \0 note_id` | Yes |

**Tag model fix (vs. v3.0).** In v3.0 `developer_notes.tags` was a comma-separated string while `by_tag` claimed to index per-tag. v3.1 resolves: `tags` is stored as a sorted `[]string` in the record (encoded with a length-prefixed repeat). On write, the index maintainer splits and inserts one `by_tag` entry per tag. `note list --tag=X` uses the index directly.

### 4.10 Overflow Pages

For values (or keys) larger than a quarter of a page, data is stored in a singly-linked chain of `Overflow` pages.

```
Leaf entry value = OverflowRef { first_page: uint64, total_len: uint32 }

Overflow page layout:
┌──────────────┬─────────────────────────────────────────────────┐
│ next_page_id │                   data (up to 4064 B)            │
│   (8 B)      │                                                  │
└──────────────┴─────────────────────────────────────────────────┘
```

- Last page in chain has `next_page_id = 0`.
- Overflow pages are freed (added to freelist) when the owning record is deleted or updated to a smaller inline value.
- **Hard limit on value size in v1: 16 MB.** Values beyond this are rejected with `ErrValueTooLarge`. Rationale: keeps recovery tractable and prevents a single record from dominating a checkpoint.

### 4.11 Freelist

Tracks pages available for reuse.

- **Structure:** B+tree keyed by `page_id` with empty value, stored in a reserved tree in the meta page. Rationale vs. bitmap/linked-list:
  - Bitmap doesn't scale cleanly as file grows beyond millions of pages.
  - Linked-list loses locality and requires reading a page to get the next free page.
  - B+tree supports efficient range allocation (pre-allocate N contiguous pages when growing) and persists naturally inside the transaction.
- **Allocation inside a transaction:**
  - `alloc_page()` — pop smallest free page ID; if none, extend file by a chunk (16 pages) and push 15, return 1.
  - `free_page(id)` — push into the freelist tree.
- **Transaction rollback:** freelist mutations in an aborted transaction are discarded with the rest. Page allocations made in the aborted tx are released back to the freelist on abort.

### 4.12 Durability & Filesystem Caveats

- **macOS:** Plain `fsync(2)` does **not** flush drive cache. `kdb` uses `fcntl(fd, F_FULLFSYNC)` on darwin. Falls back to `fsync` if `F_FULLFSYNC` returns `ENOTSUP` (network filesystems), with a warning logged.
- **Linux:** `fsync(2)` is sufficient on reliable hardware. On EXT4, `data=ordered` (default) gives the required ordering between metadata and data.
- **Windows:** `FlushFileBuffers` is used. Not a primary target platform in v1 but CI builds it.
- **Network filesystems (NFS, SMB):** Detected on open; `kdb` issues a warning. Durability guarantees are reduced to whatever the filesystem provides.
- **Direct I/O:** Not used in v1. The OS page cache is tolerated; buffer pool sits on top of it. Revisit if benchmarks show double-buffering pain.

### 4.13 Observability & Diagnostics

First-class. Exposed through `pcke diagnostics` (subcommand) and as Go API on `*DB`:

```go
type Stats struct {
    // Storage
    DataFileBytes     map[string]int64   // per tree file
    PageCount         uint64
    FreePageCount     uint64
    OverflowChains    uint64
    // B+tree
    TreeDepths        map[string]int
    KeysPerTree       map[string]uint64
    // WAL
    WALSegments       int
    WALTotalBytes     int64
    LastCheckpointLSN uint64
    ActiveLSN         uint64
    // Buffer pool
    BufferPoolSize    int
    BufferHitRate     float64
    DirtyPages        int
    PinnedPages       int
    // FTS
    TermCount         uint64
    PostingBytes      int64
}
```

Commands:

- `pcke diagnostics` — human-readable summary.
- `pcke diagnostics --format=json` — machine-readable.
- `pcke diagnostics --pages <tree>` — per-page breakdown for debugging.
- `pcke diagnostics --wal` — WAL segment listing with LSN ranges.
- `pcke explain <query>` — query planner trace (see §4.15).

All commands are read-only and open the DB in read-mode (no lock conflict with a running writer — but conflicts with another process holding the exclusive lock).

### 4.14 Record Encoding & Schema Versioning

**Record format:**

```
┌─────────┬──────────┬─────────────────────────────────────┐
│ Version │ Field    │ Fields...                           │
│ (1 byte)│ Count    │ (tag + length + value) × N         │
│         │ (1 byte) │                                     │
└─────────┴──────────┴─────────────────────────────────────┘
```

- **Version** is per-collection schema version. The schema registry in the meta page stores `{collection_name → current_version}`.
- **Field tag byte** encodes both type (`uint64`, `int64`, `float64`, `string`, `bytes`, `bool`, `timestamp`, `list<T>`) and field ID. Unknown tags on read are skipped (forward compatibility within a version).
- **Length:** varint for `string`/`bytes`/`list`; implicit for fixed-size types.

**Schema evolution.**

- **Additive changes** (new field, new optional field): no version bump needed; decoders skip unknown tags.
- **Breaking changes** (rename, type change, field removal): bump collection version. `pcke migrate` re-reads all records through the old decoder and writes them through the new encoder in a single transaction (or multiple, chunked). No online ALTER.
- **Meta page holds** `kdb_format_version` (engine format) and per-collection `schema_version`. Engine refuses to open a DB written by a newer `kdb_format_version`.

### 4.15 Inverted Index (Full-Text Search)

**Model.** Per-segment immutable posting lists (Lucene-style) combined with a live *in-memory segment* for the current write transaction, flushed at commit.

**Rationale over "in-place mutable posting lists":** posting lists are typically append-heavy; mutable posting lists in a B+tree cause write amplification and fragmentation. Immutable segments + periodic merging (background, during checkpoints) keep writes cheap and reads sequential.

**Indexing pipeline:**

```
Record → Tokenizer → Normalizer → InMemorySegment
                                      ↓ on commit
                                  Flushed segment on disk
                                      ↓ periodically
                                  Merged into larger segment
```

**Tokenizer.** Unicode word-boundary aware (uses Go's `unicode.IsLetter`, `IsDigit`; does **not** pull in full ICU in v1). Additionally splits `camelCase` and `snake_case` to surface identifier components as searchable terms (emits both the whole and the parts).

**Scope in v1:** Latin scripts fully supported. CJK scripts produce per-codepoint tokens (functional but suboptimal). Full ICU segmentation is a post-v1 consideration.

**Normalizer.** Lowercase + Unicode NFKC + ASCII folding for Latin. No stemming (code terms don't stem well).

**BM25 scoring.**

$$score(D, Q) = \sum_{i=1}^{n} IDF(q_i) \cdot \frac{f(q_i, D) \cdot (k_1 + 1)}{f(q_i, D) + k_1 \cdot (1 - b + b \cdot \frac{|D|}{avgdl})}$$

With $k_1 = 1.2$, $b = 0.75$, $IDF(q_i) = \ln\left(\frac{N - n(q_i) + 0.5}{n(q_i) + 0.5} + 1\right)$.

**Segments on disk.** Each flushed segment is a set of files under `index/fts/segments/<seg_id>/`:

- `terms.kdb` — B+tree: `term → posting_list_ref`
- `postings.bin` — sequential posting lists, VarInt + delta-encoded doc IDs, gamma-encoded term frequencies
- `norms.kdb` — B+tree: `doc_id → field_length[]`
- `meta` — doc count, total length, global stats

**Deletions.** Tombstone-based. A per-segment `deleted.bitmap` records deleted doc IDs; readers filter them. On merge, deleted docs are dropped from the merged segment.

**Merge policy.** Tiered: whenever a segment count exceeds 10 at a size tier, merge those into a next-tier segment. Merges happen at checkpoint boundaries to share fsync cost.

**Consistency.** The inverted index is updated in the **same transaction** as the primary B+tree write (invariant #6). Readers see the index state corresponding to their snapshot LSN; new segments become visible only after COMMIT + meta swap publishes their segment list.

**Indexed fields per collection:**

| Collection | Indexed Fields |
|-----------|---------------|
| `knowledge_nodes` | `name`, `summary`, `content` |
| `constraints` | `rule_text`, `reason` |
| `developer_notes` | `content`, `tags` (each tag a separate token) |

### 4.16 Query Language

A simple, purpose-built query language for power users.

**Grammar (EBNF):**

```ebnf
query       = collection [where_clause] [order_clause] [limit_clause]
collection  = "nodes" | "evolution" | "constraints" | "notes" | "relations"
where_clause = "where" condition { ("and" | "or") condition }
condition   = field operator value
operator    = "=" | "!=" | ">" | "<" | ">=" | "<=" | "contains" | "matches"
order_clause = "order" "by" field ["asc" | "desc"]
limit_clause = "limit" integer
field       = identifier { "." identifier }
value       = string_literal | number_literal | boolean_literal
```

**Planner.** AST → logical plan → index selection:

- Equality on an indexed field → index seek.
- Range on a sortable indexed field → index range scan.
- Otherwise → full collection scan with post-filter.
- `and` intersects index results; `or` unions.
- `order by` prefers an index with matching sort; otherwise materializes + sorts.

`pcke explain <query>` prints the chosen plan for debugging (§4.13).

**Examples:**

```
nodes where type = 'module' and stability > 0.7
nodes where module = 'api' order by updated_at desc limit 10
constraints where scope = 'global' and severity = 'must'
evolution where author = 'jesus' and change_type = 'refactored'
notes where tags contains 'decision'
```

---

## 5. `pcke` — Application Layer

### 5.1 Technology Stack

| Component | Technology | Rationale |
|-----------|-----------|-----------|
| Language | **Go** | Single binary, strong concurrency, mature ecosystem |
| CLI framework | **Cobra** (spf13/cobra) | Industry standard |
| Git analysis | **go-git** (go-git/go-git) | Pure Go, no `git` binary dependency |
| AST parsing | **tree-sitter** (smacker/go-tree-sitter) | Multi-language AST, 25+ grammars |
| Storage | **kdb** (custom, in-tree) | Purpose-built knowledge DB |
| MCP server | **mcp-go** (mark3labs/mcp-go) | Mature Go MCP implementation; stdio transport |

### 5.2 Data Model

The logical schema that `pcke` stores in `kdb`.

**Collection: `knowledge_nodes`**

```
{
  id:           uint64       (auto-increment)
  type:         string       (module | function | pattern | rule | lesson)
  name:         string       (human-readable identifier)
  summary:      string       (one-line description)
  content:      string       (full context/documentation)
  file_path:    string       (primary source file, if applicable)
  language:     string       (go | typescript | python | rust | ...)
  module:       string       (parent module name)
  stability:    float64      (0.0 volatile → 1.0 stable)
  status:       string       (active | legacy | deleted)
  created_at:   timestamp
  updated_at:   timestamp
  content_hash: bytes        (sha256 of source material; used to detect no-op re-indexing)
}
```

**Collection: `evolution_logs`**

```
{
  id:           uint64
  node_id:      uint64       (FK knowledge_nodes)
  commit_hash:  string
  change_type:  string       (created | modified | refactored | deleted | renamed)
  description:  string
  diff_summary: string
  author:       string
  committed_at: timestamp
}
```

**Collection: `constraints`**

```
{
  id:           uint64
  scope:        string       (global | module:<name> | file:<path>)
  rule_text:    string
  reason:       string
  source:       string       (auto | manual)
  severity:     string       (must | should | must_not)
  created_at:   timestamp
}
```

**Collection: `relations`**

```
{
  id:             uint64
  source_node_id: uint64
  target_node_id: uint64
  type:           string     (depends_on | implements | replaced_by | relates_to | imports)
  source:         string     (auto | manual)
  created_at:     timestamp
}
```

**Collection: `developer_notes`**

```
{
  id:           uint64
  node_id:      uint64       (nullable)
  content:      string
  tags:         list<string> (sorted; each tag is a separate indexed token)
  created_at:   timestamp
}
```

**Data lifecycle decisions (resolved from v3.0 ambiguities):**

- **`stability` recomputation:** Computed on `scan`; stored on the node. Included in the inverted index `content` field only when surfaced in `summary`, not as a standalone numeric field (avoids churning FTS postings on every scan). For query filtering, `stability` is queryable via the query language path (§4.16), not FTS.
- **`status = legacy`:** Set by heuristic during `scan` when a node's file has not been touched in ≥ 365 days *and* no other node depends on it; can be overridden manually.
- **`status = deleted`:** Soft-delete marker used when a file is removed. Kept for history coherence; GC'd by `pcke compact --prune-deleted`.
- **Rename detection:** `go-git`'s rename detection is used during `scan`; produces a `renamed` evolution log entry and updates the node's `file_path` rather than creating a new node.

### 5.3 Scan Semantics: Incremental vs. Full

`pcke scan` is **incremental by default**, full when forced.

**Incremental scan:**

1. Read `last_scan_commit` from meta (the commit hash at last successful scan).
2. Ask `go-git` for files changed between `last_scan_commit` and `HEAD` (plus working-tree uncommitted changes).
3. Recompute nodes only for changed files.
4. For each changed file, compute `content_hash`; skip re-indexing if unchanged (guards against content-preserving edits like whitespace).
5. Update git-derived metrics (change frequency, stability) for the touched modules only.
6. Persist new `last_scan_commit = HEAD` on success.

**Full scan (`pcke scan --full`):** Scans all tracked files; rebuilds all computed fields. Required after:

- Upgrading `kdb`/`pcke` to a version with a new schema.
- After `pcke compact --prune-deleted`.
- First scan on a repo (implicit).

**File deletion handling.** When `go-git` reports a delete, the corresponding node transitions to `status = deleted` with an evolution log entry.

### 5.4 Multi-Branch Strategy

- **Default:** A single `.pcke/` per repo, keyed to the currently checked-out branch at scan time. `pcke status` reports which branch the DB currently reflects.
- **Branch switch detection.** On any `pcke` command, compare `HEAD` to `meta.last_scan_commit`. If they differ significantly (different branches, divergent history), emit a warning: *"DB reflects branch X; currently on Y. Run `pcke scan` to refresh."*
- **Multi-branch mode (post-v1).** Optional `.pcke/branches/<branch>/` layout for teams that need per-branch knowledge. Explicitly out of scope for v1.0.

### 5.5 Analysis Engine

All extraction is deterministic — no LLM calls.

#### 5.5.1 File Tree Scanner (Phase 0)

- Directory structure → module detection.
- Language detection by file extension.
- File classification by path heuristics:
  - `*_test.go`, `*.spec.ts` → Tests
  - `**/cmd/**`, `**/cli/**` → Entry points
  - `**/api/**`, `**/routes/**` → API layer
  - `**/models/**`, `**/entities/**` → Data layer
  - `Dockerfile`, `*.tf` → Infrastructure

#### 5.5.2 Git Intelligence (Phase 0)

Powered by `go-git`:

- **Change frequency** → volatile vs. stable modules.
- **Coupling detection** → files that change together within a sliding window of commits; recorded as `relations(type=relates_to, source=auto)`.
- **Stability scoring:** `1 − (changes_last_90d / max(total_changes, 1))`, clamped to `[0, 1]`.
- **Conventional commit parsing:** `feat:`, `fix:`, `refactor:`, `breaking:` → mapped to `change_type`.
- **Authorship map** → who owns what module.

#### 5.5.3 AST Structural Analysis (Phase 2)

Powered by `tree-sitter`:

- Entity extraction: functions, classes, interfaces, structs → `knowledge_nodes`.
- Import/dependency mapping → `relations(type=imports, source=auto)`. **This is the primary populator of the `relations` collection.**
- Pattern recognition heuristics (controllers, models, services, middleware).
- Export surface detection (public API per module).

#### 5.5.4 Developer Annotations (Phase 0+)

**Via CLI:**

```bash
pcke rule add "Never use raw SQL in controller layer" --scope=module:api --severity=must
pcke note "Migrated from Redis to Valkey due to license change" --tag=decision,migration
pcke note "Session cache TTL is 24h — GDPR requirement" --file=src/cache/session.go
```

**Via in-code annotations (Phase 3):**

```go
// @pcke-rule: must validate JWT before any database access
// @pcke-lesson: connection pooling with pgbouncer broke transactions
```

#### 5.5.5 Secrets Filtering (Phase 0)

A required pre-index filter. PCKE must not store secrets in its knowledge base.

- **Path-based exclusion:** `.env*`, `*.pem`, `*.key`, `**/secrets/**`, `**/*_secret*` (defaults; configurable).
- **Content-based redaction:** Common regexes for AWS keys (`AKIA[0-9A-Z]{16}`), generic high-entropy strings ≥ 32 chars in identifiers matching `*key*`, `*token*`, `*secret*`. Redacted tokens replaced with `[REDACTED]` before indexing.
- **`.gitignore` respect:** Files ignored by git are never scanned unless explicitly included via `pcke config scan.include_ignored`.
- **MCP disclosure:** The MCP server's `recall` / `query` responses pass through the same redaction filter as a defense in depth, even though stored data is already sanitized.
- **Opt-out:** Users can disable filtering via `pcke config scan.redact_secrets=false` (documented as not recommended).

### 5.6 Output System

#### 5.6.1 Markdown Context Directory (`.context/`)

`pcke sync` generates:

```
.context/
├── ARCHITECTURE.md         # Module map, dependency graph, tech stack
├── CONVENTIONS.md          # Detected patterns, naming conventions
├── HISTORY.md              # Timeline of significant changes
├── DECISIONS.md            # Developer notes, lessons learned
├── CONSTRAINTS.md          # Engineering rules (must/must-not/should)
└── MODULES/
    ├── api.md
    ├── database.md
    └── ...
```

Auto-generates agent instruction files:

- `.github/copilot-instructions.md` — GitHub Copilot
- `.claude/CLAUDE.md` — Claude Code

#### 5.6.2 MCP Server (`pcke serve`)

Transport: **stdio**.

**Tools:**

| Tool | Parameters | Returns |
|------|-----------|---------|
| `recall` | `query: string, limit?: int` | BM25-ranked results |
| `get_module_context` | `module: string` | Module summary, files, deps, history, constraints |
| `get_constraints` | `scope?: string` | Rules applicable to scope |
| `get_history` | `file_path: string` | Evolution timeline |
| `query` | `query_string: string` | Direct `kdb` query (Phase 3) |

**Resources:**

| URI | Description |
|-----|-------------|
| `pcke://architecture` | Full architecture map |
| `pcke://constraints` | All engineering rules |
| `pcke://decisions` | Developer decisions and lessons |

**Safety.** The MCP server is read-only. Writes are never exposed via MCP; only `pcke` CLI commands can mutate the knowledge base.

---

## 6. CLI Specification

```
pcke — Project Context & Knowledge Engine

Usage:
  pcke [command]

Core Commands:
  init                       Initialize PCKE in the current repository
  scan [--full] [--deep]     Analyze project; incremental by default
  sync                       Regenerate .context/ from knowledge base
  recall <query>             BM25 full-text search
  query <expression>         Execute a kdb query

Knowledge Commands:
  rule add <text>            Add an engineering constraint
  rule list                  List constraints
  rule remove <id>           Remove a constraint
  note <text>                Add a developer note
  note list                  List notes

Inspection Commands:
  status                     Health, branch, last scan, counts
  modules                    Detected modules with stability scores
  diagnostics [--pages|--wal|--format=json]
                             kdb internals introspection
  explain <query>            Print query planner trace

Maintenance Commands:
  compact [--prune-deleted]  Reclaim space, GC soft-deleted records
  migrate                    Run schema migrations

Server Commands:
  serve                      Start MCP server (stdio transport)

Export Commands:
  export [--format=json|yaml]

Flags:
  --scope <scope>            Scope for rules: global, module:<name>, file:<path>
  --severity <level>         must, should, must_not
  --tag <tags>               Comma-separated tags for notes
  --file <path>              Associate a note with a file
  --format <fmt>             Output format: text (default), json
  -v, --verbose              Verbose output
  --version                  Print version
```

### 6.1 Command Phasing

| Command | Phase |
|---------|-------|
| `pcke init` | 0 |
| `pcke scan` (incremental + full) | 0 |
| `pcke sync` | 0 |
| `pcke rule *`, `pcke note *` | 0 |
| `pcke status`, `pcke modules` | 0 |
| `pcke diagnostics` | 0 |
| `pcke recall` | 1 |
| `pcke scan --deep` | 2 |
| `pcke serve` | 2 |
| `pcke compact`, `pcke migrate` | 2 |
| `pcke query`, `pcke explain`, `pcke export` | 3 |

---

## 7. User Journey

### Setup (Day 1)

```bash
go install github.com/jesusnavarrete/pcke@latest

cd my-project
pcke init
pcke scan
pcke sync
```

### Add Knowledge

```bash
pcke rule add "All API endpoints must validate JWT before DB access" \
  --scope=module:api --severity=must
pcke note "Migrated from Express to Fastify Q3 2025 for perf." \
  --tag=decision,migration
pcke sync
```

### Query

```bash
pcke recall "auth system changes"
pcke query "nodes where module = 'api' and stability > 0.7 order by updated_at desc"
pcke diagnostics          # inspect kdb internals
pcke explain "nodes where module = 'api'"
```

---

## 8. Development Phases

Durability of user-authored data (rules, notes) is non-negotiable from Phase 0. The old v3.0 "no WAL in Phase 0, expect some loss" is replaced by a minimal but correct durability path from day one.

### Phase 0 — Foundation (durable)

**Goal:** B+tree storage with crash-safe user writes + CLI with basic scan/sync.

| Component | Deliverable |
|-----------|------------|
| **kdb** | Page manager (4 KB, CRC32C, LSN). Double-meta atomic swap. Freelist (B+tree-based). B+tree (put/get/delete/range, variable-length keys, overflow pages). Record encoding (schema v1). **Minimal WAL**: serial append + fsync/F_FULLFSYNC per transaction, no checkpointing yet (WAL grows; truncated only on clean shutdown). Transaction API (`View`/`Update`). `flock` single-process guard. Diagnostics (`Stats`). |
| **pcke** | CLI (Cobra). `init`, `scan` (file tree + git log, incremental), `sync`, `rule add/list/remove`, `note/note list`, `status`, `modules`, `diagnostics`. Secrets filtering. |

**Testing:** B+tree property tests (insert/delete sequences preserve invariants). Crash simulation: kill process at random points, verify (a) committed transactions survive, (b) uncommitted transactions are absent, (c) no CRC errors on reopen.

### Phase 1 — Search & Checkpointing

**Goal:** FTS with BM25 + checkpointing so WAL doesn't grow unbounded.

| Component | Deliverable |
|-----------|------------|
| **kdb** | Fuzzy checkpointing with LSN tracking. WAL segment rotation + truncation. Inverted index (in-memory segment + flushed segments + tombstones). BM25 scorer. Segment merge on checkpoint. |
| **pcke** | `pcke recall` powered by BM25. Index text fields on scan (incremental). |

**Testing:** Precision@5 suite (≥ 50 labeled queries). BM25 parity with a reference implementation (Lucene via test fixtures). Long-running soak test: continuous scans + crashes, verify no index/data drift.

### Phase 2 — Deep Analysis & MCP

**Goal:** AST + MCP server + secondary indexes + compaction.

| Component | Deliverable |
|-----------|------------|
| **kdb** | Secondary indexes (by_module, by_file, by_type, by_tag). `Compact` operation (reclaim fragmentation, optionally prune soft-deleted). Snapshot-isolated read transactions (CoW over meta page). |
| **pcke** | `pcke scan --deep` (tree-sitter). `pcke serve` (MCP stdio). `pcke compact`. Branch mismatch warnings. |

**Testing:** Snapshot isolation tests (reader during concurrent writes). MCP integration with Claude Code. AST extraction accuracy on reference repos.

### Phase 3 — Query Language & Polish

**Goal:** Query DSL + annotations + export.

| Component | Deliverable |
|-----------|------------|
| **kdb** | Query parser (recursive descent). Planner (index selection). Executor. `EXPLAIN`. |
| **pcke** | `pcke query`, `pcke explain`, `pcke export`. In-code annotation parsing. Benchmark suite (1K / 10K / 100K files). |

**Testing:** Parser correctness (AST snapshot tests). Planner picks optimal index on synthetic workloads. E2E perf regression gate.

### Phase 4 — v1.0

| Component | Deliverable |
|-----------|------------|
| **kdb** | Buffer pool tuning. Group-commit optimization. Concurrent reader perf. |
| **pcke** | Schema migration tooling (`pcke migrate`). Multi-repo support (shared DBs). Comprehensive docs. |

---

## 9. Validation & Quality Metrics

| Metric | How to measure | Target |
|--------|---------------|--------|
| **B+tree invariants** | Property-based tests (gopter/rapid): random op sequences preserve sort, balance, no orphan pages | 100% |
| **Crash safety** | Chaos test: kill at every WAL stage × every operation type; verify invariants §4.2 | Zero corruption |
| **Recovery correctness** | Committed-tx survival rate after injected crash | 100% |
| **Snapshot isolation** | Readers during concurrent writes never see torn/partial state | 100% |
| **FTS Precision@5** | 50 labeled queries (ground truth curated per PCKE self-scan) | ≥ 70% |
| **FTS consistency** | No dangling postings, no missing postings after 10k random mutations | 100% |
| **Context Coverage** | % project modules with at least one knowledge node | ≥ 80% |
| **Scan Performance (incremental, no changes)** | `pcke scan` on 10K files with no diff | < 500 ms |
| **Scan Performance (full, cold)** | `pcke scan --full` on 10K files | < 10 s |
| **FTS Query Latency** | p99 `pcke recall` on 10K-node DB | < 50 ms |
| **Binary Size** | Release build, stripped | < 30 MB |
| **Memory Usage** | Peak RSS during full scan of 10K files | < 200 MB |
| **Buffer pool hit rate** | During steady-state workload | > 90% |

**Benchmark baselines.** Compare against bbolt (CoW B+tree, Go), BadgerDB (LSM, Go), and SQLite (WAL mode) on: point-read latency, range-scan throughput, write throughput at 1K/10K/100K records, on-disk size for identical dataset.

---

## 10. Design Decisions & Rationale

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | **No LLM usage** | Zero token cost is the core differentiator. |
| 2 | **Custom DB (kdb) from scratch** | Goal in itself: full control over format, learning value, no vendor lock-in. Product differentiation is a side benefit. |
| 3 | **B+tree as primary structure** | Ordered data, range scans, disk-friendly. Well-documented. |
| 4 | **4 KB fixed pages** | OS/SSD alignment; torn-write reasoning is simpler with fixed small pages. |
| 5 | **WAL + in-place updates (not pure CoW)** | Lower write amplification for PCKE's small-frequent-updates pattern than Bolt-style CoW; accepts complexity of recovery. |
| 6 | **CRC32C per page + LSN** | Detects torn writes, enables redo-only recovery. |
| 7 | **Double-meta page atomic swap** | Cheap, proven pattern (Bolt, SQLite). No dependency on 4 KB write atomicity. |
| 8 | **Single-writer, many-readers with snapshot isolation** | Matches PCKE workload; dramatically simpler than full MVCC. |
| 9 | **BM25** | Handles term saturation + length normalization. Standard. |
| 10 | **Immutable posting segments with tiered merge** | Write-friendly; read-sequential; familiar Lucene pattern. |
| 11 | **Freelist as B+tree** | Scales, locality, transactional by construction. |
| 12 | **F_FULLFSYNC on macOS** | Regular fsync does not flush drive cache; durability would be a lie without this. |
| 13 | **flock + PID on Open** | Prevents silent corruption from concurrent process access. |
| 14 | **Go** | Single binary, concurrency, mature tooling. |
| 15 | **tree-sitter for AST** | Language-agnostic, 25+ grammars. CGo deferred to Phase 2. |
| 16 | **MCP stdio** | Standard for Claude Code; no ports, no HTTP. |
| 17 | **Incremental scan by default** | Makes `pcke scan` a cheap, frequent operation. |
| 18 | **Secrets filter on by default** | Defense-in-depth; knowledge indexed by PCKE flows to cloud agents via MCP. |
| 19 | **`.pcke/` gitignored by default** | Resolves v3.0 open question. User can opt in to commit via `pcke config commit_db=true`. |
| 20 | **No schema ALTER; migrate tool** | Correctness > convenience at v1. |
| 21 | **Phase 0 ships with WAL** | User writes must be durable from day one; non-negotiable. |

### 10.1 Alternatives considered

- **Pure CoW B+tree (Bolt-style).** Rejected: whole-tree COW amplifies writes on PCKE's small-frequent-update pattern. Kept the idea of CoW meta swap for atomic commit publishing.
- **LSM tree (Badger-style).** Rejected: compaction is complex and write-path ordering with an inverted index is hard. B+tree is a better fit for read-heavy workloads.
- **SQLite as storage.** Rejected: contradicts the "build kdb from scratch" goal, which is a stated non-negotiable.
- **Async index updates.** Rejected: eventual-consistency between primary and index is a nightmare to debug and violates invariant #6.

---

## 11. Technical Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|-----------|
| B+tree bugs | Critical | Property-based tests + fuzzing; invariant checks on every mutation in debug builds |
| WAL recovery edge cases | Critical | Injected-fault test suite covering every record boundary; recovery is pure function over WAL + last meta |
| Torn writes on macOS APFS | Critical | Per-page CRC32C + WAL redo; F_FULLFSYNC on commit |
| Double-process corruption | Critical | `flock` + PID file, fail-fast on Open |
| Snapshot isolation bugs | High | Dedicated concurrency test harness; race detector in CI |
| FTS segment merge correctness | High | Differential tests: results must match a reference scan+filter |
| Inverted index drift vs. primary | High | Every transaction validates: |posting entries added| == |tokens emitted|; asserted in debug builds |
| Performance regression | High | Benchmark suite as CI gate; fail if > 10% regression |
| tree-sitter cross-compilation | Medium | Deferred to Phase 2; provide prebuilt binaries via CI matrix |
| Query DSL scope creep | Medium | Grammar frozen at Phase 3 entry; new ops go to v1.1+ |
| Unicode tokenization scope | Medium | Latin-first; CJK functional; full ICU post-v1 |
| Schema evolution pain | Medium | `pcke migrate` chunked + idempotent; format version gate on Open |

---

## 12. Future Roadmap (Post v1.0)

| Version | Feature | Description |
|---------|---------|-------------|
| v1.1 | Advanced MCP | Streaming, subscriptions, prompt templates |
| v1.2 | Onboarding mode | Auto-generated project walkthrough |
| v1.3 | Online schema evolution | Limited ALTER-like operations |
| v2.0 | Multi-repo intelligence | Cross-repo knowledge federation |
| v2.x | Local embeddings | Vector similarity via local models, augments BM25 |
| v2.x | IDE extensions | VS Code plugin for inline annotations |
| v3.0 | `kdb` standalone | Extract as independent Go module + product |

---

## 13. Resolved Questions & Remaining Open Questions

### 13.1 Resolved in v3.1

| # | Question (v3.0) | Resolution |
|---|-----------------|-----------|
| 1 | Commit `.pcke/`? | Default gitignored; opt-in via config. §10 #19 |
| 2 | Page size configurable? | Fixed 4 KB in v1. §4.4 |
| 3 | Schema evolution model? | Migrate tool, no online ALTER. §4.14 |
| 5 | Git hooks auto-run? | No. Incremental scan is cheap; user invokes. Consider `pre-commit` snippet in docs. |
| 7 | Extract kdb from day one? | Internal package in v1; extraction is v3.0 roadmap (§12). |

### 13.2 Still open

| # | Question | Decision deadline |
|---|----------|-------------------|
| A | Max supported repo size target? (drives B+tree depth / index sizing decisions) | Before Phase 1 |
| B | Compression for stored values (snappy/lz4)? Worth the CPU? | Before Phase 4 |
| C | How to source the Precision@5 ground-truth corpus for FTS validation? | Before Phase 1 |
| D | CJK tokenization scope for v1 (leave per-codepoint or do segmentation library)? | Before Phase 1 |
| E | Should `pcke compact` be online or require DB closed? | Before Phase 2 |

---

## 14. Project Structure (Go)

```
pcke/
├── cmd/
│   └── pcke/
│       └── main.go
├── internal/
│   ├── kdb/
│   │   ├── page/                   # Page manager, buffer pool, clock-sweep
│   │   │   ├── page.go
│   │   │   ├── manager.go
│   │   │   ├── bufpool.go
│   │   │   └── freelist.go
│   │   ├── btree/
│   │   │   ├── tree.go
│   │   │   ├── node.go
│   │   │   ├── cursor.go
│   │   │   ├── split.go
│   │   │   └── overflow.go
│   │   ├── wal/
│   │   │   ├── writer.go
│   │   │   ├── segment.go
│   │   │   ├── recovery.go
│   │   │   └── checkpoint.go
│   │   ├── tx/                     # Transaction manager
│   │   │   ├── tx.go
│   │   │   ├── meta.go             # Double-meta swap
│   │   │   └── snapshot.go
│   │   ├── index/
│   │   │   ├── fts/
│   │   │   │   ├── tokenizer.go
│   │   │   │   ├── segment.go
│   │   │   │   ├── merger.go
│   │   │   │   └── bm25.go
│   │   │   └── secondary.go
│   │   ├── query/
│   │   │   ├── parser.go
│   │   │   ├── ast.go
│   │   │   ├── planner.go
│   │   │   └── executor.go
│   │   ├── encoding/
│   │   │   └── record.go
│   │   ├── diagnostics/
│   │   │   └── stats.go
│   │   ├── lock/
│   │   │   └── flock.go
│   │   └── kdb.go                  # Public API (Open, Close, View, Update)
│   ├── analysis/
│   │   ├── scanner.go
│   │   ├── git.go
│   │   ├── ast.go
│   │   ├── heuristics.go
│   │   └── secrets.go              # Secrets filtering
│   ├── output/
│   │   ├── markdown.go
│   │   └── instructions.go
│   └── mcp/
│       ├── server.go
│       ├── tools.go
│       └── resources.go
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

---

### End of PRD

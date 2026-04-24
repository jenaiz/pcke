# PRD: Project Context & Knowledge Engine (PCKE)

> **Version:** 3.0  
> **Status:** Draft — In Review  
> **Date:** April 2026  
> **Authors:** Jesus Navarrete & AI-assisted  

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

---

## 2. Design Principles

1. **Zero Token Cost** — PCKE never calls an LLM. All extraction is deterministic: AST parsing, Git analysis, heuristics, and developer input.

2. **Code as Truth** — The repository is the primary source of reality. All inferred knowledge must be traceable to actual code.

3. **History as Narrative** — Git history reveals *why* code evolved. PCKE mines commits, diffs, and change patterns to build temporal understanding.

4. **Developer as Oracle** — Code cannot express every decision. The developer provides rules, annotations, and lessons that augment automated extraction.

5. **Own Your Storage** — No third-party databases. `kdb` is built from scratch, giving full control over performance, format, and evolution.

6. **Single Binary, Zero Dependencies** — One executable. No PostgreSQL, no Docker, no runtime. Install and run.

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
│  │  │              Page Manager + WAL                          │  │  │
│  │  │         (disk I/O, crash recovery)                      │  │  │
│  │  └─────────────────────────────────────────────────────────┘  │  │
│  └───────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
```

### 3.1 Component Separation

| Layer | Package | Responsibility |
|-------|---------|---------------|
| `kdb` | `internal/kdb/` | Storage engine: B+tree, paging, WAL, inverted index, BM25, query parser |
| `pcke` | `cmd/`, `internal/analysis/`, `internal/output/`, `internal/mcp/` | Application: CLI, extraction, Markdown generation, MCP server |

`kdb` has **zero knowledge of PCKE's domain**. It's a generic embedded knowledge database that could theoretically be used by other projects. `pcke` uses `kdb` as its storage backend.

---

## 4. `kdb` — Knowledge Database Engine

### 4.1 Design Goals

- **Embedded:** No separate process. Linked directly into the application.
- **Single-writer, concurrent-readers:** One process writes, multiple goroutines read concurrently.
- **Crash-safe:** WAL ensures no data corruption on unexpected shutdown.
- **Full-text search:** Built-in inverted index with BM25 scoring.
- **Graph-aware:** First-class support for relationships between records.
- **Queryable:** Built-in query language for complex lookups.

### 4.2 Storage Layout (On-Disk)

```
.pcke/
├── data/
│   ├── nodes.kdb            # B+tree: primary data store for knowledge nodes
│   ├── evolution.kdb         # B+tree: change history records
│   ├── constraints.kdb       # B+tree: engineering rules
│   ├── relations.kdb         # B+tree: graph edges between nodes
│   └── notes.kdb             # B+tree: developer annotations
├── index/
│   ├── fts/                  # Full-text search inverted index
│   │   ├── terms.idx         # Term dictionary (sorted terms → term IDs)
│   │   ├── postings.idx      # Posting lists (term ID → [doc IDs + positions])
│   │   └── fieldnorms.idx    # Field length norms (for BM25 scoring)
│   └── secondary/            # Secondary B+tree indexes
│       ├── by_module.idx     # Index: module name → node IDs
│       ├── by_file.idx       # Index: file path → node IDs
│       ├── by_type.idx       # Index: node type → node IDs
│       └── by_tag.idx        # Index: tag → note IDs
├── wal/
│   └── 000001.wal           # Write-ahead log segments
└── meta.kdb                  # Schema version, stats, config
```

### 4.3 Page Manager

The lowest level of `kdb`. All data lives in fixed-size **pages**.

- **Page size:** 4096 bytes (aligned with OS page size and SSD sectors)
- **Page types:** Meta, Internal (B+tree branch), Leaf (B+tree data), Overflow (large values), Free
- **Free list:** Tracks deallocated pages for reuse
- **Buffer pool:** LRU cache of recently accessed pages in memory
- **File growth:** Append new pages at EOF; reclaim via free list

```go
// Core page structure
type Page struct {
    ID       uint64    // Page number
    Type     PageType  // Meta | Internal | Leaf | Overflow | Free
    Checksum uint32    // CRC32 for integrity
    Data     [4072]byte // Usable space (4096 - 24 byte header)
}
```

### 4.4 B+Tree Engine

The primary data structure for all persistent storage in `kdb`.

**Properties:**
- Keys are variable-length byte slices (allows composite keys)
- Values are serialized records (binary encoding)
- Leaf nodes are doubly-linked for efficient range scans
- Branch factor adapts to key size (target: 100+ keys per internal node)
- Splits and merges maintain balance

**Operations:**
| Operation | Complexity | Description |
|-----------|-----------|-------------|
| Get(key) | O(log n) | Point lookup |
| Put(key, value) | O(log n) | Insert or update |
| Delete(key) | O(log n) | Remove |
| Range(start, end) | O(log n + k) | Range scan, k = result count |
| Scan(prefix) | O(log n + k) | Prefix scan |

**Serialization format (records):**
```
┌─────────┬──────────┬─────────────────────────────────────┐
│ Version │ Field    │ Fields...                           │
│ (1 byte)│ Count    │ (type tag + length + value) × N    │
│         │ (2 bytes)│                                     │
└─────────┴──────────┴─────────────────────────────────────┘

Field types: uint64, int64, float64, string, bytes, bool, timestamp
```

### 4.5 Write-Ahead Log (WAL)

Ensures crash safety without sacrificing write performance.

**Flow:**
1. Write operation is serialized to WAL segment
2. WAL segment is `fsync`'d to disk
3. Operation is applied to B+tree pages in memory (buffer pool)
4. Periodically, dirty pages are flushed to data files (**checkpoint**)
5. After checkpoint, WAL can be truncated

**Recovery:** On startup, replay any WAL entries after last checkpoint.

**WAL entry format:**
```
┌──────────┬────────┬─────────┬────────┬──────────┐
│ Sequence │ Op     │ Tree    │ Key    │ Value    │
│ (8 bytes)│ (1 b)  │ (1 b)   │ (var)  │ (var)    │
└──────────┴────────┴─────────┴────────┴──────────┘
Op: Put | Delete
Tree: nodes | evolution | constraints | relations | notes
```

### 4.6 Inverted Index (Full-Text Search)

A custom inverted index for `pcke recall` queries, implementing **BM25** scoring.

**Indexing pipeline:**
```
Document → Tokenizer → Normalizer → Posting List Update → Field Norms Update
```

**Tokenizer:** Unicode-aware word boundary splitting. Handles camelCase and snake_case splitting for code identifiers.

**Normalizer:** Lowercase + ASCII folding. No stemming in v1 (code terms don't stem well).

**BM25 scoring:**
$$score(D, Q) = \sum_{i=1}^{n} IDF(q_i) \cdot \frac{f(q_i, D) \cdot (k_1 + 1)}{f(q_i, D) + k_1 \cdot (1 - b + b \cdot \frac{|D|}{avgdl})}$$

Where:
- $k_1 = 1.2$ (term frequency saturation)
- $b = 0.75$ (length normalization)
- $IDF(q_i) = \ln\left(\frac{N - n(q_i) + 0.5}{n(q_i) + 0.5} + 1\right)$

**Data structures:**
- **Term dictionary:** Sorted list of terms → term ID (stored in a B+tree for prefix lookups)
- **Posting lists:** Per term: sorted list of (document ID, term frequency, field ID, positions[])
- **Field norms:** Per document per field: field length (for BM25 length normalization)

**Indexed fields per collection:**
| Collection | Indexed Fields |
|-----------|---------------|
| knowledge_nodes | name, summary, content |
| constraints | rule_text, reason |
| developer_notes | content, tags |

### 4.7 Secondary Indexes

B+tree indexes for non-text lookups:

| Index | Key | Value |
|-------|-----|-------|
| `by_module` | module_name → | []node_id |
| `by_file` | file_path → | []node_id |
| `by_type` | node_type → | []node_id |
| `by_tag` | tag → | []note_id |

These enable fast filtered queries without scanning all records.

### 4.8 Query Language

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

**Examples:**
```
nodes where type = 'module' and stability > 0.7
nodes where module = 'api' order by updated_at desc limit 10
constraints where scope = 'global' and severity = 'must'
evolution where author = 'jesus' and change_type = 'refactored'
notes where tags contains 'decision'
```

**Implementation:** Recursive descent parser → AST → Query planner → Index selection → Execution.

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
| MCP server | **mcp-go** (mark3labs/mcp-go) | Mature Go MCP implementation |

### 5.2 Data Model

The logical schema that `pcke` stores in `kdb`:

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
  status:       string       (active | legacy)
  created_at:   timestamp
  updated_at:   timestamp
}
```

**Collection: `evolution_logs`**
```
{
  id:           uint64
  node_id:      uint64       (references knowledge_nodes)
  commit_hash:  string
  change_type:  string       (created | modified | refactored | deleted)
  description:  string       (what changed, from commit message or heuristic)
  diff_summary: string       (concise diff description)
  author:       string
  committed_at: timestamp
}
```

**Collection: `constraints`**
```
{
  id:           uint64
  scope:        string       (global | module:<name> | file:<path>)
  rule_text:    string       (the constraint in natural language)
  reason:       string       (why this rule exists)
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
  type:           string     (depends_on | implements | replaced_by | relates_to)
}
```

**Collection: `developer_notes`**
```
{
  id:           uint64
  node_id:      uint64       (nullable — can be standalone)
  content:      string
  tags:         string       (comma-separated: "decision,migration,auth")
  created_at:   timestamp
}
```

### 5.3 Analysis Engine

All extraction is deterministic — no LLM calls.

#### 5.3.1 File Tree Scanner (Phase 0)

- Directory structure → module detection
- Language detection by file extension
- File classification by path heuristics:
  - `*_test.go`, `*.spec.ts` → Tests
  - `**/cmd/**`, `**/cli/**` → Entry points
  - `**/api/**`, `**/routes/**` → API layer
  - `**/models/**`, `**/entities/**` → Data layer
  - `Dockerfile`, `*.tf` → Infrastructure

#### 5.3.2 Git Intelligence (Phase 0)

Powered by `go-git`:

- **Change Frequency** → Volatile vs. stable modules
- **Coupling Detection** → Files that change together = implicit dependency
- **Stability Scoring** → `1 - (changes_last_90d / total_changes)`
- **Conventional Commit Parsing** → `feat:`, `fix:`, `refactor:`, `breaking:`
- **Authorship Map** → Who owns what module

#### 5.3.3 AST Structural Analysis (Phase 2)

Powered by `tree-sitter`:

- Entity extraction: functions, classes, interfaces, structs
- Import/dependency mapping
- Pattern recognition heuristics (controllers, models, services, middleware)
- Export surface detection (public API per module)

#### 5.3.4 Developer Annotations (Phase 0+)

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

### 5.4 Output System

#### 5.4.1 Markdown Context Directory (`.context/`)

`pcke sync` generates:

```
.context/
├── ARCHITECTURE.md         # Module map, dependency graph, tech stack
├── CONVENTIONS.md          # Detected patterns, naming conventions
├── HISTORY.md              # Timeline of significant changes
├── DECISIONS.md            # Developer notes, lessons learned
├── CONSTRAINTS.md          # Engineering rules (must/must-not/should)
└── MODULES/
    ├── api.md              # Per-module context
    ├── database.md
    └── ...
```

Auto-generates agent instruction files:
- `.github/copilot-instructions.md` — GitHub Copilot
- `.claude/CLAUDE.md` — Claude Code

#### 5.4.2 MCP Server (`pcke serve`)

Transport: **stdio**

**Tools:**

| Tool | Parameters | Returns |
|------|-----------|---------|
| `recall` | `query: string` | BM25-ranked results from the knowledge base |
| `get_module_context` | `module: string` | Full module context: summary, files, deps, history, constraints |
| `get_constraints` | `scope?: string` | Rules applicable to scope (or global) |
| `get_history` | `file_path: string` | Evolution timeline for a file/module |
| `query` | `query_string: string` | Execute a `kdb` query directly |

**Resources:**

| URI | Description |
|-----|-------------|
| `pcke://architecture` | Full architecture map |
| `pcke://constraints` | All engineering rules |
| `pcke://decisions` | All developer decisions and lessons |

---

## 6. CLI Specification

```
pcke — Project Context & Knowledge Engine

Usage:
  pcke [command]

Core Commands:
  init                  Initialize PCKE in the current repository
  scan [--deep]         Analyze project and update knowledge base
  sync                  Regenerate .context/ files from knowledge base
  recall <query>        Full-text search the knowledge base (BM25 ranked)
  query <expression>    Execute a kdb query (see query language docs)

Knowledge Commands:
  rule add <text>       Add an engineering constraint
  rule list             List all constraints
  rule remove <id>      Remove a constraint
  note <text>           Add a developer note
  note list             List all notes

Inspection Commands:
  status                Knowledge base health and statistics
  modules               List detected modules with stability scores

Server Commands:
  serve                 Start MCP server (stdio transport)

Export Commands:
  export [--format=json|yaml]   Export full knowledge base

Flags:
  --scope <scope>       Scope for rules: global, module:<name>, file:<path>
  --severity <level>    Rule severity: must, should, must_not
  --tag <tags>          Comma-separated tags for notes
  --file <path>         Associate a note with a file
  --format <fmt>        Output format: text (default), json
  -v, --verbose         Verbose output
  --version             Print version
```

### Command Phasing

| Command | Phase |
|---------|-------|
| `pcke init` | 0 |
| `pcke scan` | 0 |
| `pcke sync` | 0 |
| `pcke recall <query>` | 1 |
| `pcke rule add/list/remove` | 0 |
| `pcke note/note list` | 0 |
| `pcke status` | 0 |
| `pcke modules` | 0 |
| `pcke scan --deep` | 2 |
| `pcke serve` | 2 |
| `pcke query <expr>` | 3 |
| `pcke export` | 3 |

---

## 7. User Journey

### Setup (Day 1)

```bash
# Install
go install github.com/jesusnavarrete/pcke@latest

# Initialize
cd my-project
pcke init
# → Created .pcke/ (knowledge database)
# → Created .context/ (output directory)
# → Added .pcke/ to .gitignore

# First scan
pcke scan
# → Scanned 247 files across 12 directories
# → Analyzed 83 recent commits
# → Detected 8 modules: api, auth, database, config, middleware, models, services, utils
# → Knowledge base: 42 nodes, 83 evolution logs
# → Indexed 42 documents for full-text search

# Generate context
pcke sync
# → Generated .context/ARCHITECTURE.md
# → Generated .context/MODULES/api.md (and 7 others)
# → Updated .github/copilot-instructions.md
# → Updated .claude/CLAUDE.md
```

### Add Knowledge (Day 1)

```bash
pcke rule add "All API endpoints must validate JWT before DB access" \
  --scope=module:api --severity=must

pcke rule add "Never SELECT * in production queries" --severity=must_not

pcke note "Migrated from Express to Fastify Q3 2025 for perf. \
  Some legacy routes still use Express patterns." --tag=decision,migration

pcke sync
```

### Daily Use

AI agents automatically read `.context/` files. Your Copilot/Claude now knows the architecture, rules, and history.

### Query Knowledge

```bash
# Simple full-text search
pcke recall "auth system changes"
# → [node] auth module: JWT-based authentication (stability: 0.82)
# → [evolution] a3f2b1c: Refactored from sessions to JWT (2025-09-12)
# → [note] "Migrated to JWT for stateless API scaling" #decision #auth

# Power query
pcke query "nodes where module = 'api' and stability > 0.7 order by updated_at desc"
# → api/handlers.go    (stability: 0.91, updated: 2026-03-15)
# → api/middleware.go   (stability: 0.88, updated: 2026-02-28)
# → api/routes.go       (stability: 0.75, updated: 2026-04-01)
```

---

## 8. Development Phases

### Phase 0 — Foundation

**Goal:** B+tree storage working + CLI with basic scan and write operations.

| Component | Deliverable |
|-----------|------------|
| **kdb** | Page manager (4KB pages, free list). B+tree (put, get, delete, range scan). Record serialization. Meta page with schema version. |
| **pcke** | CLI scaffolding (Cobra). `init`, `scan` (file tree + git log, no AST), `sync` (Markdown generation), `rule add/list/remove`, `note/note list`, `status`, `modules`. |

**Testing:** B+tree correctness tests (insert/delete/split/merge), crash simulation (kill process mid-write, verify data on restart — no WAL yet, so expect some loss).

**Constraint:** No WAL, no FTS, no query language. Reads from B+tree directly. `recall` is a naive scan + contains match.

---

### Phase 1 — Search

**Goal:** Full-text search with BM25 scoring.

| Component | Deliverable |
|-----------|------------|
| **kdb** | Inverted index (tokenizer, normalizer, posting lists). BM25 scorer. FTS indexing on write. |
| **pcke** | `pcke recall` powered by BM25-ranked FTS. Index all text fields on `pcke scan`. |

**Testing:** Precision@5 test suite with predefined queries and expected results. BM25 scoring validation against reference implementation.

---

### Phase 2 — Resilience & Deep Analysis

**Goal:** Crash safety + AST-based code understanding + MCP server.

| Component | Deliverable |
|-----------|------------|
| **kdb** | WAL (write-ahead log). Checkpoint mechanism. Crash recovery on startup. Secondary indexes (by_module, by_file, by_type, by_tag). |
| **pcke** | `pcke scan --deep` with tree-sitter AST analysis. `pcke serve` (MCP server on stdio). Branch-aware context. |

**Testing:** Crash-recovery test suite (inject failures at every WAL stage, verify consistency). MCP integration tests with Claude Code.

---

### Phase 3 — Query & Polish

**Goal:** Query language + advanced features + production readiness.

| Component | Deliverable |
|-----------|------------|
| **kdb** | Query parser (recursive descent). Query planner (index selection). Query executor. |
| **pcke** | `pcke query <expr>`. `pcke export`. In-code annotation parsing. Performance benchmarks. |

**Testing:** Query correctness (parsed AST matches expected). Query planner selects optimal index. End-to-end benchmarks on repos of 1K, 10K, 100K files.

---

### Phase 4 — v1.0

**Goal:** Production-ready, documented, benchmarked.

| Component | Deliverable |
|-----------|------------|
| **kdb** | Buffer pool tuning. Compaction (reclaim fragmented space). Concurrent reader optimization. |
| **pcke** | Multi-repo support (shared knowledge). Schema migration tooling. Comprehensive documentation. |

---

## 9. Validation & Quality Metrics

| Metric | How to Measure | Target |
|--------|---------------|--------|
| **B+tree correctness** | Property-based tests: random insert/delete sequences, verify invariants hold | 100% pass |
| **Crash safety** | Kill process at random points, verify recovery produces consistent state | Zero data corruption |
| **Recall Precision@5** | Test suite: 50 queries with labeled relevant results | > 70% |
| **Context Coverage** | % of project modules with knowledge nodes | > 80% |
| **Scan Performance** | Time for `pcke scan` on 10K files | < 10s |
| **FTS Query Latency** | p99 of `pcke recall` on 10K-node DB | < 50ms |
| **Binary Size** | Final compiled binary | < 30MB |
| **Memory Usage** | Peak RSS during scan of 10K file project | < 200MB |

---

## 10. Design Decisions & Rationale

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | **No LLM usage** | Zero token cost is the core differentiator. PCKE is pre-LLM infrastructure. |
| 2 | **Custom DB from scratch** | Full control over format, performance, and evolution. No vendor lock-in. Building it is the product differentiation. |
| 3 | **B+tree as primary structure** | Proven for ordered data, range scans, and disk-friendly access patterns. Well-documented algorithms. |
| 4 | **4KB pages** | Aligned with OS page size and SSD sector size. Minimizes read/write amplification. |
| 5 | **WAL for crash safety** | Industry standard approach. Simpler than shadow paging, better write performance than force-on-commit. |
| 6 | **BM25 over tf-idf** | BM25 handles term saturation and document length normalization better. Standard in modern search engines. |
| 7 | **Go** | Single binary, good concurrency (reader goroutines), mature ecosystem. Easier iteration than Rust with comparable distribution. |
| 8 | **tree-sitter for AST** | Language-agnostic. 25+ grammars. CGo only at build time, deferred to Phase 2. |
| 9 | **MCP on stdio** | Standard for Claude Code integration. No ports, no HTTP overhead. |
| 10 | **Directory-based storage** | Separating data files, indexes, and WAL allows independent compaction and backup strategies. |
| 11 | **Individual-first** | Simpler architecture. Team features (shared DBs) are post-v1.0. `.context/` can be git-committed for informal sharing. |
| 12 | **Phased: storage before features** | The DB engine is the riskiest component. Building it first ensures the foundation is solid before adding application features. |

---

## 11. Technical Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|-----------|
| B+tree bugs (data loss) | Critical | Extensive property-based testing. Fuzzing. Reference comparison with known implementations. |
| WAL recovery edge cases | Critical | Inject failures at every step. Test with PCKE's own test suite as CI gate. |
| Performance degradation on large repos | High | Profiling from Phase 0. Buffer pool tuning. Benchmark suite as regression gate. |
| tree-sitter CGo complicates cross-compilation | Medium | Defer to Phase 2. Provide pre-built binaries via CI for common platforms. |
| Query language complexity creep | Medium | Keep grammar minimal. No joins, no subqueries, no aggregations in v1. |
| Scope creep on kdb | High | kdb has a frozen interface contract per phase. New features only in next phase. |

---

## 12. Future Roadmap (Post v1.0)

| Version | Feature | Description |
|---------|---------|-------------|
| v1.1 | **Advanced MCP** | Streaming responses, knowledge update subscriptions, prompt templates |
| v1.2 | **Onboarding Mode** | Auto-generated project walkthrough for new developers |
| v2.0 | **Multi-repo Intelligence** | Cross-pollinate knowledge across related repositories |
| v2.x | **Local Embeddings** | Vector similarity search using local models (no API). Augments BM25 |
| v2.x | **IDE Extensions** | VS Code extension for inline annotations and knowledge previews |
| v3.0 | **`kdb` as standalone** | Extract kdb as independent embeddable database product |

---

## 13. Open Questions

| # | Question | Impact |
|---|----------|--------|
| 1 | Should `.pcke/` be committable or always `.gitignore`'d? | Team sharing model |
| 2 | Page size: stick with 4KB or allow configuration? | Performance vs. simplicity |
| 3 | Should `kdb` support schema evolution (ALTER-like ops) or require rebuild? | Upgrade path between PCKE versions |
| 4 | Max supported repo size? (impacts B+tree depth, index size) | Architecture decisions |
| 5 | Should `pcke scan` auto-run on git hooks? | Freshness vs. friction |
| 6 | Compression for stored values? (snappy, lz4) | Space vs. CPU tradeoff |
| 7 | Should `kdb` be extracted as a separate Go module from day one? | Code organization vs. iteration speed |

---

## 14. Project Structure (Go)

```
pcke/
├── cmd/
│   └── pcke/
│       └── main.go                 # Entry point
├── internal/
│   ├── kdb/                        # Knowledge Database Engine
│   │   ├── page/                   # Page manager, buffer pool
│   │   │   ├── page.go
│   │   │   ├── manager.go
│   │   │   └── freelist.go
│   │   ├── btree/                  # B+tree implementation
│   │   │   ├── tree.go
│   │   │   ├── node.go
│   │   │   ├── cursor.go
│   │   │   └── split.go
│   │   ├── wal/                    # Write-ahead log
│   │   │   ├── writer.go
│   │   │   └── recovery.go
│   │   ├── index/                  # Inverted index + secondary indexes
│   │   │   ├── inverted.go
│   │   │   ├── tokenizer.go
│   │   │   ├── bm25.go
│   │   │   └── secondary.go
│   │   ├── query/                  # Query parser + planner + executor
│   │   │   ├── parser.go
│   │   │   ├── ast.go
│   │   │   ├── planner.go
│   │   │   └── executor.go
│   │   ├── encoding/               # Record serialization
│   │   │   └── record.go
│   │   └── kdb.go                  # Public API (Open, Close, Collection, Query)
│   ├── analysis/                   # Code analysis engine
│   │   ├── scanner.go              # File tree scanner
│   │   ├── git.go                  # Git intelligence (go-git)
│   │   ├── ast.go                  # AST analysis (tree-sitter)
│   │   └── heuristics.go          # Classification rules
│   ├── output/                     # Output generation
│   │   ├── markdown.go            # .context/ file generation
│   │   └── instructions.go        # copilot-instructions / CLAUDE.md
│   └── mcp/                        # MCP server
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

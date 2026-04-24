# PRD: Project Context & Knowledge Engine (PCKE)

> **Version:** 2.0  
> **Status:** Draft — In Review  
> **Date:** April 2026  
> **Authors:** Jesus Navarrete & AI-assisted  

---

## 1. Product Vision

### 1.1 Problem Statement

Every AI chat session starts from zero. An LLM working on a codebase has no memory of past architectural decisions, recurring bugs, business rules embedded in code, or the reasoning behind refactors. Developers waste tokens re-explaining context that already exists in the repository's code, commits, and tribal knowledge.

### 1.2 What PCKE Is

PCKE is a **Long-Term Engineering Memory** — a local CLI tool that extracts, organizes, and serves project knowledge to AI coding agents (GitHub Copilot, Claude Code, and others) **before** they consume a single token.

PCKE reads your codebase, analyzes your Git history, and combines it with developer-provided annotations to build a structured knowledge base. That knowledge is then served as Markdown context files and via an MCP server, so your AI agents behave like a Senior Engineer who has been on the project for years.

### 1.3 What PCKE Is NOT

- **Not an LLM wrapper.** PCKE never calls an LLM. Zero token cost at runtime.
- **Not a SaaS.** It's a local binary. Your code never leaves your machine.
- **Not a database product.** The storage engine is an implementation detail, starting with SQLite and evolving to a custom engine.

### 1.4 Key Differentiators

| Property | PCKE | Typical RAG tools |
|----------|------|-------------------|
| Token cost | Zero | Per-query embedding + LLM |
| External dependencies | None (single binary) | Vector DB, API keys, cloud |
| Privacy | Code never leaves local | Often requires API calls |
| Setup | `pcke init` | DB setup, API config, indexing pipeline |

---

## 2. Design Principles

1. **Zero Token Cost** — PCKE never calls an LLM. All extraction is deterministic: AST parsing, Git analysis, heuristics, and developer input. Its value is entirely pre-LLM.

2. **Code as Truth** — The current state of the repository is the primary source of reality. All inferred knowledge must be traceable to actual code.

3. **History as Narrative** — Git history is not just a log of changes — it reveals *why* code evolved. PCKE mines commits, diffs, and change patterns to build a temporal understanding of the project.

4. **Developer as Oracle** — Code cannot express every decision. The developer provides rules, annotations, and lessons that augment automated extraction. The hybrid of machine analysis + human input produces the richest context.

5. **Single Binary, Zero Dependencies** — `pcke` ships as one executable. No PostgreSQL, no Docker, no Python runtime, no API keys. Install and run.

---

## 3. Technical Architecture

### 3.1 Technology Stack

| Component | Technology | Rationale |
|-----------|-----------|-----------|
| Language | **Go** | Compiles to single binary, strong concurrency, mature ecosystem |
| CLI framework | **Cobra** (spf13/cobra) | Industry standard (used by kubectl, docker, gh) |
| Git analysis | **go-git** (go-git/go-git) | Pure Go, no `git` binary dependency, full log/diff/blame support |
| AST parsing | **tree-sitter** (smacker/go-tree-sitter) | Multi-language AST parsing, 25+ grammars included |
| Storage (MVP) | **SQLite + FTS5** (modernc.org/sqlite) | Pure Go SQLite, embedded, full-text search built-in |
| MCP server | **mcp-go** (mark3labs/mcp-go) | Mature Go MCP implementation, stdio transport |

> **Note on tree-sitter:** Requires CGo (C compiler) at build time. The resulting binary is fully self-contained and distributable without external dependencies. This only applies from Phase 1 (v0.2) onward — the MVP does not use AST.

### 3.2 Storage Architecture

#### MVP: SQLite + FTS5

The knowledge base lives in `.pcke/pcke.db` inside the repository root.

- **SQLite** provides ACID transactions, zero-config setup, and portability (single file).
- **FTS5** (Full-Text Search v5) powers `pcke recall` queries with ranked results.
- The database file can be `.gitignore`'d (personal context) or committed (shared context).

#### Future: Custom Storage Engine (v1.0)

The long-term goal is to replace SQLite with a purpose-built engine optimized for PCKE's access patterns:

- Knowledge graph traversal with efficient relationship queries
- BM25 ranking built-in (replacing FTS5's default ranking)
- Optimized for append-heavy, read-frequent workloads
- Potential future: local embedding-based similarity search (no external API)

The SQLite schema is designed as a contract — the custom engine must support the same logical model.

### 3.3 Data Model (SQLite MVP Schema)

```sql
-- What we know: code entities, patterns, summaries
CREATE TABLE knowledge_nodes (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    type          TEXT NOT NULL CHECK(type IN ('module','function','pattern','rule','lesson')),
    name          TEXT NOT NULL,
    summary       TEXT,
    content       TEXT,
    file_path     TEXT,
    language      TEXT,
    stability     REAL DEFAULT 0.0,  -- 0.0 (volatile) to 1.0 (stable)
    status        TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','legacy')),
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- How things evolved: change history per node
CREATE TABLE evolution_logs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id       INTEGER NOT NULL REFERENCES knowledge_nodes(id),
    commit_hash   TEXT,
    change_type   TEXT NOT NULL CHECK(change_type IN ('created','modified','refactored','deleted')),
    description   TEXT,
    diff_summary  TEXT,
    author        TEXT,
    committed_at  DATETIME
);

-- Engineering guardrails: must/must-not/should rules
CREATE TABLE constraints (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    scope         TEXT NOT NULL DEFAULT 'global',  -- 'global', 'module:api', 'file:src/auth.go'
    rule_text     TEXT NOT NULL,
    reason        TEXT,
    source        TEXT NOT NULL DEFAULT 'manual' CHECK(source IN ('auto','manual')),
    severity      TEXT NOT NULL DEFAULT 'should' CHECK(severity IN ('must','should','must_not')),
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Connections between knowledge nodes
CREATE TABLE relationships (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    source_node_id  INTEGER NOT NULL REFERENCES knowledge_nodes(id),
    target_node_id  INTEGER NOT NULL REFERENCES knowledge_nodes(id),
    type            TEXT NOT NULL CHECK(type IN ('depends_on','implements','replaced_by','relates_to'))
);

-- Developer-provided context: notes, decisions, lessons
CREATE TABLE developer_notes (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id       INTEGER REFERENCES knowledge_nodes(id),  -- nullable: can be standalone
    content       TEXT NOT NULL,
    tags          TEXT,  -- comma-separated: "decision,migration,redis"
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Full-text search index across all textual content
CREATE VIRTUAL TABLE knowledge_fts USING fts5(
    name,
    summary,
    content,
    rule_text,
    note_content,
    content=''  -- contentless: we manage content manually
);
```

### 3.4 System Architecture Diagram

```
┌──────────────────────────────────────────────────────────┐
│                     Developer Input                       │
│  pcke rule add / pcke note / @pcke-rule comments         │
└──────────────────┬───────────────────────────────────────┘
                   │
                   ▼
┌──────────────────────────────────────────────────────────┐
│                   PCKE Analysis Engine                    │
│  ┌──────────────┐ ┌──────────────┐ ┌──────────────────┐ │
│  │  File Tree    │ │  Git Intel   │ │  AST Parser      │ │
│  │  Scanner      │ │  (go-git)    │ │  (tree-sitter)   │ │
│  │              │ │              │ │   [Phase 1+]     │ │
│  └──────┬───────┘ └──────┬───────┘ └────────┬─────────┘ │
│         └────────────────┼──────────────────┘           │
│                          ▼                               │
│              ┌───────────────────────┐                   │
│              │   Knowledge Assembler │                   │
│              │   (Heuristics Engine) │                   │
│              └───────────┬───────────┘                   │
└──────────────────────────┼───────────────────────────────┘
                           ▼
              ┌───────────────────────┐
              │    SQLite + FTS5      │
              │    (.pcke/pcke.db)    │
              └───────────┬───────────┘
                          │
              ┌───────────┴───────────┐
              ▼                       ▼
┌──────────────────────┐  ┌──────────────────────┐
│   Markdown Output    │  │    MCP Server        │
│   (.context/)        │  │    (stdio)           │
│                      │  │                      │
│  ARCHITECTURE.md     │  │  Tools:              │
│  CONVENTIONS.md      │  │   - recall           │
│  HISTORY.md          │  │   - get_module       │
│  DECISIONS.md        │  │   - get_constraints  │
│  CONSTRAINTS.md      │  │   - get_history      │
│  MODULES/*.md        │  │                      │
└──────────┬───────────┘  └──────────────────────┘
           │
           ▼
┌──────────────────────────────────────────────────────────┐
│              AI Agent Context Layer                       │
│  .github/copilot-instructions.md                         │
│  .claude/CLAUDE.md                                       │
└──────────────────────────────────────────────────────────┘
```

---

## 4. Analysis Engine

PCKE extracts knowledge **without calling any LLM**. All analysis is deterministic, reproducible, and runs locally.

### 4.1 File Tree Scanner (MVP)

The simplest analysis layer — available from day one:

- **Directory structure mapping:** Identifies top-level modules, packages, and their organization.
- **Language detection:** By file extension (`.go`, `.ts`, `.py`, `.rs`, etc.).
- **File classification heuristics:**
  - `*_test.go`, `*.spec.ts`, `test_*.py` → Test files
  - `**/cmd/**`, `**/cli/**` → Entry points
  - `**/api/**`, `**/routes/**`, `**/handlers/**` → API layer
  - `**/models/**`, `**/entities/**`, `**/schema/**` → Data layer
  - `**/config/**`, `*.env*`, `*.yaml`, `*.toml` → Configuration
  - `Dockerfile`, `docker-compose*`, `*.tf` → Infrastructure
- **Size & complexity signals:** File count per directory, LOC, nesting depth.

### 4.2 Git Intelligence (MVP)

Powered by `go-git` (pure Go, no external `git` binary needed):

- **Change Frequency:** Files that change most often → volatile modules. Files unchanged in months → stable core.
- **Coupling Detection:** Files that consistently change together in the same commits → implicit coupling (even if no import relationship).
- **Stability Scoring:** `stability = 1 - (changes_last_90d / total_changes)`. Score from 0.0 (volatile) to 1.0 (stable).
- **Conventional Commit Parsing:** Extract structured info from `feat:`, `fix:`, `refactor:`, `breaking:` prefixes.
- **Authorship Map:** Who has changed each module most → ownership signal.
- **Significant Change Detection:** Identify commits that touched many files, renamed modules, or deleted large sections → potential architectural shifts.

### 4.3 AST Structural Analysis (Phase 1)

Powered by `tree-sitter` with grammars for Go, TypeScript/JavaScript, Python, and Rust:

- **Entity Extraction:** Functions, classes, interfaces, structs, enums, constants.
- **Dependency Mapping:** Import/require/use statements → inter-module dependency graph.
- **Export Surface:** Public API of each module (exported functions, types).
- **Pattern Recognition Heuristics:**
  - Controllers: route decorators (`@Get`, `@Post`), handler function signatures, router registrations.
  - Models: ORM decorators (`@Entity`, `@Column`), schema definitions, migration files.
  - Services: dependency injection patterns, constructor injection, provider registrations.
  - Middleware: function signatures matching `(req, res, next)` or `Handler` wrapping patterns.
- **Complexity Signals:** Function count per file, nesting depth, parameter counts.

### 4.4 Developer Annotations (Hybrid — MVP onward)

The developer provides context that machines cannot infer:

**Via CLI:**
```bash
# Add an engineering constraint
pcke rule add "Never use raw SQL in controller layer" --scope=module:api --severity=must

# Record a decision or lesson
pcke note "Migrated from Redis to Valkey due to license change" --tag=decision,migration

# Attach context to a specific file
pcke note "Session cache TTL is 24h — GDPR requirement" --file=src/cache/session.go
```

**Via in-code annotations (Phase 2):**
```go
// @pcke-rule: must validate JWT before any database access
// @pcke-lesson: we tried connection pooling with pgbouncer but it broke transactions
```

---

## 5. Output System

PCKE delivers knowledge to AI agents through two complementary channels.

### 5.1 Markdown Context Directory (`.context/`)

`pcke sync` generates and maintains a structured directory that AI agents read automatically:

```
.context/
├── ARCHITECTURE.md        # Module map, dependency graph, tech stack
├── CONVENTIONS.md         # Detected patterns, naming conventions, code style
├── HISTORY.md             # Timeline of significant changes and architectural shifts
├── DECISIONS.md           # Developer notes, lessons learned, decision records
├── CONSTRAINTS.md         # Engineering rules (must/must-not/should)
└── MODULES/
    ├── api.md             # Context for the API layer
    ├── database.md        # Context for the data layer
    ├── auth.md            # Context for authentication
    └── ...                # One file per detected module
```

**AI Agent Integration:**

PCKE auto-generates compact summaries injected into:
- `.github/copilot-instructions.md` — GitHub Copilot reads this automatically
- `.claude/CLAUDE.md` — Claude Code reads this automatically

These files contain a condensed version of constraints, architecture overview, and active conventions — enough to ground the AI without overwhelming the context window.

### 5.2 MCP Server (`pcke serve`)

A Model Context Protocol server for real-time, on-demand knowledge retrieval:

**Transport:** stdio (standard for Claude Code integration)

**Tools (callable by the AI agent):**

| Tool | Parameters | Returns |
|------|-----------|---------|
| `recall` | `query: string` | FTS5-ranked results from the entire knowledge base |
| `get_module_context` | `module: string` | Full context for a specific module (summary, files, dependencies, history, constraints) |
| `get_constraints` | `scope?: string` | All rules applicable to the given scope (or global if omitted) |
| `get_history` | `file_path: string` | Evolution timeline for a specific file or module |

**Resources (readable by the AI agent):**

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
  init                Initialize PCKE in the current repository
  scan                Analyze the project and update the knowledge base
  sync                Regenerate .context/ files from the knowledge base
  recall <query>      Search the knowledge base

Knowledge Commands:
  rule add <text>     Add an engineering constraint
  rule list           List all constraints
  rule remove <id>    Remove a constraint
  note <text>         Add a developer note or decision record
  note list           List all notes

Inspection Commands:
  status              Show knowledge base health and stats
  modules             List all detected modules with their stability scores

Server Commands:
  serve               Start the MCP server (stdio transport)

Flags:
  --deep              Enable AST-based deep analysis (scan only)
  --scope <scope>     Scope for rules: global, module:<name>, file:<path>
  --severity <level>  Severity for rules: must, should, must_not
  --tag <tags>        Comma-separated tags for notes
  --file <path>       Associate a note with a specific file
  --format <fmt>      Output format: text (default), json
  -v, --verbose       Verbose output
  --version           Print version
```

### Command Details

| Command | Description | Phase |
|---------|-------------|-------|
| `pcke init` | Creates `.pcke/` directory, initializes SQLite DB, creates `.context/` scaffold | MVP |
| `pcke scan` | File tree scan + recent git history (last 100 commits). Updates knowledge nodes and evolution logs | MVP |
| `pcke scan --deep` | Full AST analysis (tree-sitter) + complete git history + coupling detection + stability scoring | v0.2 |
| `pcke sync` | Reads the knowledge base and regenerates all `.context/*.md` files and agent instruction files | MVP |
| `pcke recall <query>` | Full-text search across all knowledge (nodes, constraints, notes). Returns ranked results | MVP |
| `pcke rule add <text>` | Inserts a manual constraint into the `constraints` table | MVP |
| `pcke rule list` | Lists all constraints, grouped by scope | MVP |
| `pcke rule remove <id>` | Removes a constraint by ID | MVP |
| `pcke note <text>` | Inserts a developer note into `developer_notes` | MVP |
| `pcke note list` | Lists all notes, most recent first | MVP |
| `pcke status` | Prints: node count, last scan time, stale modules, constraint count, FTS index size | MVP |
| `pcke modules` | Lists detected modules with stability score, file count, last change date | MVP |
| `pcke serve` | Starts MCP server on stdio. Exposes tools and resources from Section 5.2 | v0.2 |
| `pcke export` | Exports full knowledge base to JSON or YAML | v0.3 |

---

## 7. User Journey

### Day 1: Setup

```bash
# Install (single binary, no dependencies)
go install github.com/user/pcke@latest

# Initialize in a project
cd my-project
pcke init
# → Created .pcke/pcke.db
# → Created .context/ directory
# → Added .pcke/ to .gitignore

# First scan
pcke scan
# → Scanned 247 files across 12 directories
# → Analyzed 83 recent commits
# → Detected 8 modules: api, auth, database, config, middleware, models, services, utils
# → Knowledge base: 42 nodes, 83 evolution logs

# Generate context files
pcke sync
# → Generated .context/ARCHITECTURE.md
# → Generated .context/CONVENTIONS.md
# → Generated .context/HISTORY.md
# → Generated .context/MODULES/api.md (and 7 others)
# → Updated .github/copilot-instructions.md
# → Updated .claude/CLAUDE.md
```

### Day 1: Add team knowledge

```bash
# Record architectural decisions
pcke rule add "All API endpoints must validate JWT before accessing the database" \
  --scope=module:api --severity=must

pcke rule add "Never use SELECT * in production queries" \
  --scope=global --severity=must_not

pcke note "We migrated from Express to Fastify in Q3 2025 for performance. \
  Some legacy routes still use Express patterns." --tag=decision,migration

pcke sync  # Regenerate context with new rules
```

### Daily use: AI agents read context automatically

When you open GitHub Copilot or Claude Code, they automatically read the `.context/` files and `copilot-instructions.md`. Your AI now knows:

- The project architecture and module boundaries
- Which files are stable core vs. actively changing
- Engineering rules it must follow
- Past decisions and why they were made

### On demand: Query your knowledge base

```bash
pcke recall "why did we change the auth system"
# → [evolution_log] commit a3f2b1c: Refactored auth from session-based to JWT (2025-09-12)
# → [developer_note] "Migrated to JWT for stateless API scaling" (tagged: decision, auth)
# → [constraint] "All API endpoints must validate JWT before accessing the database" (scope: module:api)

pcke status
# → Knowledge base: 42 nodes, 156 evolution logs, 5 constraints, 3 notes
# → Last scan: 2 hours ago (14 new commits since)
# → Stale modules: auth (3 files changed since last scan)
```

---

## 8. Development Phases

### Phase 0 — MVP (v0.1)

**Goal:** A working CLI that scans a project and produces useful Markdown context.

**Scope:**
- CLI scaffolding with Cobra
- `pcke init` — directory and database setup
- `pcke scan` — file tree scanning + recent git log (last 100 commits, no AST)
- `pcke sync` — Markdown generation for `.context/` and agent instruction files
- `pcke recall <query>` — FTS5 search
- `pcke rule add/list/remove` — manual constraint management
- `pcke note/note list` — developer notes
- `pcke status` — knowledge base stats
- `pcke modules` — module listing
- SQLite + FTS5 storage (modernc.org/sqlite, pure Go)
- Language detection by file extension only (no parsing)
- File classification by path heuristics

**Deliverable:** A `go install`-able binary that improves Copilot/Claude context in any Go, TS, Python, or Rust project.

### Phase 1 — Deep Analysis & MCP (v0.2)

**Goal:** AST-based structural analysis and real-time MCP server.

**Scope:**
- tree-sitter integration (Go, TypeScript/JavaScript, Python, Rust grammars)
- `pcke scan --deep` — AST entity extraction, dependency mapping, pattern recognition
- Git coupling detection (files that change together)
- Stability scoring (change frequency analysis)
- `pcke serve` — MCP server on stdio with tools and resources
- Branch-aware context (knowledge scoped to current branch)

**Deliverable:** Deep code understanding and native Claude Code integration via MCP.

### Phase 2 — Advanced Search & Annotations (v0.3)

**Goal:** Better retrieval, in-code annotations, and export.

**Scope:**
- Custom BM25 ranking replacing SQLite FTS5 default ranking
- `pcke export` — JSON/YAML export of full knowledge base
- In-code annotation parsing (`@pcke-rule`, `@pcke-lesson` comments)
- Multi-repo support (shared context across related repositories)

**Deliverable:** Production-grade search quality and team workflow support.

### Phase 3 — Custom Storage Engine (v1.0)

**Goal:** Replace SQLite with a purpose-built storage engine.

**Scope:**
- Custom storage engine optimized for knowledge graph operations
- Efficient relationship traversal (graph queries)
- Built-in BM25 scoring
- Plugin API for language-specific parsers
- Migration tool from SQLite → custom engine

**Deliverable:** PCKE v1.0 with its own storage engine, zero external dependencies.

---

## 9. Validation & Quality Metrics

Metrics that are **actually measurable** without requiring LLM instrumentation:

| Metric | How to Measure | Target |
|--------|---------------|--------|
| **Recall Precision** | Automated test suite: predefined queries with expected results. Measure precision@5 | > 70% |
| **Context Coverage** | `pcke status`: % of project modules with at least one knowledge node and summary | > 80% |
| **Freshness** | Delta between last `pcke scan` and newest commit. Alert if > N commits behind | < 20 commits |
| **Constraint Completeness** | Ratio of auto-detected patterns to total constraints (auto + manual) | Tracked, no target |
| **Scan Performance** | Time to complete `pcke scan` on repos of 10K, 50K, 100K files | < 30s for 10K files |
| **Integration Test** | A/B comparison: AI agent performance with vs. without `.context/`. Manual evaluation on 10 standard tasks | Qualitative improvement |

---

## 10. Design Decisions & Rationale

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | **No internal LLM usage** | Zero token cost is PCKE's core differentiator. If it needs an LLM, it becomes just another RAG wrapper. |
| 2 | **Go** | Single binary compilation, strong stdlib (HTTP, JSON, filesystem), good concurrency, mature CLI ecosystem. Easier onboarding than Rust with comparable distribution story. |
| 3 | **SQLite for MVP** | Zero-config embedded database. FTS5 gives us full-text search for free. Pure Go driver (modernc.org) means no CGo for MVP. Database is a single file — trivial to back up, copy, or delete. |
| 4 | **Custom engine as v1.0 goal** | SQLite is general-purpose. PCKE's access patterns (graph traversal, temporal queries, BM25 ranking) will benefit from a purpose-built engine. Building it is also a learning and differentiation opportunity. |
| 5 | **tree-sitter for AST** | Language-agnostic parsing with one library. 25+ grammars available. The CGo requirement is acceptable because (a) it only affects build time, not runtime, and (b) it's deferred to Phase 1. |
| 6 | **MCP server on stdio** | stdio is the standard transport for Claude Code MCP integration. No port conflicts, no firewall issues, no HTTP overhead. |
| 7 | **Individual-first** | Simpler architecture, faster iteration. Team features (shared knowledge bases, multi-repo) are valuable but not MVP-critical. The `.context/` files can be committed to git for informal sharing. |
| 8 | **Hybrid input (auto + manual)** | Pure automation misses context that only humans know (business reasons, compliance requirements, tribal knowledge). Pure manual defeats the purpose. The hybrid approach captures both. |

---

## 11. Future Roadmap (Post v1.0)

| Version | Feature | Description |
|---------|---------|-------------|
| v1.1 | **Advanced MCP** | Streaming responses, subscription to knowledge updates, prompt templates |
| v1.2 | **Onboarding Mode** | Auto-generated project walkthrough for new developers, based on PCKE's accumulated knowledge |
| v2.0 | **Multi-repo Intelligence** | Cross-pollinate patterns and lessons across related repositories |
| v2.x | **Local Vector Search** | Embedding-based similarity search using local models (no API). Replace or augment BM25 |
| v2.x | **IDE Extensions** | VS Code extension for inline PCKE annotations and knowledge previews |

---

## 12. Open Questions

| # | Question | Impact | Status |
|---|----------|--------|--------|
| 1 | Should `.pcke/pcke.db` be committed to git or `.gitignore`'d by default? | Team sharing vs. personal context | Open |
| 2 | How to handle monorepos with multiple logical projects? | Scan scope, knowledge partitioning | Open |
| 3 | What's the maximum repo size PCKE should handle performantly? | Architecture constraints for scan/index | Open |
| 4 | Should `pcke scan` run automatically on git hooks (post-commit, post-merge)? | Freshness vs. developer friction | Open |
| 5 | How to version the knowledge schema for upgrades between PCKE versions? | Migration strategy | Open |

---

### End of PRD

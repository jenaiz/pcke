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

# Federation — Multi-Repo Intelligence

pcke supports federating knowledge bases from multiple repositories into a unified view. Each repository keeps its own `.pcke/` database — federation is a **read-time overlay** with no shared store.

## Setup

### 1. Initialize the federation manifest

The federation manifest lives at `~/.config/pcke/federation.toml` (XDG standard). It's created automatically when you add the first repo.

### 2. Add repositories

```bash
pcke federation add backend-api /path/to/backend-api
pcke federation add frontend-app /path/to/frontend-app
pcke federation add shared-lib /path/to/shared-lib
```

Each repo must have been initialized with `pcke init` and scanned with `pcke scan`.

### 3. Verify health

```bash
pcke federation list
```

Output:
```
Federation: my-org

  ✓ backend-api
    Path: /path/to/backend-api
  ✓ frontend-app
    Path: /path/to/frontend-app
  ✓ shared-lib
    Path: /path/to/shared-lib
```

### 4. Remove a repository

```bash
pcke federation remove frontend-app
```

## Cross-Repo Queries

Execute pcke DSL queries across all federated repos:

```bash
pcke federation query "FROM nodes WHERE type = \"function\""
pcke federation query "FROM nodes WHERE module = \"internal/kdb\"" --format=json
pcke federation query "FROM constraints" --timeout=5s --concurrency=8
```

Results are merged and annotated with `_repo` provenance (which repo each result came from).

### Partial failures

If one repo can't be opened or queried (e.g., moved, locked, corrupted), the query still returns results from healthy repos and reports the failure:

```
Queried 3 repos, 42 results (1 partial failure)

  [backend-api] HandleUsers (cmd/api)
  [shared-lib] AuthMiddleware (pkg/auth)
  ...

Errors:
  frontend-app: open /old/path: no such file or directory
```

### MCP tool

The `query_federation` MCP tool provides the same functionality for AI assistants:

```json
{
  "name": "query_federation",
  "arguments": {
    "query": "FROM nodes WHERE type = \"function\"",
    "repos": "backend-api,shared-lib",
    "limit": 20
  }
}
```

## Dependency Detection

pcke detects cross-repo dependencies by analyzing import statements.

### Supported languages

| Language | Detection method |
|----------|-----------------|
| Go | `import "github.com/<org>/<other-repo>/..."` matched against federated repo module paths |

### Usage

```bash
pcke federation deps
pcke federation deps --module=pkg/auth
pcke federation deps --format=json
```

Output:
```
Cross-repo dependencies (3):

  cmd/main.go → shared-lib/pkg/auth
    import: github.com/org/shared-lib/pkg/auth (via go-import)
  internal/handler.go → shared-lib/pkg/http
    import: github.com/org/shared-lib/pkg/http (via go-import)
  internal/client.go → backend-api/pkg/client
    import: github.com/org/backend-api/pkg/client (via go-import)
```

### MCP tool

```json
{
  "name": "get_cross_repo_deps",
  "arguments": {
    "module": "pkg/auth",
    "direction": "incoming"
  }
}
```

### How it works

1. For each federated repo, pcke reads its `go.mod` to determine the Go module path
2. During scan of the local repo, Go import statements are matched against known module paths
3. Matches are recorded as `CrossRepoDep` edges with source and target repo/module
4. Deps can be stored in the local DB under the `fr:` (federation_relations) prefix

## Shared Constraints

Define org-wide constraints in the federation manifest. These are checked locally during `pcke scan`.

### Manifest format

```toml
[constraints]
[[constraints.rules]]
scope = "all"
severity = "must"
description = "No direct DB access outside repository boundary"

[[constraints.rules]]
scope = "api"
severity = "should"
description = "All public API endpoints must have OpenAPI annotations"
```

### Scope values

| Scope | Applies to |
|-------|-----------|
| `all` | All nodes in every repo |
| `api` | Nodes in modules containing "api" |

### Severity levels

| Severity | Meaning |
|----------|---------|
| `must` | Critical — should be fixed immediately |
| `should` | Advisory — flag for review |

### Check violations

```bash
pcke federation constraints
```

Output:
```
Org-wide constraints (2):

  [must] all — No direct DB access outside repository boundary
  [should] api — All public API endpoints must have OpenAPI annotations

Violations (1):

  ⚠ db-import: Direct DB access detected in internal/database
```

### Propagation

Org constraints are propagated to the local DB during `pcke scan` so they're visible in local queries. They're stored under the `oc:` prefix.

## Troubleshooting

### Stale repos

If a federated repo has been moved or deleted:
```bash
pcke federation list  # shows ✗ for invalid repos
pcke federation remove stale-repo
pcke federation add stale-repo /new/path
```

### Timeouts

For large repos or slow storage:
```bash
pcke federation query "FROM nodes" --timeout=30s
```

### Partial failures

Federation queries are designed to be resilient. If one repo fails:
- Results from healthy repos are still returned
- Failed repos are listed in the errors section
- The query does NOT fail entirely

### Lock contention

Each repo's DB is opened independently with its own file lock. If a repo is locked (e.g., another `pcke scan` is running), the federation query will report it as a partial failure and continue.

### Memory usage

Federation queries open each repo's DB file independently. For large federations (10+ repos), use `--concurrency` to limit parallel DB opens:
```bash
pcke federation query "FROM nodes" --concurrency=2
```

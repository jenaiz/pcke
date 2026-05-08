# Graph Guide — Exploring Code with `pcke`

This guide shows what you can do with the typed-event graph that
`pcke scan` populates as of v0.10.0. Each section is a real workflow
expressed as a few commands you can copy-paste.

> **First time?** Run `pcke scan` (or `pcke scan --deep` for AST-level
> entities) before any of these. Scan time also runs the decision
> backfill, so `docs/adr/*.md` files and `@pcke-rule` annotations land
> in the graph automatically.

## 1. Who depends on this file?

A common refactoring question — "if I change this, what breaks?".

```bash
pcke graph impact e:internal/kdb/btree/btree.go --depth=3
```

`impact` is reverse traversal with a higher default depth. Output is
one ref per line, alphabetically sorted, plus a count on stderr:

```
e:cmd/pcke/commands.go
e:internal/analysis/scanner.go
e:internal/kdb/db.go
e:internal/kdb/event/store.go
e:internal/kdb/migrate/v0011_kn_to_e.go

5 result(s)
```

Pipe to `xargs grep` to inspect the call sites:

```bash
pcke graph impact e:internal/kdb/btree/btree.go | sed 's/^e://' | xargs grep -l "btree\\."
```

## 2. What does this file pull in?

Forward traversal — useful when reading unfamiliar code:

```bash
pcke graph neighbors e:cmd/pcke/main.go --depth=2
```

Add `--edge-type=imports` to ignore decision-link edges and stay on
the dependency graph:

```bash
pcke graph neighbors e:cmd/pcke/main.go --depth=2 --edge-type=imports
```

## 3. What rules apply here?

Decisions are typed assertions with severity (`must` / `should` /
`may`) and scope (`file` / `module` / `global`). The scan-time
backfill populates them from three sources:

```bash
pcke decision list                        # all decisions
pcke decision list --source=adr           # only ADR files
pcke decision list --source=annotation    # only @pcke-rule annotations
pcke decision list --source=commit        # only "decision:"-prefixed commits
pcke decision list --severity=must        # only the must-respect ones
```

Drill into one:

```bash
pcke decision show adr:0008-context-graph-pivot
```

Output:

```
ID:        adr:0008-context-graph-pivot
Title:     ADR-0008: Context Graph Pivot, Versioning Reset, and Scope Pruning
Severity:  must
Scope:     global
Source:    adr
Lifecycle: active
Version:   1
CreatedAt: 2026-05-04T10:17:30Z

# ADR-0008: Context Graph Pivot, Versioning Reset, and Scope Pruning
…full ADR body…
```

## 4. How has this changed?

Walk the version chain of any entity, decision, or link:

```bash
pcke history e:internal/kdb/db.go
```

Output (oldest → newest):

```
v1     2026-05-04T08:55:17Z  active      file db.go (internal/kdb/db.go)
v2     2026-05-08T09:14:00Z  active      file db.go (internal/kdb/db.go)

2 version(s)
```

The `:v<N>` suffix is auto-stripped, so you can paste any versioned
key:

```bash
pcke history e:internal/kdb/db.go:v2     # same as above
pcke history d:adr-0008-context-graph-pivot
```

## 5. Time-travel — what was the graph like a month ago?

`AS OF` pins reads to a moment in the past. Edges that didn't yet
exist (or were tombstoned by then) are skipped.

```bash
pcke graph neighbors e:cmd/pcke/main.go --as-of=2026-04-01
pcke graph impact e:internal/kdb/btree --as-of=2026-04-01 --depth=3
```

Useful for:

- "When did X start depending on Y?" — bisect AS OF dates
- "What was the architecture before the refactor?"
- "Which decisions were active when this commit was authored?"

## 6. The DSL form (for scripting)

Everything above also works as a query, with sort + limit:

```bash
pcke query "nodes WHERE TRAVERSE(edges, depth=2, edge='imports') FROM 'e:internal/kdb/db.go' ORDER BY ref ASC LIMIT 20"

pcke query "nodes WHERE TRAVERSE(edges, depth=3, direction=reverse) FROM 'e:internal/kdb/btree' AS OF '2026-04-01'"
```

See [Query Language](query-language.md) for the full grammar and the
list of recognised arguments.

## 7. Audit / compliance pattern

> "Show me every must-severity decision in this repo, grouped by source."

```bash
pcke decision list --severity=must
```

Combine with shell tools:

```bash
pcke decision list --severity=must \
  | awk '{print $3}' \
  | sort | uniq -c | sort -rn
```

```
   8 adr
   3 annotation
   1 commit
```

## 8. Adding decisions yourself

The scan-time backfill harvests decisions automatically, but you can
also seed them manually using the existing `pcke note add` command (in
v0.10+ those are migrated into Decision events with `source=manual`).

For a new ADR:

1. Drop a markdown file under `docs/adr/NNNN-title.md`.
2. Run `pcke scan`.
3. The next `pcke decision show adr:NNNN-title` reflects it.

For an in-code rule:

```go
// @pcke-rule must-validate-input: every API boundary validates incoming data.
func handler(w http.ResponseWriter, r *http.Request) { … }
```

After `pcke scan`, the rule is queryable as
`d:rule:must-validate-input` with `severity=must`, `scope=file`,
`source=annotation`, body anchored at the file:line.

## 9. Combining with the MCP server

When `pcke serve` is running, AI agents can query the same graph
through the MCP protocol. The graph subgraph for a touched file lands
naturally in the MCP context, so an agent reviewing `internal/kdb/db.go`
sees its imports, its reverse-deps, and the decisions linked to its
module — without needing to run any of the commands above explicitly.

See [Advanced MCP Features](advanced-mcp.md) for the protocol details
and [Architecture](architecture.md) for the typed-event log design.

## What's queryable

| Prefix | Kind | Created by |
|---|---|---|
| `e:` | Entity (file, function, type, module) | `pcke scan` (also migrated from legacy `kn:`) |
| `d:` | Decision | scan-time backfill (ADRs, `@pcke-rule`, commit messages) |
| `l:` | Link (forward edge) | `pcke scan` (also migrated from legacy `rel:`) |
| `lr:` | Reverse-link index | maintained automatically alongside `l:` |
| `o:` | Observation | reserved (Phase 14) |
| `x:` | Outcome | reserved (Phase 14) |

The legacy collections (`kn:`/`rel:`/`nt:`/`el:`) are still readable
through `pcke query nodes …` for backward compatibility, but new work
should target the typed-event log via `pcke graph` / `pcke decision` /
`pcke history` or the DSL `TRAVERSE` / `AS OF` clauses.

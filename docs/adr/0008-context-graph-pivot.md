# ADR-0008: Context Graph Pivot, Versioning Reset, and Scope Pruning

> **Status:** Accepted
> **Date:** 2026-05-04
> **Authors:** jenaiz
> **Supersedes:** Implicitly supersedes the roadmap sections of PRD v3.1, v4.0, v4.1, and v5.0
> **Amended by:** [ADR-0009](0009-durable-memory-corrections.md) (§3 retag procedure; §4.2 embeddings stance)

## Context

PCKE was conceived as a code knowledge engine optimized for AI agents. Through PRDs v1.0–v5.0 it accreted a custom storage engine (`kdb`), AST extraction, MCP server, query DSL, file watcher, interactive shell, federation across repos, schema migration, onboarding mode, and a planned local-embeddings layer.

In May 2026, after publishing `v2.0.0` (Phase 8 — Multi-Repo Intelligence), it became clear that despite the technical depth, **end-user value was thin**:

- Agents received generic dumps, not relevant context.
- `recall` was substring matching, not even BM25.
- Relations (`rel:`) were stored but not traversable.
- Decisions (`nt:`) were not linked to code.
- EvolutionLog (`el:`) tracked only file renames.

Concurrent investigation of HydraDB (a commercial "context infrastructure for agentic AI" product) crystallized a sharper thesis: the right model for agent context is not retrieval over flat indexes but **a graph of code, decisions, constraints, and evolution that the agent queries and mutates as state**.

PCKE already had the right primitives in `kn:`, `rel:`, `el:`, `nt:` — but treated them as flat collections instead of a graph. Vector similarity, federation, and IDE extensions were premature without graph traversal as the foundation.

## Decision

### 1. Pivot the product thesis

PCKE is repositioned as the **open-source local-first counterpart of HydraDB for code**: a code context graph database that AI agents use as durable state. The unit of value is no longer "ranked chunks served on demand" but "a navigable graph of code state with a real timeline and explicit decisions."

Tagline: *Code Context Graph, not a vector dump.*

### 2. New roadmap (PRD v5.1) — four phases to v1.0.0

| Phase | Name | Target version |
|-------|------|----------------|
| 12 | Graph Foundation | `v0.10.0` |
| 13 | Context Graph Queries | `v0.11.0` |
| 14 | Agentic State | `v0.12.0` |
| 15 | Workflow Awareness | `v0.13.0` |
| — | **First stable release** | **`v1.0.0`** |

`v1.0.0` is reserved for the completion of PRD v5.1. Until then, every release is `v0.x` to communicate that the public API and storage format may shift.

### 3. Versioning reset

Existing remote tags `v1.0.0` through `v2.0.0` predate the pivot and the v1.0 stability commitment. They are renamed to `v0.x` to align with the new policy.

| Old tag | New tag | Phase / scope |
|---------|---------|---------------|
| `v1.0.0` | `v0.4.0` | Phase 4 — v1 release (pre-pivot) |
| `v1.1.0` | `v0.5.0` | Phase 5 — Advanced MCP |
| `v1.1.1` | `v0.5.1` | Phase 5 patch |
| `v1.2.0` | `v0.6.0` | CLI completeness & DX |
| `v1.2.1` | `v0.6.1` | Distribution channels |
| `v1.3.0` | `v0.7.0` | Phase 6 — Onboarding |
| `v1.4.0` | `v0.8.0` | Phase 7 — Schema Evolution |
| `v2.0.0` | `v0.9.0` | Phase 8 — Federation (now deprecated) |

Tags `v0.0.0`, `v0.0.0-phase-minus-1`, `v0.1.0`, `v0.2.0`, `v0.3.0` are already below 1.0 and remain unchanged.

**Migration procedure** (executed by maintainer, requires confirmation — does not run automatically):

```sh
# For each (old, new) pair:
git tag <new> <old>^{}             # create new tag at same commit
git tag -d <old>                   # delete local old tag
git push origin <new>              # push new tag
git push origin :refs/tags/<old>   # delete remote old tag
```

After retag:
- GitHub Releases must be re-created against the new tags (or updated in place).
- Homebrew tap formula bumps to `v0.9.0` (last published) until `v0.10.0` ships.
- `go install github.com/jenaiz/pcke/cmd/pcke@v1.x` URLs become invalid; `@latest` and `@v0.x` work. README updated accordingly.
- Docker images on `ghcr.io/jenaiz/pcke` are republished with new tag aliases; old tags can remain as immutable history but new pulls use `v0.x`.

This is destructive for downstream consumers; it is justified because PCKE has not yet committed to API stability and the pivot is large enough that v1.x labels would mislead users into expecting forward compatibility that does not hold.

### 4. Scope pruning

Two existing scopes are removed from active development to reduce maintenance surface and keep focus on the pivot.

#### 4.1 Phase 8 — Federation (multi-repo): **Deprecated, retained, frozen**

The `internal/federation/*` package and its CLI commands (`pcke federation manifest`, `pcke federation query`, etc.) remain in the binary for backward compatibility but are marked deprecated and receive no new features. They will not be ported to the new graph model. Rationale:

- Federation assumed teams sharing context across repos; the post-pivot audience is single developers with a local context graph.
- Cross-repo graph traversal is an order of magnitude harder and was never used in anger.
- Removing the code now is premature (some users may have it wired up); removing it after Phase 15 is acceptable if no demand surfaces.

A follow-up ADR may remove the package after `v1.0.0` if usage signals confirm zero adoption.

#### 4.2 Phase 9 — Local Embeddings: **Parked permanently**

The `feature/local-embeddings` branch (or equivalent) is preserved but **not merged**. It will not appear in `v0.x` or `v1.0.0`. Rationale:

- The graph thesis treats similarity as one signal among many, not as the primary retrieval mechanism. Phases 13–15 deliver precision through traversal, recency, severity, and workflow — not vectors.
- Vector indexes add binary size, CGO surface (depending on runtime), and a maintenance burden that competes with the graph work.
- If a future use case demands semantic search, embeddings can be re-introduced as an optional re-ranker scoped to a subgraph (post-`v1.0`).

PRDs v3.1, v4.0, and v4.1 references to "Phase 9 — Local Embeddings" are considered superseded by this ADR.

### 5. PRD freeze policy clarification

PRD v5.0 is preserved as historical record. PRD v5.1 is the active source of truth (later superseded by PRD v5.2 per ADR-0009). PRDs are maintained as internal documents and are not published in this repository.

## Consequences

### Positive

- Clear narrative: PCKE has a defensible position (local-first, deterministic, code-specific, graph-native) distinct from HydraDB, vector DBs, and code-search products.
- Smaller maintenance surface: deprecating Federation and parking Embeddings removes two large code paths from the active roadmap.
- Honest versioning: `v0.x` accurately reflects the current state of API and storage stability; `v1.0.0` becomes a meaningful event.
- Each phase 12–15 builds linearly on the previous; no parallel speculative tracks.

### Negative

- Retagging breaks downstream installs. Affected users must update install commands and bookmarks. Mitigation: clear release notes, install.sh updated, Homebrew tap re-pinned.
- Dropping federation may disappoint the (small) audience that adopted it. Mitigation: code retained, deprecation notice, possibility of a community-maintained fork.
- Reverting to `v0.x` may look like a regression to outsiders. Mitigation: messaging emphasizes the deliberate v1.0 commitment.
- Embedding work invested in the parked branch is shelved. Mitigation: branch retained; reusable when (and if) needed.

### Neutral

- All existing storage on disk remains compatible. No data migration is required for end users; only tag/branch references change.
- The `internal/vector` directory (if it exists in tree) should be removed from `main` and live only in the parked branch to prevent accidental imports from new phases.

## Verification

- ADR merged before any Phase 12 code lands.
- PRD v5.1 references this ADR in its Pre-flight section.
- Retag procedure executed by maintainer with explicit confirmation (not by automation).
- `go.mod` and any `go install` references in README, `install.sh`, `Dockerfile`, Homebrew formula updated to `v0.9.0` (current stable) before Phase 12 starts.
- A `DEPRECATED.md` notice or in-binary warning is added when `pcke federation *` commands are invoked (Phase 12 task).
- The parked embeddings branch is documented in `CONTRIBUTING.md` so contributors do not duplicate work.

## References

- PRD v5.0 — User Value Phases (historical, superseded by v5.1)
- PRD v5.1 — Context Graph (active)
- HydraDB manifesto: <https://hydradb.com/manifesto>
- Memory note: `/memories/session/plan.md` (drafting notes for this pivot)

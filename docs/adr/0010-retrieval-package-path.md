# ADR-0010: Subgraph Retrieval Lives in `internal/retrieval`, Not `internal/context`

> **Status:** Accepted
> **Date:** 2026-06-26
> **Authors:** jenaiz
> **Amends:** PRD v5.2 §4.6, §4.7, §5.6, §5.7 (package paths only)
> **Does not supersede** ADR-0008 or ADR-0009. The pivot thesis, phase plan,
> and durable-memory corrections stand. This ADR records a naming correction
> between the PRD and the shipped implementation.

## Context

PRD v5.2 ("Durable Code Memory") specifies the Phase 13 subgraph-retrieval and
Phase 14 session code under an `internal/context` package:

- §4.6 — `internal/context/session` for the `Session` interface.
- §4.7 — task `F13.T1` names `internal/context/engine.go`.
- §5.6 / §5.7 — `F14.T3` replaces the in-memory session impl; coverage gate
  targets `internal/context/session`.

The implementation did **not** adopt that path. It shipped under
`internal/retrieval`:

| PRD v5.2 path | Shipped path |
|---|---|
| `internal/context/engine.go` | [`internal/retrieval/engine.go`](../../internal/retrieval/engine.go) |
| `internal/context/session` | [`internal/retrieval/session/`](../../internal/retrieval/session) |
| (budget / score / workflow / recipe) | `internal/retrieval/{budget,score,workflow,recipe}.go` |

Reasons the implementation diverged from the PRD name:

1. **`context` collides with the standard library.** A first-party package named
   `context` shadows `context.Context` at every import site and forces awkward
   aliasing. Go style guidance discourages naming packages after stdlib
   packages.
2. **`retrieval` describes the responsibility.** The package assembles, ranks,
   and budgets subgraphs for serving — it is the retrieval layer, distinct from
   the storage layer (`internal/kdb`) and the MCP transport (`internal/mcp`).
3. **No behavioural change.** Only the package name moved. The `Session`
   interface, ranking formula (§4.3), budgeting, and the in-memory → persistent
   session swap (Phase 14) are all present, under `internal/retrieval` and its
   `session/` subpackage. Persistent sessions live in
   [`internal/retrieval/session/persistent.go`](../../internal/retrieval/session/persistent.go),
   backed by the observation collector in
   [`internal/observe`](../../internal/observe).

## Decision

`internal/retrieval` is the authoritative location for subgraph retrieval,
ranking, budgeting, sessions, workflow detection, and recipes. The PRD v5.2
references to `internal/context/*` are read as `internal/retrieval/*`.

Per the ADR convention (ADR-0009), this correction is recorded as a new ADR
rather than by editing the frozen PRD. The PRD remains the historical record of
the plan-at-the-time; this ADR captures the as-built reality.

Coverage gates from PRD §4.8 / §5.7 apply to `internal/retrieval` and
`internal/retrieval/session` instead of `internal/context/*`.

## Consequences

- Documentation, onboarding notes, and agent instructions reference
  `internal/retrieval`. [`CLAUDE.md`](../../CLAUDE.md) already lists the
  shipped layout.
- No migration, no import rewrite, no API change for downstream consumers —
  `internal/` is unstable pre-1.0 (ADR-0008) and the package was never exported
  under the `context` name.
- Future PRDs should reference `internal/retrieval` directly.

## Alternatives considered

1. **Rename the package to `internal/context` to match the PRD.** Rejected:
   reintroduces the stdlib `context` collision for no benefit, and churns a
   working, tested package purely to satisfy a planning document.
2. **Edit PRD v5.2 in place.** Rejected: PRDs are frozen; per ADR-0009 the
   amend-vs-supersede convention requires corrections to land as new ADRs so the
   original plan stays auditable.
3. **Leave the divergence undocumented.** Rejected: it already caused reader
   confusion (the PRD path does not exist on disk). An explicit record prevents
   future readers from hunting for a non-existent package.

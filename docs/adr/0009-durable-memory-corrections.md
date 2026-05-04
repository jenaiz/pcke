# ADR-0009: Durable Code Memory — Corrections to ADR-0008

> **Status:** Accepted
> **Date:** 2026-05-04
> **Authors:** jenaiz
> **Amends:** [ADR-0008 — Context Graph Pivot](0008-context-graph-pivot.md) (§3 procedure, §4.2)
> **Does not supersede** ADR-0008. The pivot thesis, phase plan, version mapping, and federation deprecation in ADR-0008 stand. Only the two sections listed below change.

## Context

PRD v5.1 (Context Graph) was drafted on top of ADR-0008. Engineering review surfaced two concrete problems with ADR-0008 as written:

1. **The retag procedure (§3) violates Go module immutability.** `proxy.golang.org` caches every published module version forever. Deleting `v1.x` tags from `origin` does not invalidate proxy cache entries or downstream `go.sum` records. Anyone who has resolved `github.com/jenaiz/pcke@v1.x` continues to receive the cached content; deleting the tag breaks `go list -m -versions` and confuses tooling without removing the version. The procedure as documented could produce silent breakage in downstream builds.

2. **§4.2 "Local Embeddings parked permanently" is too categorical.** Engineering review of the agentic-memory category (HydraDB, mem0, Graphlit, Cortex) shows that the leading designs are **hybrid graph + vector substrates**, not graph-only. PCKE has good reasons to ship a graph-only default (binary size, no CGO, deterministic), but committing to "no embeddings, ever" forecloses an obvious post-1.0 design (vectors as a re-ranker scoped to a graph-extracted subgraph) without a real cost-benefit case.

PRD v5.2 reframes the project around "durable code memory" and proposes corrections to both points. This ADR ratifies those corrections.

A secondary purpose of this ADR is to set the precedent that **specific-section amendments to earlier ADRs are written as new "amending" ADRs**, not by editing the original. The original stays as the historical record of the decision-at-the-time; the amendment captures what changed and why.

## Decision

### 1. Replace the retag procedure in ADR-0008 §3

The destructive `git tag -d <old>` / `git push origin :refs/tags/<old>` steps are removed. They are replaced with:

```sh
# 1. Cut new v0.x tags at the same commits as the old v1.x/v2.0.0 ones.
git tag v0.4.0 v1.0.0^{}
git tag v0.5.0 v1.1.0^{}
git tag v0.5.1 v1.1.1^{}
git tag v0.6.0 v1.2.0^{}
git tag v0.6.1 v1.2.1^{}
git tag v0.7.0 v1.3.0^{}
git tag v0.8.0 v1.4.0^{}
git tag v0.9.0 v2.0.0^{}
git push origin v0.4.0 v0.5.0 v0.5.1 v0.6.0 v0.6.1 v0.7.0 v0.8.0 v0.9.0

# 2. Old tags are NOT deleted. They remain in history as immutable provenance.
```

Add a `retract` block to `go.mod` so `go list -m -versions github.com/jenaiz/pcke` and module-aware tooling surface the old `v1.x` versions as withdrawn:

```go
retract (
    v1.0.0 // Pre-pivot. Use v0.4.0.
    v1.1.0 // Pre-pivot. Use v0.5.0.
    v1.1.1 // Pre-pivot. Use v0.5.1.
    v1.2.0 // Pre-pivot. Use v0.6.0.
    v1.2.1 // Pre-pivot. Use v0.6.1.
    v1.3.0 // Pre-pivot. Use v0.7.0.
    v1.4.0 // Pre-pivot. Use v0.8.0.
)
```

**`v2.0.0` is intentionally absent from the retract block.** Go's module rules require versions ≥ v2 to declare a `/v2` suffix in the module path; since `github.com/jenaiz/pcke` carries no such suffix, `go` rejects any retract for `v2.0.0` (`should be v0 or v1, not v2`). The tag was already unreachable via `go install` from the moment it was published — only binary distributions (Homebrew, install.sh, GitHub Releases) ever delivered it. The `v0.9.1` release notes call out v2.0.0's supersession by `v0.9.0` so those consumers know to switch.

Effect:

- Existing `go.sum` entries for `v1.x` continue to verify (content unchanged).
- New `go install` invocations against `@v1.x` succeed but `go` warns about retraction.
- `@latest` resolves to the highest non-retracted version (`v0.9.0` until Phase 12 ships).
- Downstream consumers can pin to `v0.9.0` (or any new tag) at their pace.

GitHub Releases corresponding to the old tags are kept and marked as superseded in their notes (not deleted), preserving release-page provenance.

### 2. Soften ADR-0008 §4.2 — embeddings deferred, not banned

Replace "Phase 9 — Local Embeddings: Parked permanently" with:

> **Phase 9 — Local Embeddings: deferred from the default build; admitted as opt-in for v0.11+.**
>
> Embeddings will not appear in the default `pcke` binary or the default config. PRD v5.2 §4.4 specifies an opt-in re-ranker behind a `-tags=rerank` build tag, scoped to operate over a subgraph already extracted by graph traversal — never as a primary retrieval mechanism. This preserves provenance (every served fact is graph-reachable) while leaving the door open for semantic re-ordering when a user opts in.
>
> The `feature/local-embeddings` branch remains preserved. Any work it contained may be revisited as the source for the opt-in implementation. PRDs v3.1, v4.0, v4.1 references to "Phase 9 — Local Embeddings" as a default-on phase remain superseded; the **default-off, opt-in** treatment in PRD v5.2 §4.4 is the active design.

### 3. ADR amendment convention (precedent)

Going forward:

- **Amendment ADR** — when specific sections of an earlier ADR need correction and the rest still holds. The amending ADR (this one) lists which sections it modifies and why; the original ADR receives a one-line `> **Amended by:** ADR-NNNN (§X.Y)` reference at the top, but its body is not edited.
- **Superseding ADR** — when the original decision is wrong end-to-end. The original's status flips to `Superseded by ADR-NNNN`; readers of the old ADR get redirected to the new one.

The ADR template (`0000-adr-template.md`) will gain an `Amends:` field alongside `Supersedes:` to make this explicit. (Trivial follow-up; not gated by this ADR.)

### 4. Active source-of-truth update

PRD v5.2 is the active source of truth. PRD v5.1 joins PRD v5.0 as historical record. (PRDs are maintained as internal documents and are not published in this repository.)

ADR-0008 §5 (PRD freeze policy) is updated implicitly by this clause: any further deviation from PRD v5.2 still requires a new ADR.

## Consequences

### Positive

- Downstream Go consumers do not silently break. `retract` is the idiomatic way to express "this version was wrong"; tooling already understands it.
- The opt-in re-ranker spec gives PCKE a credible answer to "does it find semantically similar code that doesn't share imports?" without compromising the deterministic default build.
- The amendment pattern keeps ADR history readable. Future maintainers can see ADR-0008 *as decided* plus the targeted corrections, instead of an edited document with no record of the change.

### Negative

- Two ADRs to read instead of one. Mitigated by the `Amended by` cross-reference at the top of ADR-0008.
- The opt-in build tag (`rerank`) adds a CI matrix dimension. Mitigated by a single smoke build in CI; full coverage not required for the optional path until adoption justifies it.

### Risks

| Risk | Mitigation |
|------|------------|
| `retract` directives confuse very old `go` versions | Affects `go` < 1.16; documented in release notes; install.sh recommends `go ≥ 1.21` already |
| Amendment pattern is misused (used to dodge writing a proper supersede) | Code review on ADRs; if amendments accumulate >2 per parent ADR, supersede instead |
| Opt-in re-ranker becomes default-by-stealth via config sprawl | PRD v5.2 §4.4 fixes default-off; any change requires a new ADR |

## Alternatives Considered

1. **Supersede ADR-0008 wholesale with ADR-0009.**
   Rejected: ADR-0008's pivot thesis, phase plan, version-mapping table, and federation deprecation are all still correct. A wholesale supersede would force re-litigating decisions that don't need re-litigating, and would lose the historical record of *what was decided when*.

2. **Edit ADR-0008 in place, add a "Changelog" section.**
   Rejected: ADRs are meant to be immutable records of decisions at a point in time. Editing them in place obscures *why* the decision changed. The amendment pattern is more honest.

3. **Keep ADR-0008's destructive retag and accept the breakage.**
   Rejected: there is no upside. `retract` achieves the user-facing message ("v1.x is wrong, use v0.x") without breaking module proxies or downstream `go.sum` entries.

4. **Ship embeddings on by default in v1.0.0 (HydraDB-style hybrid).**
   Rejected: increases binary size, adds CGO surface, and validates a design choice (semantic similarity for code) that has no usage signal yet. Opt-in is the conservative midpoint.

## Verification

- ADR-0008 receives an `> **Amended by:** ADR-0009 (§3 procedure, §4.2)` line at the top.
- `go.mod` `retract` block is added in Phase 11 (PRD v5.2 §2).
- Phase 11 acceptance criterion (PRD v5.2 §2.5 F11.T1) verifies `go list -m -versions` surfaces the retraction.
- The `-tags=rerank` build path is exercised by a CI smoke build (PRD v5.2 §4.8 F13.T7).
- The ADR template is updated with an `Amends:` field as a follow-up housekeeping change.

## References

- [ADR-0008 — Context Graph Pivot](0008-context-graph-pivot.md) (active; amended by this ADR)
- PRD v5.2 — Durable Code Memory (internal)
- PRD v5.1 — Context Graph (internal, historical)
- Go module retraction: <https://go.dev/ref/mod#go-mod-file-retract>
- Go module proxy immutability: <https://go.dev/ref/mod#module-proxy>

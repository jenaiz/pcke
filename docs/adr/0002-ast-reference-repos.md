# ADR-0002: Reference Repositories for AST Accuracy Validation

> **Status:** Accepted
> **Date:** 2026-04-25
> **Authors:** Jesus Navarrete
> **Supersedes:** —

## Context

Phase 2 introduces tree-sitter-powered AST analysis (F2.T3/T4) for entity
extraction (functions, structs, interfaces, classes) and import-graph
construction. The Execution Plan §6 requires **AST accuracy ≥ 90%** measured
against three reference repositories.

We need to select repos that:

1. Cover the three target languages (Go, JavaScript, Python).
2. Are well-known, stable, and unlikely to change structure drastically.
3. Have a manageable LOC count (< 15 K source lines each) so the test
   harness runs in < 30 s total.
4. Exercise a variety of entity types: exported/unexported functions, structs,
   interfaces (Go), classes/arrow functions (JS), classes/decorators (Python).

## Decision

The following three repositories will serve as the ground-truth corpus for
the `pcke scan --deep` accuracy harness:

| Language   | Repository              | Approx LOC | Why                                                    |
|------------|-------------------------|------------|--------------------------------------------------------|
| Go         | `go-chi/chi`            | ~3 K       | Clean interfaces (`Router`, `Mux`), middleware pattern, idiomatic Go. |
| JavaScript | `expressjs/express`     | ~5 K       | De-facto HTTP framework; mix of factory functions, prototypal methods. |
| Python     | `pallets/flask`         | ~8 K       | Classes, decorators, blueprints; exercises all Python entity types.   |

Each repo will be pinned to a specific release tag at test-time (recorded in
`testdata/ast/repos.json`) so results are reproducible across CI runs.

### Accuracy measurement

For each repo the harness will:

1. Clone at the pinned tag into a temp directory.
2. Run `pcke scan --deep` to extract entities.
3. Compare extracted entities against a hand-curated golden file
   (`testdata/ast/golden/<repo>.json`) containing expected entities with
   name, kind (function/struct/interface/class), and file path.
4. Compute **precision** (correct extractions / total extractions) and
   **recall** (correct extractions / expected entities).
5. Report **F1 score**; the ≥ 90% threshold applies to F1.

## Consequences

### Positive

- Deterministic, reproducible accuracy gate in CI.
- Three languages ensure the tree-sitter grammars are exercised.
- Moderate size keeps the accuracy test under the 30 s budget.

### Negative

- Hand-curating golden files requires one-time manual effort (~2 h).
- Pinned tags may become stale; periodic refresh is needed.

### Risks

- Upstream repos could delete tags. Mitigation: pin to release tags
  (not branch HEADs), and cache clones in CI.
- Entity extraction rules may need language-specific tuning. Mitigation:
  the three-repo spread catches language bias early.

## Alternatives Considered

1. **Single large monorepo** (e.g., Kubernetes) — rejected: too large,
   slow CI, and only covers Go.
2. **Synthetic test repos** — rejected: wouldn't validate real-world
   code patterns and naming conventions.
3. **Five repos** — rejected: marginal accuracy improvement doesn't
   justify the extra golden-file maintenance.

## References

- PRD v3.1: `PRDs/PRD_PCKE_v3_1.md`
- Execution Plan: `PRDs/PRD_PCKE_v3_1_EXECUTION_PLAN.md` §6
- Phase 2 prompt: `.github/prompts/phase-2-deep-analysis-mcp.prompt.md`

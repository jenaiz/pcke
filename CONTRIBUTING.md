# Contributing to pcke

> Project status: **pre-alpha** (Phase −1 bootstrap). The internal API is
> unstable until `v1.0.0`. Read [PRD v3.1](PRDs/PRD_PCKE_v3_1.md) and the
> [Execution Plan](PRDs/PRD_PCKE_v3_1_EXECUTION_PLAN.md) before opening
> non-trivial PRs.

## Quick start

```bash
git clone https://github.com/jesusnavarrete/pcke.git
cd pcke
make install-hooks   # opt-in: pre-commit lint + test-short
make verify          # lint + test + build
```

Required tools:

- Go **1.23+**
- [golangci-lint](https://golangci-lint.run/) v1.61+
- [goreleaser](https://goreleaser.com/) v2 (only for `make release-dryrun`)
- [cosign](https://docs.sigstore.dev/) (only for release signing)

## Workflow

1. Open an issue describing the change. For Phase 0+ work, the issue should
   reference the task ID from the [Execution Plan](PRDs/PRD_PCKE_v3_1_EXECUTION_PLAN.md)
   §4 / §5 DAG (e.g. *Closes #T7 — B+tree Put/Delete/splits*).
2. Branch from `main`: `feature/<phase>-<short-name>` or `fix/<short-name>`.
3. Keep commits atomic and conventional (`feat:`, `fix:`, `docs:`, `test:`,
   `refactor:`, `chore:`).
4. All CI gates must pass. Coverage must not drop more than **2 pp** vs. the
   `main` baseline (gate active from Phase 0). See [Plan §7.4](PRDs/PRD_PCKE_v3_1_EXECUTION_PLAN.md).
5. PRs introducing a design decision not covered by the PRD or the plan
   **must** add an ADR under `PRDs/ADRs/` (template
   `0000-adr-template.md`).

## Code conventions

- Follow standard Go style (`gofumpt`, `goimports`).
- Public API in `internal/kdb/` is governed by Plan §4.5 ("API congelada").
  Breaking changes after Phase 0 close require an ADR.
- Test files live next to the code (`foo.go` ↔ `foo_test.go`).
- Property-based tests use [`pgregory.net/rapid`](https://pgregory.net/rapid).
- Fuzz corpora live in `testdata/fuzz/` under each package.

## DCO / sign-off

Not required at this stage. Will be re-evaluated before `v1.0.0`.

## Reporting security issues

Do **not** open a public issue. Email the maintainer (see `CODEOWNERS`).

# Contributing to pcke

> Project status: **pre-alpha**. The internal API is unstable until `v1.0.0`.
> Read [docs/architecture.md](docs/architecture.md) for an overview before
> opening non-trivial PRs.

## Quick start

```bash
git clone https://github.com/jenaiz/pcke.git
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

1. Open an issue describing the change.
2. Branch from `main`: `feature/<short-name>` or `fix/<short-name>`.
3. Keep commits atomic and conventional (`feat:`, `fix:`, `docs:`, `test:`,
   `refactor:`, `chore:`).
4. All CI gates must pass. Coverage must not drop more than **2 pp** vs. the
   `main` baseline.
5. Significant design decisions should be documented in the PR description.

## Code conventions

- Follow standard Go style (`gofumpt`, `goimports`).
- Public API in `internal/kdb/` is unstable until `v1.0.0`. Breaking changes
  should be called out in the PR description.
- Test files live next to the code (`foo.go` ↔ `foo_test.go`).
- Property-based tests use [`pgregory.net/rapid`](https://pgregory.net/rapid).
- Fuzz corpora live in `testdata/fuzz/` under each package.

## DCO / sign-off

Not required at this stage. Will be re-evaluated before `v1.0.0`.

## Reporting security issues

Do **not** open a public issue. Email the maintainer (see `CODEOWNERS`).

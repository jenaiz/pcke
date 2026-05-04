# Contributing to pcke

> Project status: **v0.9.x — pre-1.0 pivot in progress.** The internal API and
> on-disk format are unstable until `v1.0.0`. Read
> [docs/architecture.md](docs/architecture.md) and the relevant
> [ADRs](docs/adr/) before opening non-trivial PRs.

## Quick start

```bash
git clone https://github.com/jenaiz/pcke.git
cd pcke
make install-hooks   # opt-in: pre-commit lint + test-short
make verify          # lint + test + build
```

Required tools:

- Go **1.23+**
- [golangci-lint](https://golangci-lint.run/) v2
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

## Frozen / parked work

Some directions are intentionally on hold while the v1.0 pivot lands. Don't
duplicate this work in new PRs without checking the relevant ADR first.

### Federation (`internal/federation/`) — frozen

Federation (multi-repo intelligence) is deprecated and receives no new
features. It remains in the binary for backward compatibility. See
[ADR-0008 §4.1](docs/adr/0008-context-graph-pivot.md) for rationale. Removal
after `v1.0.0` is contingent on adoption signals.

### Local embeddings — deferred from default build

A `feature/local-embeddings` branch is reserved for any work on a vector
re-ranker. As of v0.9.1, no such branch exists in the repo — when work
resumes (post-v1.0, or earlier behind the opt-in `-tags=rerank` build path),
it should land on a branch with that name to keep the convention consistent.

The design is specified in
[ADR-0009 §2](docs/adr/0009-durable-memory-corrections.md): the re-ranker is
**off in the default build**, opt-in via `-tags=rerank`, and operates only
over a subgraph already extracted by graph traversal — never as a primary
retrieval mechanism.

Don't add embedding code to the default build. Don't import any embedding or
ONNX dependency from packages that build under the default tag set.

## DCO / sign-off

Not required at this stage. Will be re-evaluated before `v1.0.0`.

## Reporting security issues

Do **not** open a public issue. Email the maintainer (see `CODEOWNERS`).

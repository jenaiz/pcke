# Copilot Instructions — pcke

## Project identity

- **GitHub owner:** `jenaiz`
- **Repository:** <https://github.com/jenaiz/pcke>
- **Go module path:** `github.com/jenaiz/pcke`
- **License:** Apache-2.0

Never use `jesusnavarrete` in URLs or module paths. The correct GitHub
handle is **jenaiz**.

## Key docs

- Architecture & decisions: `PRDs/PRD_PCKE_v3_1.md` (frozen)
- Execution plan: `PRDs/PRD_PCKE_v3_1_EXECUTION_PLAN.md` (frozen)
- Design changes require an ADR in `PRDs/ADRs/`

## Conventions

- Go 1.23+ stdlib only in Phase −1 (no external deps yet).
- Conventional commits: `feat:`, `fix:`, `docs:`, `test:`, `chore:`.
- Lint config: `.golangci.yml` (9 linters, gocyclo ≤ 15).
- Build tags: `kdbdebug` for assertions/crash hooks (never in releases).

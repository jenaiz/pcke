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
- Lint config: `.golangci.yml` **v2 format** (8 linters + gofumpt formatter, gocyclo ≤ 15).
- Build tags: `kdbdebug` for assertions/crash hooks (never in releases).

## Git credentials
- use always the user jenaiz for git operations, never use jesusnavarrete or glb-jesus
- use the email jesus.navarrete@gmail.com por git operations, never use other email in this repository
- all commits must be signed with the a associated email address, never use other email address for signing commits in this repository
# ADR-0004: Documentation Site Strategy

> **Status:** Accepted
> **Date:** 2026-04-26
> **Authors:** jenaiz
> **Supersedes:** —

## Context

Phase 4 requires a comprehensive documentation site covering architecture,
API reference, getting started guide, query language, and annotations.

The project uses Go 1.23+ stdlib only (no external deps in Phase −1 scope).
We need a documentation strategy that:

- Is simple to maintain alongside code.
- Requires no external tooling beyond what Go developers already have.
- Can be hosted on GitHub Pages with zero infrastructure.

## Decision

Use **Markdown files in `docs/`** served via GitHub Pages with Jekyll's
default rendering. No custom static site generator or theme.

Directory structure:

```
docs/
  index.md              — landing page / getting started
  architecture.md       — system architecture (existing, updated)
  api-reference.md      — exported Go API surface
  query-language.md     — query DSL grammar + examples
  annotations.md        — @pcke-rule / @pcke-lesson syntax
  schema-migrations.md  — pcke migrate usage
```

Each document is standalone Markdown with cross-links. `godoc` remains the
canonical API reference; `api-reference.md` provides a curated overview for
non-Go users (e.g., MCP clients).

## Consequences

### Positive

- Zero additional dependencies or build steps.
- GitHub renders Markdown natively; Pages enables a browsable site.
- Contributors can edit docs with any text editor.
- Docs live next to code, making staleness visible in PRs.

### Negative

- No search, sidebar navigation, or versioned docs out of the box.
- Styling is limited to GitHub's default Markdown rendering.

### Risks

- Docs may drift from code. Mitigation: `make verify` includes a docs
  freshness check post-v1 if needed.

## Alternatives Considered

1. **Hugo / mdBook / Docusaurus**: Powerful but adds a build step and
   dependency. Overkill for a single-binary CLI tool at v1.0.
2. **godoc only**: Great for Go API but doesn't cover CLI usage, query
   language, or annotations for non-Go consumers.
3. **GitHub Wiki**: Separate from the repo; harder to keep in sync with code
   changes and not reviewable in PRs.

## References

- PRD v3.1: `PRDs/PRD_PCKE_v3_1.md`
- Execution Plan: `PRDs/PRD_PCKE_v3_1_EXECUTION_PLAN.md` §6 (Phase 4 DoD)

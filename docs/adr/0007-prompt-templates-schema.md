# ADR-0007: Prompt Templates Schema

> **Status:** Accepted
> **Date:** 2026-04-27
> **Authors:** jenaiz
> **Supersedes:** —

## Context

AI agents connecting via MCP need domain-specific context bundles to work effectively with a codebase. Currently, agents must make multiple tool calls (`recall`, `get_module_context`, `get_constraints`) and assemble context manually. Pre-built prompt templates combine multiple context sources into a single, coherent payload optimized for common workflows.

The MCP specification defines a `prompts/` capability where servers expose named prompts with optional arguments. Clients call `prompts/list` to discover available prompts and `prompts/get` to retrieve a resolved prompt.

## Decision

Implement prompt templates in `internal/mcp/templates.go` with the following design:

### Built-in templates

| Template | Purpose | Context sources |
|----------|---------|----------------|
| `onboarding` | New developer orientation | Architecture + conventions + key decisions + top modules |
| `review` | Code review context | Constraints + recent history + module stability |
| `debug` | Debugging assistance | Module context + dependencies + related constraints |
| `refactor` | Refactoring guidance | Architecture + module coupling + stability scores |

### Schema

Each template is exposed as an MCP prompt with:

- **Name**: template identifier (e.g., `onboarding`)
- **Description**: human-readable explanation of when to use it
- **Arguments**: optional parameters to scope the template (e.g., `module` for `debug` and `review`)

### Resolution

Templates are resolved at request time by:

1. Loading relevant data from kdb (nodes, relations, evolution logs)
2. Rendering each section using existing `output.Render*` functions
3. Assembling sections into a sequence of `PromptMessage` objects with `role: user`

### Custom templates

Users can define custom templates in `.pcke/templates/` as TOML files:

```toml
name = "security-review"
description = "Security-focused code review context"
sections = ["constraints", "modules", "history"]
```

Custom templates are discovered at server startup and registered alongside built-in templates. They use the same section renderers as built-ins.

### MCP integration

- Register with `WithPromptCapabilities(false)` (no dynamic list changes at runtime for now)
- Each template becomes a `mcp.Prompt` with a `PromptHandlerFunc`
- `prompts/list` returns all built-in + custom templates
- `prompts/get` resolves and returns the template content

## Consequences

### Positive

- Single MCP call gives agents comprehensive, curated context
- Consistent context format across different agent workflows
- Custom templates allow project-specific adaptation without code changes
- Leverages existing `output.Render*` infrastructure

### Negative

- Template resolution loads data from kdb on each call (no caching)
- Custom template format (TOML) adds a user-facing schema to maintain
- Built-in templates are opinionated about what context is useful

### Risks

- Templates may produce large payloads for big codebases — mitigated by the streaming layer (ADR-0006)
- Custom template TOML schema may need versioning if it evolves

## Alternatives Considered

1. **Resource-based templates** (expose as `pcke://templates/*` resources): Rejected — prompts are the correct MCP abstraction for parameterized context bundles.
2. **Markdown template files**: Rejected — TOML is more structured and avoids ambiguity in section parsing.
3. **Agent-side template assembly**: Rejected — defeats the purpose; agents shouldn't need to know pcke's internal data model.

## References

- PRD v3.1: `PRDs/PRD_PCKE_v3_1.md`
- Phase 5 prompt: `.github/prompts/phase-5-advanced-mcp.prompt.md`
- MCP specification: §prompts
- ADR-0006: MCP Streaming Design

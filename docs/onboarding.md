# Onboarding Mode

pcke can generate interactive, auto-guided walkthroughs combining architecture,
key modules, conventions, constraints, and entry points from the knowledge base.

## CLI Usage

```bash
# Full walkthrough (plain text, paginated on TTY)
pcke onboard

# Markdown output
pcke onboard --format=markdown

# JSON output
pcke onboard --format=json

# Scope to a specific module
pcke onboard --module=internal/kdb

# Shallow mode (first 3 sections only)
pcke onboard --depth=shallow

# Write to ONBOARDING.md file
pcke onboard --output=file
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--format` | `text` | Output format: `text`, `json`, or `markdown` |
| `--module` | (all) | Scope walkthrough to a specific module |
| `--depth` | `full` | `shallow` (3 sections) or `full` (all sections) |
| `--output` | (stdout) | Use `file` to write `ONBOARDING.md` at repo root |

## MCP Tool

The `get_onboarding` tool is available via the MCP server (`pcke serve`).

### Parameters

| Parameter | Required | Description |
|-----------|----------|-------------|
| `section` | No | Filter to a specific section name |
| `module` | No | Scope to a specific module |
| `depth` | No | `shallow` or `full` (default) |

### Example

```json
{
  "method": "tools/call",
  "params": {
    "name": "get_onboarding",
    "arguments": {
      "depth": "shallow"
    }
  }
}
```

## Walkthrough Sections

The generated walkthrough includes these sections (in order):

1. **Project Overview** — file and module counts, languages detected
2. **Tech Stack** — languages with file counts
3. **Architecture** — module structure and relationships
4. **Entry Points** — detected from `cmd/`, `api/`, `main.go`, high fan-in
5. **Key Modules** — ordered by composite complexity score
6. **Conventions** — coding conventions detected from the codebase
7. **Constraints** — engineering rules and requirements
8. **Open Decisions** — architectural decisions not yet resolved

## Complexity Scoring

Modules are ranked by a composite complexity score:

```
Score(module) = 0.30 × fan_in_normalized
             + 0.25 × (1 - avg_stability)
             + 0.20 × churn_rate_normalized
             + 0.15 × file_count_normalized
             + 0.10 × entity_density_normalized
```

- **Fan-in**: count of distinct modules that import this module
- **Stability**: average file stability (lower = more complex)
- **Churn rate**: evolution log entries in the last 90 days
- **File count**: number of files in the module
- **Entity density**: entities per file (from deep scan)

All metrics are min-max normalized to [0, 1]. If entity density is unavailable
(no `--deep` scan), its weight is redistributed proportionally.

## Custom Configuration

Create `.pcke/onboarding.toml` to customize the walkthrough:

```toml
[walkthrough]
title = "Welcome to Our Project"
highlight_modules = ["internal/kdb", "cmd/pcke"]
skip_sections = ["history"]

[[walkthrough.custom_sections]]
name = "Team Conventions"
content = "We use trunk-based development with short-lived feature branches."
position = "after:conventions"
```

### Configuration Reference

| Field | Type | Description |
|-------|------|-------------|
| `walkthrough.title` | string | Custom title (default: "Project Walkthrough") |
| `walkthrough.highlight_modules` | string[] | Modules to display first |
| `walkthrough.skip_sections` | string[] | Section names to omit |
| `walkthrough.custom_sections` | array | User-defined sections |

#### Custom Section Fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Section name/title |
| `content` | string | Markdown content |
| `position` | string | Placement: `after:<section>` or `before:<section>` |

## Entry Point Detection

Files are classified as entry points when:

- `Class == "entry_point"` (files in `cmd/`, `cli/`, `bin/`)
- `Class == "api"` (files in `api/`, `routes/`, `handlers/`, `endpoints/`, `controllers/`)
- `Name == "main.go"`
- High fan-in (many modules depend on them)

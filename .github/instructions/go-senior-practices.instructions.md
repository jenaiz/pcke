---
name: go-senior-practices
description: "Apply Go senior engineer best practices: error handling, concurrency, interfaces, testing, performance gates, logging. Use when: writing Go code, reviewing PRs, fixing lint/test failures, optimizing performance, designing new APIs."
applyTo: "**/*.go"
---

# Go Senior Engineer Best Practices

This instruction wires the **Go Senior Engineer Best Practices** skill into your pcke development workflow. It auto-applies when working with Go files.

## What This Does

When you:
- **Write a new function** → Guides design decisions (error handling, interfaces, concurrency)
- **Fix lint errors** → Applies relevant best practices sections
- **Review a PR** → Suggests checklist items for feedback
- **Optimize performance** → References benchmarking & profiling sections
- **Debug test failures** → Points to testing strategies (table-driven, coverage, race detection)

The skill provides:
- ✅ **8 practice areas**: error handling, concurrency, interfaces, testing, linting, performance, modules, logging
- ✅ **Code examples**: ✓ good vs ✗ bad patterns for every rule
- ✅ **Checklists**: Pre-commit, PR review, feature development workflows
- ✅ **Actionable guidance**: Decision flowchart, invocation scenarios, common pitfalls

## How to Invoke

1. **During implementation:**  
   `"I'm writing a query parser. Apply Go senior practices to guide my design."`

2. **Pre-commit validation:**  
   `"Apply the pre-commit checklist to my changes."`

3. **PR review:**  
   `"Use the Go best practices checklist to review this PR."`

4. **Fix test failures:**  
   `"Help me resolve these test failures using the testing section of the skill."`

5. **Performance tuning:**  
   `"BenchmarkCritical* regressed 12%. Help me profile and optimize."`

## Key Areas

| Area | When to Use |
|------|------------|
| **Error Handling** | Every public function that can fail; mapping to exit codes |
| **Concurrency** | Goroutines, shared state, sync primitives, race detection |
| **Interfaces** | Designing public APIs; accepting flexibility, testing with mocks |
| **Testing** | Coverage targets (88%+), table-driven tests, benchmarks, fuzz |
| **Linting** | Code style, naming, cyclomatic complexity (≤15), no unjustified `//nolint` |
| **Performance** | BenchmarkCritical* paths, <10% regression threshold, profiling |
| **Modules** | Layering (cmd → internal layers), encapsulation, no circular deps |
| **Logging** | Structured slog attributes, auto-redaction, no secrets |

## Pre-Commit Gate

After `make install-hooks`, the pre-commit hook runs:
```bash
make lint && make test-short
```

This instruction **complements** the automation by helping you fix issues *before* they block.

## References

- **Full Skill:** [.github/SKILL_go_best_practices.md](../../.github/SKILL_go_best_practices.md)
- **Project Guide:** [CLAUDE.md](../../CLAUDE.md)
- **Architecture:** [docs/architecture.md](../../docs/architecture.md)
- **Makefile:** `make verify` (lint + test + build)

## Quick Lint Fix Commands

```bash
make format      # gofumpt + goimports
make lint        # golangci-lint v2
make test        # All tests with coverage
make test-race   # Race detector (blocking)
make bench       # Benchmarks (BenchmarkCritical* checked for regression)
```

## Exit Codes (for error handling section)

- `0`: success
- `1`: user error
- `2`: config error
- `3`: lock conflict (ErrDBLocked)
- `4`: corruption (checksum, invalid config)
- `5`: internal error (panic, invariant violation)
- `6`: schema mismatch (DB version incompatible)

See [cmd/pcke/exitcodes.go](../../cmd/pcke/exitcodes.go) for mapping errors to codes.

---

**This instruction applies to all `.go` files in the pcke project.**

If you find gaps or want to refine the skill, run:  
`"Update the Go best practices skill with [specific improvement]"`

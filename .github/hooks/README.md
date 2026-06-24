# Go Best Practices Validation Hooks

This directory contains shell scripts and hook configurations that enforce Go senior engineer best practices before commits and pushes.

## Files

| File | Purpose |
|------|---------|
| `validate-go-practices.sh` | Main validation script (linting, testing, race detection, benchmarks) |
| `go-practices-hooks.json` | Hook configuration (defines lifecycle triggers and blocking behavior) |

## Quick Start

### Manual Validation

Run validation at any time:

```bash
# Fast validation (lint + quick tests) — ~1-2 seconds
./.github/hooks/validate-go-practices.sh quick

# Full validation (lint, test, race, benchmarks) — ~15-30 seconds
./.github/hooks/validate-go-practices.sh full

# Check race conditions only
./.github/hooks/validate-go-practices.sh race

# Check performance regressions only
./.github/hooks/validate-go-practices.sh benchmarks
```

### Integration with Pre-Commit

The existing `.githooks/pre-commit` already runs:
```bash
make lint
make test-short
```

To upgrade it with full validation, update `.githooks/pre-commit`:

```bash
#!/usr/bin/env bash
set -euo pipefail

echo "pre-commit: running Go best practices validation..."
./.github/hooks/validate-go-practices.sh quick

echo "pre-commit: all checks passed."
```

Then reinstall hooks:
```bash
make install-hooks
```

### CI/CD Integration (GitHub Actions)

Create `.github/workflows/go-practices.yml`:

```yaml
name: Go Best Practices

on: [push, pull_request]

jobs:
  validate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.25.5'
      
      - name: Run full validation
        run: ./.github/hooks/validate-go-practices.sh full
      
      - name: Check benchmarks
        run: ./.github/hooks/validate-go-practices.sh benchmarks
        continue-on-error: true  # Informational only
```

---

## Validation Modes

### `quick` (Pre-Commit)
**Purpose:** Fast feedback loop before commit  
**Time:** ~1-2 seconds  
**Checks:**
- ✓ Linting (golangci-lint v2)
- ✓ Fast tests (make test-short)

**When to use:** Before every commit

```bash
./.github/hooks/validate-go-practices.sh quick
```

### `race` (Pre-Push)
**Purpose:** Catch data races before shared branch  
**Time:** ~5-10 seconds  
**Checks:**
- ✓ Linting
- ✓ Full tests
- ✓ Race detector (-race flag)

**When to use:** Before pushing to main or feature branch

```bash
./.github/hooks/validate-go-practices.sh race
```

### `benchmarks` (Performance Gating)
**Purpose:** Prevent performance regressions (>10%)  
**Time:** ~10-15 seconds  
**Checks:**
- ✓ Linting
- ✓ Full tests
- ✓ BenchmarkCritical* regression check

**When to use:** Before merging performance-critical code

```bash
./.github/hooks/validate-go-practices.sh benchmarks
```

### `full` (Pre-Merge)
**Purpose:** Comprehensive validation gate  
**Time:** ~15-30 seconds  
**Checks:**
- ✓ Linting
- ✓ Full tests + coverage (88%+)
- ✓ Race detector
- ✓ BenchmarkCritical* regression

**When to use:** Before merge to main (or in CI)

```bash
./.github/hooks/validate-go-practices.sh full
```

---

## Understanding the Checklist

### 1. Code Style & Linting
**Runs:** `make lint` (golangci-lint v2)  
**Checks:**
- No unused imports
- Correct naming conventions (acronyms capitalized, exported PascalCase)
- No dead code or unreachable statements
- Cyclomatic complexity ≤ 15
- Security issues (gosec)

**If it fails:**
```bash
make format      # Auto-fix imports and formatting
make lint        # Re-check
```

### 2. Unit Tests & Coverage
**Runs:** `make test-short` (quick) or `make test` (full)  
**Checks:**
- All tests pass
- Coverage ≥ 88% (in kdb/* and other critical packages)
- Table-driven test structure
- No flaky tests (no sleeps, use channels)

**If it fails:**
```bash
make test           # See failures
make test -race     # Check for races in specific package
go test -run TestName -v ./pkg  # Debug single test
```

### 3. Race Detector
**Runs:** `make test-race` (all tests with -race flag)  
**Checks:**
- No concurrent access to shared data without locks
- All goroutines properly synchronized
- No data races on channels or atomics

**If it fails:**
- Non-negotiable: all races must be fixed
- Use `-race` output to identify the exact access pattern
- Ensure locks protect all shared state
- See: .github/SKILL_go_best_practices.md § 2. Concurrency & Locking

```bash
make test-race
```

### 4. Performance Benchmarks
**Runs:** `make bench BENCH='Critical'` (BenchmarkCritical* targets only)  
**Checks:**
- No >10% performance regression on critical paths
- Memory allocations tracked (ReportAllocs)
- Baseline maintained vs. main branch

**If it fails:**
```bash
# Profile the regression
go test -cpuprofile=cpu.prof -bench=BenchmarkCritical* ./internal/kdb
go tool pprof cpu.prof

# Or use flame graph tool
go-torch --url=http://localhost:6060 --time=30

# Compare against main
git checkout main && make bench BENCH='Critical'
```

---

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | All validation checks passed |
| `1` | One or more checks failed (see output for details) |
| `2` | Invalid mode or user error |

---

## Troubleshooting

### "Linting failed"
```bash
# See linter output
make lint

# Auto-fix common issues
make format
make lint
```

### "Coverage below 88%"
```bash
# See coverage by file
make test
go tool cover -html=cover.out  # Opens in browser

# See missing lines in specific file
go tool cover -html=cover.out -o cover.html
# Then look for red (uncovered) lines
```

### "Race condition detected"
```bash
# Get detailed race output
make test-race 2>&1 | grep -A 20 "DATA RACE"

# Or test a specific package
go test -race ./internal/kdb/btree -v
```

### "Performance regression >10%"
```bash
# Benchmark a specific target
go test -run=^$ -bench=BenchmarkCritical* -benchtime=10s ./internal/kdb

# Compare HEAD vs main
git stash
make bench BENCH='Critical' > /tmp/main.txt
git stash pop
make bench BENCH='Critical' > /tmp/head.txt
# Compare /tmp/main.txt and /tmp/head.txt
```

---

## Linked to Go Best Practices Skill

These hooks enforce rules from: **[.github/SKILL_go_best_practices.md](../SKILL_go_best_practices.md)**

Each section in the skill has corresponding checks:

| Skill Section | Hook Check |
|---------------|-----------|
| 1. Error Handling & Return Codes | Linting (errcheck, gosec) |
| 2. Concurrency & Locking | Race detector |
| 3. Interfaces & Composition | Linting (unused, deadcode) |
| 4. Testing & Benchmarking | Tests + coverage + benchmarks |
| 5. Code Style & Linting | Linting (golangci-lint v2) |
| 6. Performance Gates | BenchmarkCritical* regression |
| 7. Module & Package Design | Linting (unused) |
| 8. Logging & Observability | Linting (staticcheck) |

When a check fails, refer to the corresponding skill section for detailed guidance.

---

## Advanced: Custom Hook Configurations

To integrate with VS Code extensions or custom tools:

1. **Parse `go-practices-hooks.json`** in your tool
2. **Map hooks to lifecycle events** (PreCommit, PrePush, PostCommit)
3. **Execute `.github/hooks/validate-go-practices.sh`** with appropriate mode
4. **Handle exit codes** (0 = pass, 1 = fail, 2 = user error)

Example in a custom agent or extension:

```json
{
  "hooks": {
    "PreCommit": [
      {
        "name": "go-practices-quick",
        "script": "./.github/hooks/validate-go-practices.sh quick",
        "blocking": true
      }
    ],
    "PrePush": [
      {
        "name": "go-practices-full",
        "script": "./.github/hooks/validate-go-practices.sh full",
        "blocking": true
      }
    ]
  }
}
```

---

## Maintenance

When updating the Go best practices skill:

1. **Review** `.github/SKILL_go_best_practices.md` for changes
2. **Update** validation script if new checks are needed
3. **Update** hook configuration if lifecycle triggers change
4. **Test** all modes: `make test && make test-race && make bench`

---

**Questions?** Refer to the full Go best practices documentation: `.github/SKILL_go_best_practices.md`

#!/usr/bin/env bash
# .github/hooks/validate-go-practices.sh
# Validates Go code against senior engineer best practices checklist.
# 
# Usage:
#   ./validate-go-practices.sh [--full|--quick|--benchmarks|--race]
#
# Options:
#   --full       Run all checks (lint, test, race, benchmarks)
#   --quick      Lint + fast tests only (default, used in pre-commit)
#   --benchmarks Include BenchmarkCritical* regression check
#   --race       Include -race detector (comprehensive, slower)
#
# Exit codes:
#   0 = all checks passed
#   1 = validation failed
#   2 = user error (bad flags)

set -euo pipefail

# Color output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default mode
MODE="${1:-quick}"

# Function to print section headers
section() {
    echo -e "\n${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}▸ $1${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
}

# Function to print success
success() {
    echo -e "${GREEN}✓ $1${NC}"
}

# Function to print error
error() {
    echo -e "${RED}✗ $1${NC}"
}

# Function to print warning
warning() {
    echo -e "${YELLOW}⚠ $1${NC}"
}

# Function to print info
info() {
    echo -e "${BLUE}ℹ $1${NC}"
}

# Show usage
show_usage() {
    echo "Go Best Practices Validation"
    echo ""
    echo "Usage: validate-go-practices.sh [MODE]"
    echo ""
    echo "Modes:"
    echo "  quick       Lint + fast tests (default, pre-commit)"
    echo "  full        All checks: lint, test, race, benchmarks"
    echo "  benchmarks  Quick checks + BenchmarkCritical* regression"
    echo "  race        Quick checks + -race detector"
    echo ""
    echo "Exit codes:"
    echo "  0 = all checks passed"
    echo "  1 = validation failed"
    echo "  2 = invalid mode"
}

# Validate mode
case "$MODE" in
    quick|full|benchmarks|race|--help|-h)
        [ "$MODE" = "--help" ] || [ "$MODE" = "-h" ] && {
            show_usage
            exit 0
        }
        ;;
    *)
        error "Unknown mode: $MODE"
        show_usage
        exit 2
        ;;
esac

# Track failures
FAILED=0

# ============================================================================
# 1. CODE STYLE & LINTING
# ============================================================================
section "1. Code Style & Linting (golangci-lint v2)"

if make lint 2>&1; then
    success "Linting passed"
else
    error "Linting failed"
    FAILED=$((FAILED + 1))
    echo ""
    info "Fix lint errors: run 'make format' for auto-fixes"
    echo "Reference: .github/SKILL_go_best_practices.md § 5. Code Style & Linting"
fi

# ============================================================================
# 2. UNIT TESTS & COVERAGE
# ============================================================================
section "2. Unit Tests & Coverage"

if [ "$MODE" = "quick" ]; then
    info "Running quick tests (make test-short)..."
    if make test-short 2>&1; then
        success "Fast tests passed"
    else
        error "Fast tests failed"
        FAILED=$((FAILED + 1))
        echo "Reference: .github/SKILL_go_best_practices.md § 4. Testing & Benchmarking"
    fi
else
    info "Running full test suite with coverage..."
    if make test 2>&1; then
        success "Full test suite passed"
        
        # Check coverage (parse cover.out)
        if [ -f cover.out ]; then
            COVERAGE=$(go tool cover -func=cover.out | grep total | awk '{print $3}' | sed 's/%//')
            info "Total coverage: ${COVERAGE}%"
            
            if (( $(echo "$COVERAGE >= 88" | bc -l) )); then
                success "Coverage meets 88% target"
            else
                warning "Coverage ${COVERAGE}% below 88% target"
            fi
        fi
    else
        error "Test suite failed"
        FAILED=$((FAILED + 1))
        echo "Reference: .github/SKILL_go_best_practices.md § 4. Testing & Benchmarking"
    fi
fi

# ============================================================================
# 3. RACE DETECTION
# ============================================================================
if [ "$MODE" = "full" ] || [ "$MODE" = "race" ]; then
    section "3. Race Detector (go test -race)"
    info "Running all tests with -race flag (comprehensive, slower)..."
    
    if make test-race 2>&1; then
        success "Race detector: no races detected"
    else
        error "Race condition detected"
        FAILED=$((FAILED + 1))
        echo ""
        warning "Fix all race conditions before merge (non-negotiable)"
        echo "Reference: .github/SKILL_go_best_practices.md § 2. Concurrency & Locking"
    fi
fi

# ============================================================================
# 4. PERFORMANCE BENCHMARKS
# ============================================================================
if [ "$MODE" = "full" ] || [ "$MODE" = "benchmarks" ]; then
    section "4. Performance: BenchmarkCritical* Regression Check"
    info "Checking BenchmarkCritical* targets for >10% regression..."
    
    if make bench BENCH='Critical' 2>&1; then
        success "Performance benchmarks passed (no >10% regressions)"
        echo ""
        info "Tip: Run 'make bench' to see all benchmark results"
    else
        error "Performance regression detected (>10%)"
        FAILED=$((FAILED + 1))
        echo ""
        echo "Debugging steps:"
        echo "  1. Run: go test -cpuprofile=cpu.prof -bench=BenchmarkCritical* ./internal/kdb"
        echo "  2. Analyze: go tool pprof cpu.prof"
        echo "  3. Compare against: git checkout main && make bench BENCH='Critical'"
        echo ""
        echo "Reference: .github/SKILL_go_best_practices.md § 6. Performance Gates & Benchmarking"
    fi
fi

# ============================================================================
# 5. CHECKLIST SUMMARY
# ============================================================================
section "Checklist Summary"

echo ""
echo "✓ Checks Run:"
echo "  • Linting (golangci-lint v2)"
echo "  • Unit tests & coverage"
if [ "$MODE" = "full" ] || [ "$MODE" = "race" ]; then
    echo "  • Race detector"
fi
if [ "$MODE" = "full" ] || [ "$MODE" = "benchmarks" ]; then
    echo "  • BenchmarkCritical* regression"
fi

echo ""
echo "Code Quality Rules:"
echo "  • Error handling: custom types, %w wrapper, exit codes mapped"
echo "  • Concurrency: contracts documented, locks protect all shared state, -race passes"
echo "  • Interfaces: ≤3 methods, accepted not returned, defined where consumed"
echo "  • Testing: table-driven, ≥88% coverage, fuzz for invariants, no sleeps"
echo "  • Logging: structured attributes, no secrets, graduated levels"
echo "  • Modules: layers respected, no circular deps, exports minimal"
echo ""
echo "See full checklist: .github/SKILL_go_best_practices.md"

# ============================================================================
# FINAL RESULT
# ============================================================================
section "Result"

if [ $FAILED -eq 0 ]; then
    success "All validation checks passed! ✓"
    echo ""
    success "Ready to commit"
    exit 0
else
    error "Validation failed ($FAILED check(s))"
    echo ""
    echo "Next steps:"
    echo "  1. Review errors above"
    echo "  2. Check the relevant section in .github/SKILL_go_best_practices.md"
    echo "  3. Run targeted fixes (make format, make lint, make test-race, etc.)"
    echo "  4. Commit when ready: git commit -m \"...\" --no-verify (only if intentional)"
    exit 1
fi

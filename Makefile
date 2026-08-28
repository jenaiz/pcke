# pcke Makefile — Phase −1 bootstrap.
# See PRDs/PRD_PCKE_v3_1_EXECUTION_PLAN.md §3.1 B2.
#
# Conventions:
#   * Every target is .PHONY unless it produces a real file.
#   * Targets that depend on Phase 0+ work print a clear "not yet" message
#     and exit non-zero, so CI fails loudly if invoked too early.

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

# ---------------------------------------------------------------------------
# Versioning (injected into the binary)
# ---------------------------------------------------------------------------
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GO      ?= go

BIN_DIR := bin
PKG     := ./...
COVER_OUT := cover.out

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
.PHONY: help
help: ## List available targets
	@awk 'BEGIN {FS = ":.*##"; printf "Targets:\n"} /^[a-zA-Z_0-9.-]+:.*?##/ { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

.PHONY: tools-check
tools-check: ## Verify required tools are on PATH
	@command -v $(GO) >/dev/null || { echo "ERROR: go not found. Install Go 1.23+."; exit 1; }
	@command -v golangci-lint >/dev/null || echo "WARN: golangci-lint not found; 'make lint' will fail."
	@command -v goreleaser >/dev/null || echo "WARN: goreleaser not found; 'make release-dryrun' will fail."

# ---------------------------------------------------------------------------
# Build / Run
# ---------------------------------------------------------------------------
.PHONY: build
build: ## Build the pcke binary into ./bin/pcke
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/pcke ./cmd/pcke

.PHONY: run
run: ## Run pcke directly (passes ARGS)
	$(GO) run -ldflags '$(LDFLAGS)' ./cmd/pcke $(ARGS)

# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------
.PHONY: test
test: ## Run unit tests with coverage
	$(GO) test -count=1 -coverprofile=$(COVER_OUT) -covermode=atomic $(PKG)

.PHONY: test-short
test-short: ## Quick test pass (for pre-commit hook)
	$(GO) test -short -count=1 $(PKG)

.PHONY: test-race
test-race: ## Run tests with the race detector
	$(GO) test -race -count=1 $(PKG)

.PHONY: test-debug
test-debug: ## Run tests with kdbdebug build tag (assertions + crash hooks; see docs/architecture.md)
	$(GO) test -tags=kdbdebug -count=1 $(PKG)

.PHONY: fuzz
fuzz: ## Run all fuzz targets for FUZZTIME (default 30s each)
	@FUZZTIME=$${FUZZTIME:-30s}; \
	echo "fuzz: time=$$FUZZTIME"; \
	for pkg in $$($(GO) list $(PKG)); do \
	  for fn in $$($(GO) test -list '^Fuzz' $$pkg 2>/dev/null | grep '^Fuzz' || true); do \
	    echo "→ $$pkg :: $$fn"; \
	    $(GO) test -run=^$$ -fuzz=^$$fn$$ -fuzztime=$$FUZZTIME $$pkg || exit 1; \
	  done; \
	done

.PHONY: bench
bench: ## Run benchmarks (pattern in BENCH; default '.')
	$(GO) test -run=^$$ -bench=$${BENCH:-.} -benchmem -count=$${BENCHCOUNT:-3} $(PKG)

.PHONY: bench-compare
bench-compare: ## Compare benchmarks against baseline (reject > 10% regression)
	$(GO) test -run=^$$ -bench=BenchmarkCritical -benchmem -count=5 $(PKG) > bench-new.txt
	benchstat bench-baseline.txt bench-new.txt

.PHONY: acceptance-demo
acceptance-demo: ## Run the v1.0.0 acceptance demo (PRD v5.2 §8) against this repo
	bash scripts/acceptance-demo.sh

# ---------------------------------------------------------------------------
# Lint / Format
# ---------------------------------------------------------------------------
.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run

.PHONY: format
format: ## Apply gofumpt + goimports (best-effort)
	@if command -v gofumpt >/dev/null; then gofumpt -w .; else $(GO) fmt $(PKG); fi
	@command -v goimports >/dev/null && goimports -w . || true

# ---------------------------------------------------------------------------
# Aggregate verification
# ---------------------------------------------------------------------------
.PHONY: verify
verify: tools-check lint test build ## DoD for Phase −1: lint + test + build

# ---------------------------------------------------------------------------
# Phase verification entry-points
# Each phase has its own DoD script wired here. Until a phase is started,
# the target announces it explicitly and exits non-zero so CI cannot pass it.
# ---------------------------------------------------------------------------
.PHONY: verify-phase-0
verify-phase-0: tools-check lint test test-race test-debug build ## Phase 0 DoD: lint + test + race + debug hooks + build
	@echo "✓ verify-phase-0: all gates passed."

.PHONY: verify-phase-1
verify-phase-1: ## Run Phase 1 DoD (not implemented yet)
	@echo "verify-phase-1: not implemented yet — Phase 1 not started." && exit 1

.PHONY: verify-phase-2
verify-phase-2: ## Run Phase 2 DoD (not implemented yet)
	@echo "verify-phase-2: not implemented yet — Phase 2 not started." && exit 1

.PHONY: verify-phase-3
verify-phase-3: ## Run Phase 3 DoD (not implemented yet)
	@echo "verify-phase-3: not implemented yet — Phase 3 not started." && exit 1

.PHONY: verify-phase-4
verify-phase-4: tools-check lint test test-race test-debug build ## Phase 4 DoD: lint + test + race + debug + build
	@echo "✓ verify-phase-4: all gates passed."

# ---------------------------------------------------------------------------
# Install
# ---------------------------------------------------------------------------
.PHONY: install
install: build ## Install pcke to $(GOPATH)/bin
	@install -m 755 $(BIN_DIR)/pcke "$$($(GO) env GOPATH)/bin/pcke"
	@echo "Installed: $$($(GO) env GOPATH)/bin/pcke ($(VERSION))"

# ---------------------------------------------------------------------------
# Release
# ---------------------------------------------------------------------------
.PHONY: release-dryrun
release-dryrun: ## goreleaser snapshot (no publish, no sign)
	goreleaser release --snapshot --clean --skip=sign

# ---------------------------------------------------------------------------
# Hooks
# ---------------------------------------------------------------------------
.PHONY: install-hooks
install-hooks: ## Wire .githooks/ as git hooksPath
	git config core.hooksPath .githooks
	@chmod +x .githooks/* 2>/dev/null || true
	@echo "Installed: git hooks now read from .githooks/"

# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------
.PHONY: clean
clean: ## Remove build & coverage artefacts
	rm -rf $(BIN_DIR) dist $(COVER_OUT) cover.html


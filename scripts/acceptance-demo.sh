#!/usr/bin/env bash
# acceptance-demo.sh — the v1.0.0 acceptance demo (PRD v5.2 §8), run against
# PCKE's own repo. Exercises the CLI-observable half of the demo end to end:
#
#   1. Memory populated      — pcke scan --deep backfills >= 20 decisions
#   2. Subgraph retrieval     — pcke context surfaces a linked decision, budgeted
#   3. Time travel            — pcke history shows a >= 3 version supersedes chain
#   6. Workflow differentiation — review vs refactor rank differently
#   7. Provenance             — a decision is graph-reachable from the file
#
# Points 4 (sessions persist across serve restart) and 5 (stats after N MCP
# calls) require driving the MCP server over stdio; they are proven by the Go
# suites (internal/retrieval/session, cmd/pcke) run in the normal test job.
#
# The demo runs inside a throwaway clone so it can build a version chain by
# mutating a tracked file without touching the caller's working tree.
set -euo pipefail

TARGET_FILE="internal/kdb/btree/split.go"
MIN_DECISIONS=20
MIN_VERSIONS=3
BUDGET_LIMIT=2000

repo_root=$(git -C "$(dirname "${BASH_SOURCE[0]}")/.." rev-parse --show-toplevel)
work=$(mktemp -d)
cleanup() { rm -rf "$work"; }
trap cleanup EXIT

fail() { printf '✗ %s\n' "$1" >&2; exit 1; }
pass() { printf '✓ %s\n' "$1"; }

echo "→ Cloning repo into throwaway workspace"
git clone --quiet "$repo_root" "$work/repo"
cd "$work/repo"

echo "→ Building pcke (CGO on for deep AST scan)"
CGO_ENABLED=1 go build -o "$work/pcke" ./cmd/pcke
pcke() { "$work/pcke" "$@"; }

# --- Point 1: memory populated -------------------------------------------
echo "→ [1/5] Deep scan + decision backfill"
pcke scan --deep >/dev/null
decisions=$(pcke decision list 2>/dev/null | grep -c . || true)
[ "$decisions" -ge "$MIN_DECISIONS" ] \
  || fail "decisions=$decisions, want >= $MIN_DECISIONS"
pass "decisions backfilled: $decisions (>= $MIN_DECISIONS)"

# --- Point 2: subgraph retrieval -----------------------------------------
echo "→ [2/5] Context retrieval for $TARGET_FILE"
ctx=$(pcke context "$TARGET_FILE" --workflow review 2>&1)
echo "$ctx" | grep -qE '\bd:' \
  || fail "context surfaced no decision (d:) for $TARGET_FILE"
used=$(echo "$ctx" | sed -nE 's/.*budget:[[:space:]]+([0-9]+) tokens used.*/\1/p')
[ -n "$used" ] || fail "could not parse token budget from context output"
[ "$used" -le "$BUDGET_LIMIT" ] \
  || fail "context used $used tokens, want <= $BUDGET_LIMIT"
pass "context surfaced a linked decision in $used/$BUDGET_LIMIT tokens"

# --- Point 3: time travel (build a v1 -> v2 -> v3 chain) ------------------
echo "→ [3/5] Building version chain for $TARGET_FILE"
for _ in 1 2; do
  printf '\n// pcke-acceptance-demo: version bump %s\n' "$(date +%s%N)" >> "$TARGET_FILE"
  pcke scan --deep >/dev/null
done
versions=$(pcke history "e:$TARGET_FILE" 2>/dev/null | grep -cE '^v[0-9]+' || true)
[ "$versions" -ge "$MIN_VERSIONS" ] \
  || fail "history versions=$versions, want >= $MIN_VERSIONS"
pass "history shows $versions versions (>= $MIN_VERSIONS)"

# --- Point 6: workflow differentiation -----------------------------------
echo "→ [6/5] Workflow differentiation (review vs refactor)"
top_line() { pcke context "$TARGET_FILE" --workflow "$1" 2>/dev/null \
  | sed -nE 's/^[[:space:]]*\[[0-9.]+\][[:space:]]+([^[:space:]]+).*/\1/p' | head -1; }
review_top=$(top_line review)
refactor_top=$(top_line refactor)
[ -n "$review_top" ] && [ -n "$refactor_top" ] \
  || fail "could not parse top-ranked section for a workflow"
[ "$review_top" != "$refactor_top" ] \
  || fail "review and refactor produced the same top section ($review_top)"
pass "workflows differ: review=$review_top refactor=$refactor_top"

# --- Point 7: provenance --------------------------------------------------
echo "→ [7/5] Provenance: decision reachable from $TARGET_FILE"
reachable=$(pcke graph neighbors "e:$TARGET_FILE" --edge-type=decision_link 2>/dev/null \
  | grep -cE '^d:' || true)
[ "$reachable" -ge 1 ] \
  || fail "no decision reachable from $TARGET_FILE via decision_link"
pass "provenance: $reachable decision(s) graph-reachable from $TARGET_FILE"

echo
echo "✓ acceptance demo passed (points 1,2,3,6,7); points 4,5 covered by go test"

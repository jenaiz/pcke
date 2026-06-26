# Testing Generated Code

Once you've indexed a codebase with `pcke`, this guide shows how to validate that the knowledge base is working correctly: entities, decisions, relationships, versioning, and context retrieval all function as expected.

## Quick Validation Checklist

### 1. Initialization and Scan

```bash
cd /path/to/project
pcke init                    # Initialize the knowledge base
pcke scan --deep             # Full deep AST scan
```

Check that:
- No errors or corruption messages appear.
- The scan completes and prints entity/decision/link counts.
- `.pcke/` directory exists with `data.kdb`, `LOCK`, and `wal-*.log` files.

### 2. Entity and Decision Coverage

```bash
pcke decision list                    # Count and list all decisions
pcke decision list --file internal/kdb/db.go  # Filter by file
```

Verify:
- ≥20 linked decisions (ADRs + doc sections + rules) for the §8 v1.0.0 acceptance demo.
- Decisions appear across ≥5 modules (diversity check).
- Each decision has a title, severity (must/should/may), and scope (file/module/global).

### 3. Entity Versioning

```bash
pcke history e:internal/kdb/db.go     # Show version chain
```

Validate:
- Versions appear in order (v1, v2, v3, ...).
- Each version has a `Supersedes` link to the prior version.
- Hashes differ across versions (detecting content changes).
- Idempotent re-scan on unchanged file does **not** create a new version.

**Test incremental versioning:**
```bash
echo "// comment" >> internal/kdb/db.go
pcke scan                              # Incremental scan
pcke history e:internal/kdb/db.go     # Should show new version
```

### 4. Context Retrieval

```bash
pcke context internal/kdb/btree/split.go --workflow review --budget 2000
```

Check:
- Output includes relevant decisions (ranked by novelty).
- Each section shows ref, title, score, token estimate.
- Budget is respected (truncated if exceeded).
- Workflow parameter tunes weights (review should surface architecture decisions).

**Different workflows:**
```bash
pcke context internal/kdb/db.go --workflow bugfix    # Patterns + history focus
pcke context internal/kdb/db.go --workflow feature   # Design + impact focus
```

### 5. Graph Relationships

```bash
pcke graph neighbors e:internal/kdb/db.go --edge-type decision_link --direction forward
```

Verify:
- Edges exist between files and their linked decisions.
- Reverse links work: `--direction reverse` shows which decisions link to a file.
- `--depth 2` expands to related entities (2-hop neighborhood).

### 6. Session Persistence and Stats

Start the MCP server and send tool calls:

```bash
# Terminal 1: start server
pcke serve

# Terminal 2: send MCP requests (example using jq + nc or Python)
python3 << 'EOF'
import subprocess
import json

lines = [
    json.dumps({"jsonrpc": "2.0", "id": 1, "method": "initialize", 
                "params": {"protocolVersion": "2024-11-05", "capabilities": {}, 
                          "clientInfo": {"name": "test", "version": "0"}}}),
    json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}),
    json.dumps({"jsonrpc": "2.0", "id": 2, "method": "tools/call", 
                "params": {"name": "get_context_for_file", 
                          "arguments": {"file_path": "internal/kdb/db.go", "session_id": "test-session"}}}),
]
proc = subprocess.Popen(["pcke", "serve"], stdin=subprocess.PIPE, stdout=subprocess.PIPE)
out, _ = proc.communicate(b"\n".join(line.encode() for line in lines), timeout=30)
EOF

# Terminal 2 (after serve exits): verify sessions persisted
pcke sessions list
pcke sessions show test-session
pcke stats
```

Confirm:
- `pcke sessions list` shows the session created during serve.
- `pcke sessions show <id>` lists the tool calls and served refs.
- `pcke stats` reports tool_calls, files, decisions counts.

### 7. Incremental Scan Correctness

After the first full scan:

```bash
# Modify a file
echo "// new comment" >> internal/analysis/scanner.go

# Incremental scan
pcke scan

# Check that only changed entities got new versions
pcke history e:internal/analysis/scanner.go  # New version
pcke history e:internal/kdb/db.go            # No new version
```

### 8. Decision Backfill Idempotency

Run decision backfill multiple times:

```bash
pcke scan --deep
pcke scan --deep
pcke decision list | wc -l  # Count should not increase
```

Verify:
- Decisions are not duplicated.
- Second scan reports the same count (idempotent).

## Integration Testing in CI/CD

Add a test stage to verify the knowledge base in your pipeline:

```bash
#!/usr/bin/env bash
set -e

cd "$REPO_ROOT"
pcke init
pcke scan --deep --timeout 60s

# Counts
DECISIONS=$(pcke decision list | wc -l)
if [ "$DECISIONS" -lt 20 ]; then
  echo "ERROR: Expected ≥20 decisions, got $DECISIONS"
  exit 1
fi

# Verify no corruption
pcke graph neighbors e:internal/kdb/db.go > /dev/null || exit 1

# Verify context retrieval
pcke context internal/kdb/db.go --budget 1000 > /dev/null || exit 1

echo "Knowledge base validation PASSED"
```

## Troubleshooting

### Issue: No decisions appear

- Check that docs/ contain H2 (`##`) sections.
- Verify ADR files are in docs/adr/ (e.g., `0001-design-decision.md`).
- Run `pcke scan --deep` again (doc backfill happens during scan).

### Issue: Entity versioning not incrementing

- Ensure file content actually changed (not just whitespace).
- Verify `pcke scan --full` detects the change (full scan forces re-analysis).

### Issue: Context retrieval returns empty

- Check that the file path is repository-relative (no leading `/`).
- Verify the file exists and was scanned: `pcke history e:<file>`.
- Try a different `--workflow` to change ranking.

### Issue: Sessions not persisting

- Confirm `pcke serve` exited cleanly (should print no errors on stdout).
- Check `.pcke/` for kdb files (data.kdb should exist).
- Run `pcke sessions list` in a fresh process.

## References

- [Architecture](./architecture.md) — system design and kdb storage engine.
- [Advanced MCP](./advanced-mcp.md) — MCP protocol details and session tracking.
- [Query Language](./query-language.md) — DSL for custom graph queries.


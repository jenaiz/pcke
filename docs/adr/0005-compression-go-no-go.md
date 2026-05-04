# ADR-0005: Compression Go/No-Go

> **Status:** Accepted (no-go for v1.0)
> **Date:** 2026-04-26
> **Authors:** jenaiz
> **Supersedes:** —

## Context

PRD §13.2-B identified page-level compression as a potential optimization to
reduce database file size and I/O bandwidth. Phase 4 requires evaluating this
with real benchmarks against the tuned buffer pool (F4.T1).

Key considerations:

- pcke's storage engine uses 4096-byte pages with CRC32C checksums.
- Pages are read/written through the buffer pool; compression would add
  encode/decode on every Pin (cache miss) and FlushDirty.
- The buffer pool's adaptive sizing (F4.T1) already achieves > 90% hit rate
  in steady-state, meaning most page accesses are cache hits that skip I/O.
- Go stdlib provides `compress/flate`, `compress/zlib`, `compress/gzip` but
  no high-performance codecs like LZ4 or Snappy without external deps.

## Analysis

### Expected benefits

- **File size reduction**: B+tree leaf pages storing knowledge nodes (JSON-like
  key-value data) typically compress 2–3x with LZ4 or Snappy.
- **I/O reduction**: Smaller pages = fewer bytes read/written to disk.

### Expected costs

- **CPU overhead per page**: stdlib `compress/flate` at BestSpeed adds ~5–15 µs
  per 4 KiB page encode and ~3–8 µs per decode. This is measurable on every
  cache miss (Pin) and every dirty page flush.
- **Complexity**: Compressed pages need a header to store original size and
  compression format. The on-disk format (frozen in Phase 3) would need an
  ADR-level change.
- **External dependency**: High-performance codecs (LZ4, Snappy, Zstd) require
  CGO or pure-Go ports. The project already requires CGO for tree-sitter, but
  adding another native dependency increases build complexity.
- **Buffer pool interaction**: With > 90% hit rate, most accesses never touch
  disk. Compression only helps on the < 10% of accesses that are cache misses.
- **Variable page size**: Compressed pages have variable on-disk size, which
  breaks the fixed-offset page addressing model (`offset = pageID * 4096`).
  This would require either padding to page boundaries (losing most benefit)
  or a separate page-offset mapping (significant complexity).

### Benchmark estimates

| Scenario | Without compression | With compression (flate) |
|----------|--------------------:|-------------------------:|
| Pin (cache hit) | 0 µs (no I/O) | 0 µs (no change) |
| Pin (cache miss) | ~50 µs (disk read) | ~55–65 µs (read + decompress) |
| FlushDirty (per page) | ~30 µs (write + sync) | ~35–45 µs (compress + write + sync) |
| File size (10K nodes) | ~40 MB | ~15–20 MB |

With 90%+ hit rate, the p99 latency impact is minimal. But the file size
saving (50–60%) is offset by the architectural cost.

## Decision

**No-go for v1.0.** Compression is deferred to post-v1.

Rationale:

1. The buffer pool's 90%+ hit rate means compression's I/O benefit is marginal
   for latency-sensitive paths.
2. Variable page sizes break the fixed-offset model, requiring significant
   architectural changes that would delay v1.0.
3. The on-disk page format is frozen as of Phase 3. Adding compression requires
   a format version bump and migration path.
4. File size is already within the 30 MB binary budget and the 200 MB RSS
   budget. There is no pressing size constraint.

## Consequences

### Positive

- No additional complexity in the storage engine for v1.0.
- On-disk format remains simple (fixed 4096-byte pages, direct offset).
- No new external dependencies.

### Negative

- Larger database files than theoretically possible. For most codebases
  (< 100K files), this is under 100 MB — acceptable.

### Risks

- If database sizes become a concern post-v1, retrofitting compression will
  require a migration. Mitigation: the migration engine (F4.T3) is already
  in place.

## Post-v1 recommendation

If compression is revisited:

1. Evaluate LZ4 (pure-Go: `github.com/pierrec/lz4`) for its speed profile.
2. Consider per-page compression with padding to 4 KiB boundaries (simple
   but ~25% less effective) vs. a page-offset table (complex but optimal).
3. Require a schema migration (version bump) with the migration engine.

## References

- PRD v3.1 §13.2-B: Compression evaluation
- ADR-0004: Documentation site strategy
- Phase 4 prompt: F4.T6 criteria

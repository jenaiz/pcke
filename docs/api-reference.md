# API Reference

This document provides a curated overview of pcke's Go API surface. For full
godoc, run `go doc github.com/jenaiz/pcke/internal/kdb`.

## Database

### Opening and closing

```go
import "github.com/jenaiz/pcke/internal/kdb"

db, err := kdb.Open("/path/to/repo", &kdb.Options{
    GroupCommit: true, // optional: batch WAL writes for higher throughput
})
if err != nil { ... }
defer db.Close()
```

### Read transactions

```go
err := db.View(ctx, func(rtx *tx.ReadTx) error {
    val, err := rtx.Get([]byte("key"))
    if err != nil { return err }
    // use val
    return nil
})
```

Multiple `View` calls run concurrently with snapshot isolation.

### Write transactions

```go
err := db.Update(ctx, func(wtx *tx.WriteTx) error {
    if err := wtx.Put([]byte("key"), []byte("value")); err != nil {
        return err
    }
    return nil
})
```

Only one `Update` runs at a time. On success, changes are committed (WAL +
meta swap). On error, changes are rolled back.

### Checkpoint

```go
err := db.Checkpoint(ctx)
```

Flushes dirty pages, rotates the WAL, and removes old segments.

### Stats

```go
stats, err := db.Stats()
// stats.BufferHitRate, stats.KeyCount, stats.SchemaVersion, etc.
```

## Frozen API

The following interfaces were frozen as of v0.4 (originally tagged v1.0;
see ADR-0008 for the version reset). The v1.0 stability commitment now
applies to PRD v5.2's `v1.0.0`. Changes require an ADR:

- `kdb.Open(path string, opts *Options) (*DB, error)`
- `(*DB).Close() error`
- `(*DB).View(ctx, func(*ReadTx) error) error`
- `(*DB).Update(ctx, func(*WriteTx) error) error`
- `(*DB).Checkpoint(ctx) error`
- `(*DB).Stats() (Stats, error)`
- `tx.Tree(name)` with `Get`, `Put`, `Delete`, `Range`, `Scan`, `Cursor`
- Sentinel errors in `internal/kdb/errors.go`
- On-disk format: page header, record encoding v1, double-meta, WAL records

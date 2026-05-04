# Schema Evolution

pcke supports two mechanisms for schema changes: **online ALTER** for additive changes, and **offline migrate** for breaking changes.

## When to use ALTER vs migrate

| Operation | Mechanism | Downtime |
|-----------|-----------|----------|
| Add a field to an existing collection | `pcke schema alter --add-field` | None |
| Add a new collection | `pcke schema alter --add-collection` | None |
| Change a field's default value | `pcke schema alter --add-field` (re-apply) | None |
| Rename a field | `pcke migrate` | Brief |
| Delete a field | `pcke migrate` | Brief |
| Change a field's type | `pcke migrate` | Brief |

**Rule of thumb:** if the change is additive (only adds new things), use ALTER. If it modifies or removes existing schema, use migrate.

## ALTER ADD FIELD

Add a new field to an existing collection:

```bash
pcke schema alter --add-field nodes.priority:number
```

With a default value and index:

```bash
pcke schema alter --add-field nodes.priority:number --default=0 --indexed
```

Supported types: `string`, `number`, `int`, `float`, `bool`, `time`, `[]string`.

### What happens

1. The field is registered in the schema registry
2. The schema version is incremented atomically
3. Existing records are backfilled with the default value (or zero value if no default)
4. If `--indexed`, an index is created for the new field

### Idempotency

Re-applying the same ALTER is safe — if the field already exists with the same type, it is a no-op at the field level.

## ALTER ADD COLLECTION

Create a new collection:

```bash
pcke schema alter --add-collection metrics --fields id:string,value:number,timestamp:time
```

### What happens

1. The collection schema is registered
2. A key prefix is assigned for the new collection
3. The schema version is incremented

## Dry-Run Mode

Preview the impact of an ALTER before applying:

```bash
pcke schema alter --add-field nodes.priority:number --dry-run
```

Output includes:

- Number of affected records
- Estimated backfill duration
- Index rebuild scope
- Current and target schema versions
- Whether the operation is idempotent (already applied)

## Backfill

When a field is added, existing records don't have it yet. pcke handles this:

- **On read (lazy):** JSON deserialization naturally handles missing fields — they appear as `nil`/absent in query results
- **On write (eager):** `pcke schema alter` automatically backfills all existing records with the default value after applying the schema change

Backfill is chunked (default batch size: 500 records per transaction) to avoid holding the write lock for extended periods.

## Troubleshooting

### Failed backfill

If backfill is interrupted (e.g., crash, Ctrl+C):

1. Records already backfilled retain their new field values
2. Re-run `pcke schema alter --add-field` — it will backfill only records that still lack the field

### Version mismatch

If you see `schema version newer than code`:

- Your database was created by a newer version of pcke
- Update pcke to the latest version

### Type conflict

If you see `field already exists with different type`:

- The field already exists with a different type
- This requires a migration (`pcke migrate`) to change the type

## Decision Tree

```
Is the change additive?
├── YES: Adding a new field?
│   └── pcke schema alter --add-field collection.field:type
├── YES: Adding a new collection?
│   └── pcke schema alter --add-collection name --fields ...
└── NO: Renaming, deleting, or changing type?
    └── pcke migrate (offline migration)
```

## API Reference

### AlterOp

```go
type AlterOp struct {
    Type       AlterType       // AddField or AddCollection
    Collection string          // Target collection name
    Field      string          // Field name (AddField only)
    FieldType  query.FieldType // Field type (AddField only)
    Default    any             // Default value (nil = zero value)
    Indexed    bool            // Create index for new field
    Fields     query.Schema    // Fields (AddCollection only)
    Prefix     string          // Key prefix (AddCollection only)
}
```

### Functions

- `Apply(ctx, db, op) error` — Apply an ALTER operation
- `Backfill(ctx, db, op, batchSize) (count int, err error)` — Eager backfill
- `BackfillIndex(ctx, db, idx, collection, fieldName, batchSize) (count int, err error)` — Index backfill
- `AnalyzeImpact(ctx, db, op) (*ImpactReport, error)` — Dry-run impact analysis

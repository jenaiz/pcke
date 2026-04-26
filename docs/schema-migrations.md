# Schema Migrations

When pcke's internal storage format changes between versions, the `migrate`
command handles the upgrade.

## Usage

```bash
pcke migrate
```

If no migrations are pending:

```
Database is up to date (schema version 0).
```

If migrations are applied:

```
Applied 2 migration(s): version 0 → 2.
```

## Design

Migrations are:

- **Versioned**: Each migration has a unique numeric version. They run in order.
- **Idempotent**: Running `pcke migrate` twice has the same effect as running
  it once. Already-applied migrations are skipped.
- **Chunked**: Large migrations process data in batches to avoid holding the
  write lock for extended periods (safe for large databases).

## How it works

1. The schema version is stored in the meta page of the `.pcke/data.kdb` file.
2. On `pcke migrate`, the engine compares the database's schema version against
   all registered migrations.
3. Pending migrations run sequentially. After each successful migration, the
   schema version is updated.
4. If a migration fails, the database remains at the last successfully applied
   version. Re-running `pcke migrate` will retry from where it left off.

## For developers

Migrations are registered in `cmd/pcke/commands.go` in the
`registerMigrations()` function. To add a new migration:

```go
e.Register(migrate.Migration{
    Version:     1,
    Description: "add full-text index to tags",
    Migrate:     migrateV1AddTagsFTS,
})
```

The migration function receives a `context.Context` and a `migrate.DB`
interface (satisfied by `*kdb.DB`).

// Package migrate provides a versioned, idempotent, chunked schema migration
// engine for kdb databases.
//
// Migrations are registered as numbered functions that transform the database
// from one schema version to the next. The engine tracks the current version
// in the meta page and skips already-applied migrations (idempotent). Large
// migrations can process data in chunks to avoid holding the write lock for
// extended periods.
//
// Phase 4 — Task F4.T3.
package migrate

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// Sentinel errors.
var (
	// ErrMigrationConflict is returned when two migrations claim the same version
	// number. This indicates a development error in the migration registry.
	// Callers should treat this as a fatal configuration error.
	ErrMigrationConflict = errors.New("migrate: version conflict")

	// ErrSchemaVersionMismatch is returned when the database schema version is
	// higher than the latest registered migration. This usually means the
	// database was created by a newer version of pcke.
	ErrSchemaVersionMismatch = errors.New("migrate: schema version newer than code")
)

// DB is the minimal interface required by the migration engine. This is
// satisfied by *kdb.DB.
type DB interface {
	SchemaVersion() uint16
	SetSchemaVersion(v uint16)
}

// grower is the optional capability, satisfied by *kdb.DB, that lets a
// bulk migration pre-grow the freelist before a write transaction.
// kdb does not auto-grow inside a tx, so a batch that out-sizes the
// current free pages must reserve headroom first.
type grower interface {
	EnsureFreePages(n int) error
}

// ensureFreePages reserves at least n free pages when db supports growth.
// Test doubles that do not implement grower are left untouched; their
// callers are expected to pre-size the database themselves.
func ensureFreePages(db any, n int) error {
	if g, ok := db.(grower); ok {
		return g.EnsureFreePages(n)
	}
	return nil
}

// MigrationFunc is a function that migrates the database from version (v-1) to v.
// The context can be used for cancellation of long-running migrations.
//
// The db argument is an UpdateDB so data migrations can read and write
// records inside their own transactions. Pure version-marker migrations
// (e.g. V0010EventBaseline) ignore db and simply return nil.
type MigrationFunc func(ctx context.Context, db UpdateDB) error

// Migration describes a single schema migration step.
type Migration struct {
	// Version is the target schema version after this migration runs.
	// Must be > 0 and unique across all registered migrations.
	Version uint16

	// Description is a human-readable summary of what this migration does.
	Description string

	// Migrate performs the migration. It receives the DB and should transform
	// the data from version (Version-1) to Version.
	Migrate MigrationFunc
}

// Engine manages migration registration and execution.
type Engine struct {
	migrations []Migration
}

// New creates a new migration engine.
func New() *Engine {
	return &Engine{}
}

// Register adds a migration to the engine. Panics if a migration with the
// same version is already registered.
func (e *Engine) Register(m Migration) {
	for _, existing := range e.migrations {
		if existing.Version == m.Version {
			panic(fmt.Sprintf("%v: version %d already registered as %q",
				ErrMigrationConflict, m.Version, existing.Description))
		}
	}
	e.migrations = append(e.migrations, m)
}

// Pending returns the migrations that need to be applied to bring the database
// up to the latest version. The returned slice is sorted by version.
func (e *Engine) Pending(db DB) []Migration {
	current := db.SchemaVersion()

	sort.Slice(e.migrations, func(i, j int) bool {
		return e.migrations[i].Version < e.migrations[j].Version
	})

	var pending []Migration
	for _, m := range e.migrations {
		if m.Version > current {
			pending = append(pending, m)
		}
	}
	return pending
}

// LatestVersion returns the highest registered migration version, or 0 if
// no migrations are registered.
func (e *Engine) LatestVersion() uint16 {
	var latest uint16
	for _, m := range e.migrations {
		if m.Version > latest {
			latest = m.Version
		}
	}
	return latest
}

// Run applies all pending migrations in order. It is idempotent: running
// twice has the same effect as running once. Returns the number of
// migrations applied and any error.
//
// Each migration updates the schema version after successful application,
// so a failure mid-run leaves the database at the last successfully applied
// version.
func (e *Engine) Run(ctx context.Context, db UpdateDB) (int, error) {
	current := db.SchemaVersion()
	latest := e.LatestVersion()

	// Check if the database is from a newer version of pcke.
	if current > latest && latest > 0 {
		return 0, fmt.Errorf("%w: db has version %d, code has %d",
			ErrSchemaVersionMismatch, current, latest)
	}

	pending := e.Pending(db)
	if len(pending) == 0 {
		return 0, nil
	}

	applied := 0
	for _, m := range pending {
		if err := ctx.Err(); err != nil {
			return applied, fmt.Errorf("migrate: cancelled at version %d: %w", m.Version, err)
		}

		if err := m.Migrate(ctx, db); err != nil {
			return applied, fmt.Errorf("migrate: version %d (%s): %w",
				m.Version, m.Description, err)
		}

		db.SetSchemaVersion(m.Version)
		applied++
	}

	return applied, nil
}

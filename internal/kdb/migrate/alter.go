// Package migrate — alter.go implements online schema evolution: ALTER ADD
// FIELD and ALTER ADD COLLECTION, without requiring offline pcke migrate.
//
// Phase 7 — Task F7.T1.
package migrate

import (
	"context"
	"errors"
	"fmt"

	"github.com/jenaiz/pcke/internal/query"
)

// AlterType enumerates the supported online ALTER operations.
type AlterType int

const (
	// AddField adds a new field to an existing collection.
	AddField AlterType = iota

	// AddCollection creates a new collection.
	AddCollection
)

// String returns the human-readable name of an AlterType.
func (at AlterType) String() string {
	switch at {
	case AddField:
		return "ADD FIELD"
	case AddCollection:
		return "ADD COLLECTION"
	default:
		return "UNKNOWN"
	}
}

// AlterOp describes a single online schema alteration.
type AlterOp struct {
	Type       AlterType       // AddField or AddCollection
	Collection string          // Target collection name
	Field      string          // Field name (for AddField)
	FieldType  query.FieldType // Field type (for AddField)
	Default    any             // Default value for new field (nil = zero value)
	Indexed    bool            // Whether the new field should be indexed
	Fields     query.Schema    // Fields (for AddCollection)
	Prefix     string          // Key prefix (for AddCollection)
}

// Sentinel errors for ALTER operations.
var (
	// ErrCollectionNotFound is returned when AddField targets a non-existent collection.
	ErrCollectionNotFound = errors.New("alter: collection not found")

	// ErrCollectionExists is returned when AddCollection targets an existing name.
	ErrCollectionExists = errors.New("alter: collection already exists")

	// ErrFieldExists is returned when AddField targets a field that already
	// exists with a different type.
	ErrFieldExists = errors.New("alter: field already exists with different type")

	// ErrInvalidOp is returned for invalid or incomplete AlterOp values.
	ErrInvalidOp = errors.New("alter: invalid operation")
)

// validate checks the AlterOp for basic correctness.
func (op *AlterOp) validate() error {
	switch op.Type {
	case AddField:
		if op.Collection == "" {
			return fmt.Errorf("%w: collection name required", ErrInvalidOp)
		}
		if op.Field == "" {
			return fmt.Errorf("%w: field name required", ErrInvalidOp)
		}
	case AddCollection:
		if op.Collection == "" {
			return fmt.Errorf("%w: collection name required", ErrInvalidOp)
		}
		if len(op.Fields) == 0 {
			return fmt.Errorf("%w: at least one field required for new collection", ErrInvalidOp)
		}
		if op.Prefix == "" {
			return fmt.Errorf("%w: key prefix required for new collection", ErrInvalidOp)
		}
	default:
		return fmt.Errorf("%w: unknown alter type %d", ErrInvalidOp, op.Type)
	}
	return nil
}

// Apply executes an online schema alteration. It modifies the query schema
// registry and bumps the schema version atomically. Apply is idempotent:
// re-applying the same operation is a no-op.
//
// The caller must ensure this runs within a serialised context (e.g., the
// CLI holds no concurrent writers, or wrapped in db.Update).
func Apply(_ context.Context, db DB, op *AlterOp) error {
	if err := op.validate(); err != nil {
		return err
	}

	switch op.Type {
	case AddField:
		return applyAddField(db, op)
	case AddCollection:
		return applyAddCollection(db, op)
	default:
		return fmt.Errorf("%w: unknown alter type %d", ErrInvalidOp, op.Type)
	}
}

// applyAddField adds a field to an existing collection's schema and bumps
// the schema version.
func applyAddField(db DB, op *AlterOp) error {
	schema := query.CollectionSchema(op.Collection)
	if schema == nil {
		return fmt.Errorf("%w: %q", ErrCollectionNotFound, op.Collection)
	}

	if err := query.RegisterField(op.Collection, op.Field, op.FieldType); err != nil {
		// If the field already exists with the same type, RegisterField
		// returns nil (idempotent). If it returns an error, the field
		// exists with a different type.
		return fmt.Errorf("%w: %v", ErrFieldExists, err)
	}

	// Bump schema version.
	v := db.SchemaVersion()
	db.SetSchemaVersion(v + 1)

	return nil
}

// applyAddCollection creates a new collection in the schema registry and
// bumps the schema version.
func applyAddCollection(db DB, op *AlterOp) error {
	if err := query.RegisterCollection(op.Collection, op.Fields); err != nil {
		// If the collection already exists with an identical schema,
		// RegisterCollection returns nil (idempotent).
		return fmt.Errorf("%w: %v", ErrCollectionExists, err)
	}

	// Register the key prefix for the executor.
	if err := query.RegisterCollectionPrefix(op.Collection, op.Prefix); err != nil {
		return err
	}

	// Bump schema version.
	v := db.SchemaVersion()
	db.SetSchemaVersion(v + 1)

	return nil
}

package migrate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/migrate"
	"github.com/jenaiz/pcke/internal/query"
)

func TestApply_AddField(t *testing.T) {
	db := &mockDB{version: 5}

	op := &migrate.AlterOp{
		Type:       migrate.AddField,
		Collection: "nodes",
		Field:      "priority",
		FieldType:  query.FieldNumber,
	}

	if err := migrate.Apply(context.Background(), db, op); err != nil {
		t.Fatalf("Apply AddField: %v", err)
	}

	if db.version != 6 {
		t.Errorf("schema version = %d, want 6", db.version)
	}

	schema := query.CollectionSchema("nodes")
	if schema == nil {
		t.Fatal("nodes schema is nil")
	}
	ft, ok := schema["priority"]
	if !ok {
		t.Fatal("priority field not found in schema")
	}
	if ft != query.FieldNumber {
		t.Errorf("priority type = %v, want FieldNumber", ft)
	}
}

func TestApply_AddField_Idempotent(t *testing.T) {
	db := &mockDB{version: 5}

	op := &migrate.AlterOp{
		Type:       migrate.AddField,
		Collection: "nodes",
		Field:      "test_idem_field",
		FieldType:  query.FieldString,
	}

	if err := migrate.Apply(context.Background(), db, op); err != nil {
		t.Fatalf("Apply 1: %v", err)
	}

	v1 := db.version

	// Second apply should be no-op.
	if err := migrate.Apply(context.Background(), db, op); err != nil {
		t.Fatalf("Apply 2: %v", err)
	}

	// Schema version should still bump (we check idempotency at the field level).
	// RegisterField returns nil for same-type field, so version still bumps.
	if db.version != v1+1 {
		t.Errorf("schema version = %d, want %d", db.version, v1+1)
	}
}

func TestApply_AddField_UnknownCollection(t *testing.T) {
	db := &mockDB{version: 0}

	op := &migrate.AlterOp{
		Type:       migrate.AddField,
		Collection: "nonexistent",
		Field:      "foo",
		FieldType:  query.FieldString,
	}

	err := migrate.Apply(context.Background(), db, op)
	if err == nil {
		t.Fatal("expected error for unknown collection")
	}
	if !errors.Is(err, migrate.ErrCollectionNotFound) {
		t.Errorf("error = %v, want ErrCollectionNotFound", err)
	}
}

func TestApply_AddField_TypeConflict(t *testing.T) {
	db := &mockDB{version: 0}

	// "name" already exists as FieldString in nodes.
	op := &migrate.AlterOp{
		Type:       migrate.AddField,
		Collection: "nodes",
		Field:      "name",
		FieldType:  query.FieldNumber,
	}

	err := migrate.Apply(context.Background(), db, op)
	if err == nil {
		t.Fatal("expected error for type conflict")
	}
	if !errors.Is(err, migrate.ErrFieldExists) {
		t.Errorf("error = %v, want ErrFieldExists", err)
	}
}

func TestApply_AddCollection(t *testing.T) {
	db := &mockDB{version: 3}

	op := &migrate.AlterOp{
		Type:       migrate.AddCollection,
		Collection: "test_metrics",
		Fields: query.Schema{
			"id":    query.FieldString,
			"value": query.FieldNumber,
		},
		Prefix: "tm:",
	}

	if err := migrate.Apply(context.Background(), db, op); err != nil {
		t.Fatalf("Apply AddCollection: %v", err)
	}

	if db.version != 4 {
		t.Errorf("schema version = %d, want 4", db.version)
	}

	schema := query.CollectionSchema("test_metrics")
	if schema == nil {
		t.Fatal("test_metrics schema is nil")
	}
	if len(schema) != 2 {
		t.Errorf("field count = %d, want 2", len(schema))
	}

	prefix := query.CollectionPrefix("test_metrics")
	if prefix != "tm:" {
		t.Errorf("prefix = %q, want %q", prefix, "tm:")
	}
}

func TestApply_AddCollection_Idempotent(t *testing.T) {
	db := &mockDB{version: 0}

	op := &migrate.AlterOp{
		Type:       migrate.AddCollection,
		Collection: "test_idem_coll",
		Fields: query.Schema{
			"id": query.FieldString,
		},
		Prefix: "tic:",
	}

	if err := migrate.Apply(context.Background(), db, op); err != nil {
		t.Fatalf("Apply 1: %v", err)
	}

	// Same op again should succeed (idempotent).
	if err := migrate.Apply(context.Background(), db, op); err != nil {
		t.Fatalf("Apply 2: %v", err)
	}
}

func TestApply_AddCollection_Exists(t *testing.T) {
	db := &mockDB{version: 0}

	// "nodes" already exists with a different schema.
	op := &migrate.AlterOp{
		Type:       migrate.AddCollection,
		Collection: "nodes",
		Fields: query.Schema{
			"id": query.FieldString,
		},
		Prefix: "xx:",
	}

	err := migrate.Apply(context.Background(), db, op)
	if err == nil {
		t.Fatal("expected error for existing collection")
	}
	if !errors.Is(err, migrate.ErrCollectionExists) {
		t.Errorf("error = %v, want ErrCollectionExists", err)
	}
}

func TestApply_Validation(t *testing.T) {
	db := &mockDB{version: 0}

	tests := []struct {
		name string
		op   migrate.AlterOp
	}{
		{
			name: "AddField/missing collection",
			op:   migrate.AlterOp{Type: migrate.AddField, Field: "x", FieldType: query.FieldString},
		},
		{
			name: "AddField/missing field",
			op:   migrate.AlterOp{Type: migrate.AddField, Collection: "nodes"},
		},
		{
			name: "AddCollection/missing collection",
			op:   migrate.AlterOp{Type: migrate.AddCollection, Fields: query.Schema{"id": query.FieldString}, Prefix: "x:"},
		},
		{
			name: "AddCollection/missing fields",
			op:   migrate.AlterOp{Type: migrate.AddCollection, Collection: "foo", Prefix: "x:"},
		},
		{
			name: "AddCollection/missing prefix",
			op:   migrate.AlterOp{Type: migrate.AddCollection, Collection: "foo", Fields: query.Schema{"id": query.FieldString}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := migrate.Apply(context.Background(), db, &tt.op)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !errors.Is(err, migrate.ErrInvalidOp) {
				t.Errorf("error = %v, want ErrInvalidOp", err)
			}
		})
	}
}

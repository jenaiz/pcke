package migrate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/migrate"
	"github.com/jenaiz/pcke/internal/query"
)

func TestAnalyzeImpact_AddField_IdempotentExisting(t *testing.T) {
	db := &mockDB{version: 5}

	// "name" already exists as FieldString in nodes — should be idempotent.
	op := &migrate.AlterOp{
		Type:       migrate.AddField,
		Collection: "nodes",
		Field:      "name",
		FieldType:  query.FieldString,
	}

	// We can't use AnalyzeImpact with mockDB because it needs TxDB,
	// but we can test the validation and field-level check through Apply.
	err := migrate.Apply(context.Background(), db, op)
	if err != nil {
		t.Fatalf("Apply existing field same type: %v", err)
	}
}

func TestAnalyzeImpact_AddField_TypeConflict(t *testing.T) {
	db := &mockDB{version: 5}

	op := &migrate.AlterOp{
		Type:       migrate.AddField,
		Collection: "nodes",
		Field:      "name",
		FieldType:  query.FieldNumber, // conflicts with existing FieldString
	}

	err := migrate.Apply(context.Background(), db, op)
	if err == nil {
		t.Fatal("expected error for type conflict")
	}
	if !errors.Is(err, migrate.ErrFieldExists) {
		t.Errorf("error = %v, want ErrFieldExists", err)
	}
}

func TestAnalyzeImpact_Validation(t *testing.T) {
	db := &mockDB{version: 0}

	op := &migrate.AlterOp{
		Type:       migrate.AddField,
		Collection: "",
		Field:      "x",
		FieldType:  query.FieldString,
	}

	err := migrate.Apply(context.Background(), db, op)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !errors.Is(err, migrate.ErrInvalidOp) {
		t.Errorf("error = %v, want ErrInvalidOp", err)
	}
}

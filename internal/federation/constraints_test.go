package federation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

func TestCheckOrgConstraints_NoRules(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck

	manifest := &Manifest{}
	violations, err := CheckOrgConstraints(context.Background(), db, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Errorf("expected 0 violations, got %d", len(violations))
	}
}

func TestCheckOrgConstraints_DBAccessViolation(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck

	// Seed a node that represents a direct DB import.
	node := map[string]any{
		"id":     "node-db-import",
		"module": "internal/database",
		"type":   "import",
		"name":   "sql_driver",
	}
	nodeJSON, _ := json.Marshal(node)
	err = db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		wtx.Put([]byte("kn:node-db-import"), nodeJSON) //nolint:errcheck
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	manifest := &Manifest{
		Constraints: ConstraintConfig{
			Rules: []OrgConstraint{
				{Scope: "all", Severity: "must", Description: "No direct DB access outside repository boundary"},
			},
		},
	}

	violations, err := CheckOrgConstraints(context.Background(), db, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}
	if violations[0].NodeID != "node-db-import" {
		t.Errorf("violation node: got %q", violations[0].NodeID)
	}
}

func TestCheckOrgConstraints_APIScope(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck

	// API node without annotations.
	node := map[string]any{
		"id":     "api-endpoint",
		"module": "cmd/api",
		"type":   "function",
		"name":   "HandleUsers",
	}
	nodeJSON, _ := json.Marshal(node)
	err = db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		wtx.Put([]byte("kn:api-endpoint"), nodeJSON) //nolint:errcheck
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	manifest := &Manifest{
		Constraints: ConstraintConfig{
			Rules: []OrgConstraint{
				{Scope: "api", Severity: "should", Description: "All public API endpoints must have OpenAPI annotations"},
			},
		},
	}

	violations, err := CheckOrgConstraints(context.Background(), db, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}
}

func TestPropagateConstraints(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck

	manifest := &Manifest{
		Constraints: ConstraintConfig{
			Rules: []OrgConstraint{
				{Scope: "all", Severity: "must", Description: "Rule 1"},
				{Scope: "api", Severity: "should", Description: "Rule 2"},
			},
		},
	}

	if err := PropagateConstraints(context.Background(), db, manifest); err != nil {
		t.Fatal(err)
	}

	// Verify written.
	var count int
	_ = db.View(context.Background(), func(rtx *tx.ReadTx) error {
		cursor := rtx.Cursor()
		for ok := cursor.Seek([]byte("oc:")); ok; ok = cursor.Next() {
			ks := string(cursor.Key())
			if ks >= "oc:" && ks < "oc;"+string(rune(0xFF)) {
				count++
			} else {
				break
			}
		}
		return nil
	})
	if count != 2 {
		t.Errorf("expected 2 stored constraints, got %d", count)
	}

	// Propagate again (idempotent — replaces old).
	manifest.Constraints.Rules = manifest.Constraints.Rules[:1]
	if err := PropagateConstraints(context.Background(), db, manifest); err != nil {
		t.Fatal(err)
	}
	count = 0
	_ = db.View(context.Background(), func(rtx *tx.ReadTx) error {
		cursor := rtx.Cursor()
		for ok := cursor.Seek([]byte("oc:")); ok; ok = cursor.Next() {
			ks := string(cursor.Key())
			if ks >= "oc:" && ks < "oc;"+string(rune(0xFF)) {
				count++
			} else {
				break
			}
		}
		return nil
	})
	if count != 1 {
		t.Errorf("after re-propagate: expected 1, got %d", count)
	}
}

package migrate_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/migrate"
	"github.com/jenaiz/pcke/internal/kdb/tx"
	"github.com/jenaiz/pcke/internal/query"
)

// seedNodes inserts n JSON records into the "kn:" prefix.
// It grows the database as needed to accommodate the records.
func seedNodes(t *testing.T, db *kdb.DB, n int) {
	t.Helper()
	ctx := context.Background()

	// Grow DB to have enough pages.
	for range (n / 5) + 1 {
		if err := db.Grow(); err != nil {
			t.Fatalf("grow: %v", err)
		}
	}

	for i := range n {
		node := map[string]any{
			"id":        nodeID(i),
			"name":      "node-" + nodeID(i),
			"type":      "function",
			"file_path": "main.go",
			"module":    "core",
		}
		data, err := json.Marshal(node)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
			return wtx.Put([]byte("kn:"+nodeID(i)), data)
		}); err != nil {
			t.Fatalf("seed put: %v", err)
		}
	}
}

func nodeID(i int) string {
	return "node-" + padInt(i)
}

func padInt(i int) string {
	s := ""
	switch {
	case i < 10:
		s = "000"
	case i < 100:
		s = "00"
	case i < 1000:
		s = "0"
	}
	return s + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [10]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func TestBackfill_AddsDefaultValue(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	seedNodes(t, db, 10)

	op := &migrate.AlterOp{
		Type:       migrate.AddField,
		Collection: "nodes",
		Field:      "backfill_test_priority",
		FieldType:  query.FieldNumber,
		Default:    float64(42),
	}

	count, err := migrate.Backfill(context.Background(), db, op, 5)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if count != 10 {
		t.Errorf("updated = %d, want 10", count)
	}

	// Verify all records have the new field.
	ctx := context.Background()
	if err := db.View(ctx, func(rtx *tx.ReadTx) error {
		for i := range 10 {
			val, err := rtx.Get([]byte("kn:" + nodeID(i)))
			if err != nil {
				t.Errorf("get %d: %v", i, err)
				continue
			}
			var m map[string]any
			if err := json.Unmarshal(val, &m); err != nil {
				t.Errorf("unmarshal %d: %v", i, err)
				continue
			}
			v, ok := m["backfill_test_priority"]
			if !ok {
				t.Errorf("record %d missing backfill_test_priority", i)
				continue
			}
			if v != float64(42) {
				t.Errorf("record %d priority = %v, want 42", i, v)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("View: %v", err)
	}
}

func TestBackfill_SkipsExisting(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Insert a record that already has the target field.
	ctx := context.Background()
	node := map[string]any{
		"id":                       "existing-1",
		"name":                     "already-has-field",
		"backfill_skip_test_field": "original",
	}
	data, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		return wtx.Put([]byte("kn:existing-1"), data)
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	op := &migrate.AlterOp{
		Type:       migrate.AddField,
		Collection: "nodes",
		Field:      "backfill_skip_test_field",
		FieldType:  query.FieldString,
		Default:    "default-value",
	}

	count, err := migrate.Backfill(ctx, db, op, 100)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if count != 0 {
		t.Errorf("updated = %d, want 0 (field already exists)", count)
	}

	// Verify original value preserved.
	if err := db.View(ctx, func(rtx *tx.ReadTx) error {
		val, err := rtx.Get([]byte("kn:existing-1"))
		if err != nil {
			return err
		}
		var m map[string]any
		if err := json.Unmarshal(val, &m); err != nil {
			return err
		}
		if m["backfill_skip_test_field"] != "original" {
			t.Errorf("field = %v, want 'original'", m["backfill_skip_test_field"])
		}
		return nil
	}); err != nil {
		t.Fatalf("View: %v", err)
	}
}

func TestBackfill_EmptyCollection(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	op := &migrate.AlterOp{
		Type:       migrate.AddField,
		Collection: "nodes",
		Field:      "backfill_empty_test",
		FieldType:  query.FieldString,
	}

	count, err := migrate.Backfill(context.Background(), db, op, 100)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if count != 0 {
		t.Errorf("updated = %d, want 0", count)
	}
}

func TestBackfill_ChunkedProcessing(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	seedNodes(t, db, 25)

	op := &migrate.AlterOp{
		Type:       migrate.AddField,
		Collection: "nodes",
		Field:      "backfill_chunk_test",
		FieldType:  query.FieldBool,
		Default:    true,
	}

	// Use small batch size to exercise chunking.
	count, err := migrate.Backfill(context.Background(), db, op, 7)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if count != 25 {
		t.Errorf("updated = %d, want 25", count)
	}
}

func TestBackfill_ZeroValues(t *testing.T) {
	tests := []struct {
		name    string
		ft      query.FieldType
		wantVal any
	}{
		{"string", query.FieldString, ""},
		{"number", query.FieldNumber, float64(0)},
		{"bool", query.FieldBool, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			db, err := kdb.Open(dir, nil)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer func() { _ = db.Close() }()

			// Insert one record.
			ctx := context.Background()
			data, _ := json.Marshal(map[string]any{"id": "zv-1"})
			if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
				return wtx.Put([]byte("kn:zv-1"), data)
			}); err != nil {
				t.Fatalf("seed: %v", err)
			}

			op := &migrate.AlterOp{
				Type:       migrate.AddField,
				Collection: "nodes",
				Field:      "zv_" + tt.name,
				FieldType:  tt.ft,
			}

			count, err := migrate.Backfill(ctx, db, op, 100)
			if err != nil {
				t.Fatalf("Backfill: %v", err)
			}
			if count != 1 {
				t.Errorf("updated = %d, want 1", count)
			}

			if err := db.View(ctx, func(rtx *tx.ReadTx) error {
				val, err := rtx.Get([]byte("kn:zv-1"))
				if err != nil {
					return err
				}
				var m map[string]any
				if err := json.Unmarshal(val, &m); err != nil {
					return err
				}
				got := m["zv_"+tt.name]
				if got != tt.wantVal {
					t.Errorf("zero value = %v (%T), want %v (%T)", got, got, tt.wantVal, tt.wantVal)
				}
				return nil
			}); err != nil {
				t.Fatalf("View: %v", err)
			}
		})
	}
}

package wal_test

import (
	"bytes"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/wal"
)

func TestBatchAppend_Empty(t *testing.T) {
	w, _ := newTestWAL(t)

	lsn, err := w.BatchAppend(nil)
	if err != nil {
		t.Fatalf("BatchAppend(nil): %v", err)
	}
	if lsn != 0 {
		t.Errorf("BatchAppend(nil) returned LSN %d, want 0", lsn)
	}
}

func TestBatchAppend_SingleRecord(t *testing.T) {
	w, _ := newTestWAL(t)

	records := []wal.BatchRecord{
		{Type: wal.TypeInsert, Payload: []byte("hello")},
	}
	lsn, err := w.BatchAppend(records)
	if err != nil {
		t.Fatalf("BatchAppend: %v", err)
	}
	if lsn != 1 {
		t.Errorf("first LSN = %d, want 1", lsn)
	}

	// Verify replay.
	var got []wal.Record
	err = w.Replay(func(r wal.Record) error {
		got = append(got, r)
		return nil
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("replayed %d records, want 1", len(got))
	}
	if got[0].Type != wal.TypeInsert {
		t.Errorf("type = %v, want Insert", got[0].Type)
	}
	if !bytes.Equal(got[0].Payload, []byte("hello")) {
		t.Errorf("payload = %q, want %q", got[0].Payload, "hello")
	}
}

func TestBatchAppend_MultipleRecords(t *testing.T) {
	w, _ := newTestWAL(t)

	records := []wal.BatchRecord{
		{Type: wal.TypeInsert, Payload: []byte("key1")},
		{Type: wal.TypeInsert, Payload: []byte("key2")},
		{Type: wal.TypeDelete, Payload: []byte("key3")},
		{Type: wal.TypeCommit, Payload: nil},
	}

	firstLSN, err := w.BatchAppend(records)
	if err != nil {
		t.Fatalf("BatchAppend: %v", err)
	}
	if firstLSN != 1 {
		t.Errorf("first LSN = %d, want 1", firstLSN)
	}

	// Next LSN should be 5 (1,2,3,4 assigned).
	if got := w.NextLSN(); got != 5 {
		t.Errorf("NextLSN = %d, want 5", got)
	}

	// Verify replay.
	var got []wal.Record
	err = w.Replay(func(r wal.Record) error {
		got = append(got, r)
		return nil
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("replayed %d records, want 4", len(got))
	}

	// Verify consecutive LSNs.
	for i, r := range got {
		wantLSN := uint64(i + 1)
		if r.LSN != wantLSN {
			t.Errorf("record[%d].LSN = %d, want %d", i, r.LSN, wantLSN)
		}
	}

	// Verify types.
	wantTypes := []wal.RecordType{wal.TypeInsert, wal.TypeInsert, wal.TypeDelete, wal.TypeCommit}
	for i, r := range got {
		if r.Type != wantTypes[i] {
			t.Errorf("record[%d].Type = %v, want %v", i, r.Type, wantTypes[i])
		}
	}
}

func TestBatchAppend_MixedWithAppend(t *testing.T) {
	w, _ := newTestWAL(t)

	// Single append first.
	lsn1, err := w.Append(wal.TypeInsert, []byte("single"))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if lsn1 != 1 {
		t.Errorf("Append LSN = %d, want 1", lsn1)
	}

	// Then batch.
	batch := []wal.BatchRecord{
		{Type: wal.TypeInsert, Payload: []byte("batch1")},
		{Type: wal.TypeCommit, Payload: nil},
	}
	lsn2, err := w.BatchAppend(batch)
	if err != nil {
		t.Fatalf("BatchAppend: %v", err)
	}
	if lsn2 != 2 {
		t.Errorf("BatchAppend first LSN = %d, want 2", lsn2)
	}

	// Verify total records.
	var count int
	err = w.Replay(func(_ wal.Record) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if count != 3 {
		t.Errorf("replayed %d records, want 3", count)
	}
}

func TestBatchAppend_Closed(t *testing.T) {
	w, _ := newTestWAL(t)
	_ = w.Close()

	_, err := w.BatchAppend([]wal.BatchRecord{
		{Type: wal.TypeInsert, Payload: []byte("x")},
	})
	if err == nil {
		t.Fatal("expected error on closed WAL")
	}
}

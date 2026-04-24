package wal_test

import (
	"bytes"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/wal"
)

// newTestWAL creates a WAL in a temporary directory.
func newTestWAL(t *testing.T) (*wal.WAL, string) {
	t.Helper()
	dir := t.TempDir()
	w, err := wal.Open(dir)
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w, dir
}

// ── Basic Append/Replay ──

func TestAppendReplaySingle(t *testing.T) {
	w, _ := newTestWAL(t)

	lsn, err := w.Append(wal.TypeInsert, []byte("hello"))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if lsn != 1 {
		t.Errorf("LSN = %d, want 1", lsn)
	}

	var records []wal.Record
	if err := w.Replay(func(r wal.Record) error {
		records = append(records, r)
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	if records[0].LSN != 1 {
		t.Errorf("record LSN = %d, want 1", records[0].LSN)
	}
	if records[0].Type != wal.TypeInsert {
		t.Errorf("record Type = %v, want Insert", records[0].Type)
	}
	if string(records[0].Payload) != "hello" {
		t.Errorf("record Payload = %q, want %q", records[0].Payload, "hello")
	}
}

func TestAppendReplayMultiple(t *testing.T) {
	w, _ := newTestWAL(t)

	types := []wal.RecordType{wal.TypeInsert, wal.TypeDelete, wal.TypeCommit, wal.TypeCheckpoint}
	for i, rt := range types {
		lsn, err := w.Append(rt, []byte{byte(i)})
		if err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
		if lsn != uint64(i+1) {
			t.Errorf("LSN[%d] = %d, want %d", i, lsn, i+1)
		}
	}

	var records []wal.Record
	if err := w.Replay(func(r wal.Record) error {
		records = append(records, r)
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if len(records) != 4 {
		t.Fatalf("len(records) = %d, want 4", len(records))
	}

	for i, rt := range types {
		if records[i].Type != rt {
			t.Errorf("record[%d].Type = %v, want %v", i, records[i].Type, rt)
		}
		if records[i].LSN != uint64(i+1) {
			t.Errorf("record[%d].LSN = %d, want %d", i, records[i].LSN, i+1)
		}
	}
}

// ── Empty payload ──

func TestAppendEmptyPayload(t *testing.T) {
	w, _ := newTestWAL(t)

	lsn, err := w.Append(wal.TypeCommit, nil)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if lsn != 1 {
		t.Errorf("LSN = %d, want 1", lsn)
	}

	var records []wal.Record
	if err := w.Replay(func(r wal.Record) error {
		records = append(records, r)
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("len = %d, want 1", len(records))
	}
	if len(records[0].Payload) != 0 {
		t.Errorf("Payload len = %d, want 0", len(records[0].Payload))
	}
}

// ── Large payload ──

func TestAppendLargePayload(t *testing.T) {
	w, _ := newTestWAL(t)

	payload := bytes.Repeat([]byte("X"), 100_000)
	lsn, err := w.Append(wal.TypeInsert, payload)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if lsn != 1 {
		t.Errorf("LSN = %d, want 1", lsn)
	}

	var records []wal.Record
	if err := w.Replay(func(r wal.Record) error {
		records = append(records, r)
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if len(records) != 1 || !bytes.Equal(records[0].Payload, payload) {
		t.Error("large payload roundtrip failed")
	}
}

// ── NextLSN ──

func TestNextLSN(t *testing.T) {
	w, _ := newTestWAL(t)

	if w.NextLSN() != 1 {
		t.Errorf("initial NextLSN = %d, want 1", w.NextLSN())
	}

	for range 5 {
		if _, err := w.Append(wal.TypeInsert, []byte("x")); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	if w.NextLSN() != 6 {
		t.Errorf("NextLSN after 5 appends = %d, want 6", w.NextLSN())
	}
}

// ── Close idempotent ──

func TestCloseIdempotent(t *testing.T) {
	w, _ := newTestWAL(t)

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close[2]: %v", err)
	}
}

// ── Append after Close ──

func TestAppendAfterClose(t *testing.T) {
	w, _ := newTestWAL(t)
	_ = w.Close()

	_, err := w.Append(wal.TypeInsert, []byte("x"))
	if err != wal.ErrClosed {
		t.Errorf("Append after Close = %v, want ErrClosed", err)
	}
}

// ── Replay after Close ──

func TestReplayAfterClose(t *testing.T) {
	w, _ := newTestWAL(t)
	_ = w.Close()

	err := w.Replay(func(_ wal.Record) error { return nil })
	if err != wal.ErrClosed {
		t.Errorf("Replay after Close = %v, want ErrClosed", err)
	}
}

// ── Reopen: records survive ──

func TestReopenPersistence(t *testing.T) {
	dir := t.TempDir()

	// Write some records.
	w1, err := wal.Open(dir)
	if err != nil {
		t.Fatalf("Open[1]: %v", err)
	}
	for i := range 10 {
		if _, err := w1.Append(wal.TypeInsert, []byte{byte(i)}); err != nil { //nolint:gosec
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}
	if err := w1.Close(); err != nil {
		t.Fatalf("Close[1]: %v", err)
	}

	// Reopen and verify.
	w2, err := wal.Open(dir)
	if err != nil {
		t.Fatalf("Open[2]: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if w2.NextLSN() != 11 {
		t.Errorf("NextLSN after reopen = %d, want 11", w2.NextLSN())
	}

	var count int
	if err := w2.Replay(func(_ wal.Record) error {
		count++
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if count != 10 {
		t.Errorf("replayed %d records, want 10", count)
	}

	// Append more records after reopen.
	lsn, err := w2.Append(wal.TypeCommit, nil)
	if err != nil {
		t.Fatalf("Append after reopen: %v", err)
	}
	if lsn != 11 {
		t.Errorf("LSN after reopen append = %d, want 11", lsn)
	}
}

// ── Corrupt tail detection and truncation ──

func TestCorruptTailTruncated(t *testing.T) {
	dir := t.TempDir()

	// Write valid records.
	w1, err := wal.Open(dir)
	if err != nil {
		t.Fatalf("Open[1]: %v", err)
	}
	for i := range 5 {
		if _, err := w1.Append(wal.TypeInsert, []byte{byte(i)}); err != nil { //nolint:gosec
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}
	if err := w1.Close(); err != nil {
		t.Fatalf("Close[1]: %v", err)
	}

	// Append garbage to simulate incomplete write.
	walPath := filepath.Join(dir, "wal.log")
	f, err := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // G304: test path.
	if err != nil {
		t.Fatalf("open for corruption: %v", err)
	}
	if _, err := f.Write([]byte("GARBAGE_INCOMPLETE_RECORD")); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	_ = f.Close()

	// Reopen: should truncate corrupt tail and recover valid records.
	w2, err := wal.Open(dir)
	if err != nil {
		t.Fatalf("Open[2] after corruption: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if w2.NextLSN() != 6 {
		t.Errorf("NextLSN after corrupt-tail repair = %d, want 6", w2.NextLSN())
	}

	var count int
	if err := w2.Replay(func(_ wal.Record) error {
		count++
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if count != 5 {
		t.Errorf("replayed %d records after truncation, want 5", count)
	}
}

func TestCorruptTailPartialHeader(t *testing.T) {
	dir := t.TempDir()

	w1, err := wal.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := w1.Append(wal.TypeInsert, []byte("valid")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Append a partial header (fewer than 13 bytes).
	walPath := filepath.Join(dir, "wal.log")
	f, err := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // G304: test path.
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.Write([]byte{0x01, 0x02, 0x03}); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = f.Close()

	// Reopen: partial header truncated.
	w2, err := wal.Open(dir)
	if err != nil {
		t.Fatalf("Open after partial header: %v", err)
	}
	defer func() { _ = w2.Close() }()

	var count int
	_ = w2.Replay(func(_ wal.Record) error {
		count++
		return nil
	})
	if count != 1 {
		t.Errorf("replayed %d, want 1", count)
	}
}

func TestCorruptCRC(t *testing.T) {
	dir := t.TempDir()

	w1, err := wal.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for i := range 3 {
		if _, err := w1.Append(wal.TypeInsert, []byte{byte(i)}); err != nil { //nolint:gosec
			t.Fatalf("Append: %v", err)
		}
	}
	if err := w1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Corrupt the CRC of the second record.
	walPath := filepath.Join(dir, "wal.log")
	data, err := os.ReadFile(walPath) //nolint:gosec // G304: test path.
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// First record: header(13) + payload(1) + crc(4) = 18 bytes.
	// Second record starts at offset 18. Its CRC is at offset 18+13+1 = 32.
	if len(data) < 36 {
		t.Fatalf("WAL too short: %d bytes", len(data))
	}
	data[32] ^= 0xFF // flip a byte in the CRC

	if err := os.WriteFile(walPath, data, 0o600); err != nil { //nolint:gosec // G703: test path.
		t.Fatalf("write corrupted: %v", err)
	}

	// Reopen: only first record should survive.
	w2, err := wal.Open(dir)
	if err != nil {
		t.Fatalf("Open after CRC corruption: %v", err)
	}
	defer func() { _ = w2.Close() }()

	var count int
	_ = w2.Replay(func(_ wal.Record) error {
		count++
		return nil
	})
	if count != 1 {
		t.Errorf("replayed %d, want 1 (first record only)", count)
	}
}

// ── Empty WAL ──

func TestReplayEmptyWAL(t *testing.T) {
	w, _ := newTestWAL(t)

	var count int
	if err := w.Replay(func(_ wal.Record) error {
		count++
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if count != 0 {
		t.Errorf("replayed %d from empty WAL, want 0", count)
	}
}

// ── Replay is deterministic ──

func TestReplayDeterministic(t *testing.T) {
	w, _ := newTestWAL(t)

	for i := range 20 {
		if _, err := w.Append(wal.TypeInsert, []byte{byte(i)}); err != nil { //nolint:gosec
			t.Fatalf("Append: %v", err)
		}
	}

	// Replay twice and compare.
	collect := func() []wal.Record {
		var recs []wal.Record
		_ = w.Replay(func(r wal.Record) error {
			recs = append(recs, r)
			return nil
		})
		return recs
	}

	r1 := collect()
	r2 := collect()

	if len(r1) != len(r2) {
		t.Fatalf("replay counts differ: %d vs %d", len(r1), len(r2))
	}
	for i := range r1 {
		if r1[i].LSN != r2[i].LSN || r1[i].Type != r2[i].Type ||
			!bytes.Equal(r1[i].Payload, r2[i].Payload) {
			t.Errorf("record[%d] differs between replays", i)
			break
		}
	}
}

// ── RecordType.String ──

func TestRecordTypeString(t *testing.T) {
	tests := []struct {
		rt   wal.RecordType
		want string
	}{
		{wal.TypeInsert, "Insert"},
		{wal.TypeDelete, "Delete"},
		{wal.TypeCommit, "Commit"},
		{wal.TypeCheckpoint, "Checkpoint"},
		{wal.RecordType(99), "Unknown(99)"},
	}
	for _, tt := range tests {
		if got := tt.rt.String(); got != tt.want {
			t.Errorf("RecordType(%d).String() = %q, want %q", tt.rt, got, tt.want)
		}
	}
}

// ── Stress test: many appends then replay ──

func TestStressAppendReplay(t *testing.T) {
	w, _ := newTestWAL(t)
	rng := rand.New(rand.NewPCG(42, 0)) //nolint:gosec // deterministic test RNG
	n := 1000

	type expected struct {
		lsn     uint64
		rt      wal.RecordType
		payload []byte
	}
	var want []expected

	types := []wal.RecordType{wal.TypeInsert, wal.TypeDelete, wal.TypeCommit}
	for i := range n {
		rt := types[i%len(types)]
		pLen := rng.IntN(500)
		p := make([]byte, pLen)
		for j := range p {
			p[j] = byte(rng.IntN(256)) //nolint:gosec
		}
		lsn, err := w.Append(rt, p)
		if err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
		want = append(want, expected{lsn: lsn, rt: rt, payload: p})
	}

	idx := 0
	if err := w.Replay(func(r wal.Record) error {
		if idx >= len(want) {
			t.Fatalf("too many records: index %d", idx)
		}
		exp := want[idx]
		if r.LSN != exp.lsn {
			t.Errorf("record[%d].LSN = %d, want %d", idx, r.LSN, exp.lsn)
		}
		if r.Type != exp.rt {
			t.Errorf("record[%d].Type = %v, want %v", idx, r.Type, exp.rt)
		}
		if !bytes.Equal(r.Payload, exp.payload) {
			t.Errorf("record[%d].Payload mismatch (len %d vs %d)", idx, len(r.Payload), len(exp.payload))
		}
		idx++
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if idx != n {
		t.Errorf("replayed %d, want %d", idx, n)
	}
}

// ── Fuzz test: random garbage appended to WAL ──

func FuzzWALCorruptTail(f *testing.F) {
	// Seed corpus: various garbage patterns.
	f.Add([]byte{0x00})
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	f.Add(bytes.Repeat([]byte{0x42}, 100))
	f.Add(make([]byte, 17)) // exactly minRecordSize

	f.Fuzz(func(t *testing.T, garbage []byte) {
		dir := t.TempDir()

		// Write some valid records first.
		w1, err := wal.Open(dir)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		for i := range 3 {
			if _, err := w1.Append(wal.TypeInsert, []byte{byte(i)}); err != nil { //nolint:gosec
				t.Fatalf("Append: %v", err)
			}
		}
		if err := w1.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		// Append fuzz garbage.
		walPath := filepath.Join(dir, "wal.log")
		appendF, err := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // G304: fuzz test.
		if err != nil {
			t.Fatalf("open for append: %v", err)
		}
		_, _ = appendF.Write(garbage)
		_ = appendF.Close()

		// Must not panic on reopen.
		w2, err := wal.Open(dir)
		if err != nil {
			// Open can fail with certain garbage, that's acceptable.
			return
		}
		defer func() { _ = w2.Close() }()

		// Must not panic on replay.
		var count int
		_ = w2.Replay(func(_ wal.Record) error {
			count++
			return nil
		})

		// The original 3 records should survive.
		if count < 3 {
			// Some garbage could look like valid records,
			// but we should have at least our 3 original ones.
			// (In rare cases the garbage overwrites valid data,
			// but we only append so this shouldn't happen.)
			t.Errorf("replayed %d records, want >= 3", count)
		}
	})
}

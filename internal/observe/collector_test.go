package observe_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/tx"
	"github.com/jenaiz/pcke/internal/observe"
)

// silentLogger discards collector warnings so test output stays quiet.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newDBAndStore opens a fresh kdb in a temp dir + builds a Store.
func newDBAndStore(t *testing.T) (*kdb.DB, *event.Store) {
	t.Helper()
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, event.New(db)
}

func TestCollector_RecordsSessionAndCallObservations(t *testing.T) {
	t.Parallel()

	db, store := newDBAndStore(t)
	c := observe.New(db, store, observe.Options{
		BufferSize:    16,
		MaxBatch:      4,
		FlushInterval: 5 * time.Millisecond,
		Logger:        silentLogger(),
	})

	c.RecordSessionStart(observe.SessionStart{UUID: "s1", Label: "claude"})
	c.RecordCall(observe.Call{
		UUID:        "c1",
		SessionUUID: "s1",
		Tool:        "get_context_for_file",
		Served:      []string{"e:internal/kdb/db.go", "d:adr-0008"},
	})
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sess, err := store.Latest(context.Background(), event.KindObservation, event.SessionOID("s1"))
	if err != nil {
		t.Fatalf("Latest session: %v", err)
	}
	if got, ok := sess.(*event.Observation); !ok || got.Action != event.ActionSession || got.Subject != "claude" {
		t.Fatalf("session observation = %#v, want Action=session Subject=claude", sess)
	}

	call, err := store.Latest(context.Background(), event.KindObservation, event.CallOID("c1"))
	if err != nil {
		t.Fatalf("Latest call: %v", err)
	}
	if got, ok := call.(*event.Observation); !ok || got.Action != event.ActionCall || got.Subject != "get_context_for_file" {
		t.Fatalf("call observation = %#v, want Action=call Subject=get_context_for_file", call)
	}
}

func TestCollector_WritesEdgesContainsBelongsToAndServed(t *testing.T) {
	t.Parallel()

	db, store := newDBAndStore(t)
	c := observe.New(db, store, observe.Options{
		FlushInterval: 5 * time.Millisecond,
		Logger:        silentLogger(),
	})
	c.RecordCall(observe.Call{
		UUID:        "c1",
		SessionUUID: "s1",
		Tool:        "recall",
		Served:      []string{"e:a.go", "d:x"},
	})
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sessRef := event.SessionRef("s1")
	callRef := event.CallRef("c1")

	// contains: session → call
	var containsDsts []string
	err := store.ReverseLinks(context.Background(), callRef, event.EdgeContains, func(l *event.Link) error {
		if l.SrcRef == sessRef {
			containsDsts = append(containsDsts, l.DstRef)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReverseLinks contains: %v", err)
	}
	if len(containsDsts) != 1 || containsDsts[0] != callRef {
		t.Errorf("contains edge missing: got %v, want [%q]", containsDsts, callRef)
	}

	// belongs_to: call → session
	var belongsSrcs []string
	err = store.ReverseLinks(context.Background(), sessRef, event.EdgeBelongsTo, func(l *event.Link) error {
		belongsSrcs = append(belongsSrcs, l.SrcRef)
		return nil
	})
	if err != nil {
		t.Fatalf("ReverseLinks belongs_to: %v", err)
	}
	if len(belongsSrcs) != 1 || belongsSrcs[0] != callRef {
		t.Errorf("belongs_to edge missing: got %v, want [%q]", belongsSrcs, callRef)
	}

	// served: call → e:/d:
	wantServed := []string{"d:x", "e:a.go"}
	var gotServed []string
	for _, dst := range wantServed {
		err = store.ReverseLinks(context.Background(), dst, event.EdgeServed, func(l *event.Link) error {
			gotServed = append(gotServed, l.DstRef)
			return nil
		})
		if err != nil {
			t.Fatalf("ReverseLinks served (%s): %v", dst, err)
		}
	}
	sort.Strings(gotServed)
	if got, want := gotServed, wantServed; !equalStrings(got, want) {
		t.Errorf("served dsts = %v, want %v", got, want)
	}
}

func TestCollector_SessionIdempotentWithinProcess(t *testing.T) {
	t.Parallel()

	db, store := newDBAndStore(t)
	c := observe.New(db, store, observe.Options{
		FlushInterval: 5 * time.Millisecond,
		Logger:        silentLogger(),
	})
	for i := 0; i < 5; i++ {
		c.RecordSessionStart(observe.SessionStart{UUID: "s1"})
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// One version expected, regardless of how many SessionStart enqueues.
	var versions int
	err := store.History(context.Background(), event.KindObservation, event.SessionOID("s1"),
		func(_ event.Event) error { versions++; return nil })
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if versions != 1 {
		t.Errorf("session versions = %d, want 1 (idempotent within process)", versions)
	}
}

func TestCollector_RecordCallWithoutSessionStart_StillWritesSession(t *testing.T) {
	t.Parallel()

	db, store := newDBAndStore(t)
	c := observe.New(db, store, observe.Options{
		FlushInterval: 5 * time.Millisecond,
		Logger:        silentLogger(),
	})
	c.RecordCall(observe.Call{
		UUID:        "c1",
		SessionUUID: "s9",
		Tool:        "recall",
	})
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := store.Latest(context.Background(), event.KindObservation, event.SessionOID("s9")); err != nil {
		t.Errorf("session observation not auto-created: %v", err)
	}
}

func TestCollector_DropsRecordsWhenDisabled(t *testing.T) {
	t.Parallel()

	db, store := newDBAndStore(t)
	c := observe.New(db, store, observe.Options{
		FlushInterval: 5 * time.Millisecond,
		Logger:        silentLogger(),
	})
	c.SetEnabled(false)
	c.RecordSessionStart(observe.SessionStart{UUID: "s1"})
	c.RecordCall(observe.Call{UUID: "c1", SessionUUID: "s1", Tool: "recall"})
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := store.Latest(context.Background(), event.KindObservation, event.SessionOID("s1"))
	if !errors.Is(err, event.ErrNotFound) {
		t.Errorf("session leaked while disabled: err=%v", err)
	}
}

func TestCollector_ClosedRecordsAreNoops(t *testing.T) {
	t.Parallel()

	db, store := newDBAndStore(t)
	c := observe.New(db, store, observe.Options{
		FlushInterval: 5 * time.Millisecond,
		Logger:        silentLogger(),
	})
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// These must not panic.
	c.RecordSessionStart(observe.SessionStart{UUID: "s1"})
	c.RecordCall(observe.Call{UUID: "c1", SessionUUID: "s1", Tool: "recall"})
	if err := c.Close(); err != nil {
		t.Errorf("second Close should be a no-op: %v", err)
	}
	// And no records made it to storage.
	if _, err := store.Latest(context.Background(), event.KindObservation, event.SessionOID("s1")); !errors.Is(err, event.ErrNotFound) {
		t.Errorf("session leaked after close: err=%v", err)
	}
}

func TestCollector_DropCounterIncrementsOnFullBuffer(t *testing.T) {
	t.Parallel()

	// Use a tiny buffer and a writer that blocks until released so the
	// background flush cannot drain. Then prove that excess records bump
	// the drop counter rather than blocking the caller.
	blocker := &blockingWriter{ch: make(chan struct{})}
	c := observe.New(blocker, blocker, observe.Options{
		BufferSize:    1,
		MaxBatch:      1,
		FlushInterval: 24 * time.Hour, // effectively never tick
		Logger:        silentLogger(),
	})

	// Fill the buffer.
	c.RecordSessionStart(observe.SessionStart{UUID: "s1"})
	// The first one may be in-flight in the goroutine; force a second so
	// either the channel or the in-flight slot is occupied.
	c.RecordSessionStart(observe.SessionStart{UUID: "s2"})

	// Send enough enqueues to overflow.
	for i := 0; i < 100; i++ {
		c.RecordSessionStart(observe.SessionStart{UUID: "s_drop"})
	}
	if got := c.Stats().Dropped; got == 0 {
		t.Errorf("Stats().Dropped = 0, want >0 with blocked writer + tiny buffer")
	}

	// Release the writer and shut down cleanly.
	close(blocker.ch)
	_ = c.Close()
}

// BenchmarkEnqueue measures the wall-clock cost of Record* under load.
// PRD v5.2 §5.7 F14.T2 target: < 1 ms p99 enqueue overhead.
func BenchmarkEnqueue(b *testing.B) {
	dir := b.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		b.Fatalf("kdb.Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	c := observe.New(db, event.New(db), observe.Options{
		BufferSize:    1 << 14,
		MaxBatch:      256,
		FlushInterval: 50 * time.Millisecond,
		Logger:        silentLogger(),
	})
	defer func() { _ = c.Close() }()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.RecordCall(observe.Call{
			UUID:        "c",
			SessionUUID: "s",
			Tool:        "recall",
		})
	}
}

// blockingWriter satisfies both Updater + Writer; Update blocks on ch
// until the test releases it. Concurrent calls share the same gate.
type blockingWriter struct {
	ch chan struct{}
}

func (b *blockingWriter) Update(_ context.Context, _ func(*tx.WriteTx) error) error {
	<-b.ch
	return nil
}

func (b *blockingWriter) AppendInTx(_ *tx.WriteTx, _ event.Event) ([]byte, error) {
	return nil, nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

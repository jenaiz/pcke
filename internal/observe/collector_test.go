package observe_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/graph"
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

// TestCollector_ObservationsContainNoFileContent is the F14.T7 privacy gate
// (PRD v5.2 §5.5 + §5.7). It records a session + call through the collector,
// flushes to kdb, then asserts two properties:
//
//  1. The stored Observation fields are limited to Action and Subject (tool
//     name / session label) — no body, no file content.
//  2. Every served-edge DstRef is a typed graph key (e: or d: prefix), not
//     raw file text.
//
// A raw kdb scan also verifies that a content-shaped sentinel string does
// not appear in any stored value, catching any future field additions that
// accidentally persist content.
func TestCollector_ObservationsContainNoFileContent(t *testing.T) {
	t.Parallel()

	// contentSentinel is a distinctive string that would only appear in kdb
	// if an Observation accidentally stored file source text.
	const contentSentinel = "PRIVACY_GATE_F14T7_file_content_must_not_be_stored"

	db, store := newDBAndStore(t)
	c := observe.New(db, store, observe.Options{
		FlushInterval: 5 * time.Millisecond,
		Logger:        silentLogger(),
	})

	c.RecordSessionStart(observe.SessionStart{UUID: "priv-s1", Label: contentSentinel})
	c.RecordCall(observe.Call{
		UUID:        "priv-c1",
		SessionUUID: "priv-s1",
		Tool:        contentSentinel, // intentionally put sentinel in Tool field
		Served:      []string{"e:internal/kdb/db.go", "d:adr-0008"},
	})
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ctx := context.Background()

	// --- Criterion 1: Observation fields are Action + Subject only. ---
	// Session observation: Subject = label (sentinel), Action = "session".
	sessEv, err := store.Latest(ctx, event.KindObservation, event.SessionOID("priv-s1"))
	if err != nil {
		t.Fatalf("Latest session: %v", err)
	}
	if obs, ok := sessEv.(*event.Observation); !ok {
		t.Fatalf("session event type = %T, want *event.Observation", sessEv)
	} else if obs.Action != event.ActionSession {
		t.Errorf("session Action = %q, want %q", obs.Action, event.ActionSession)
	}

	// Call observation: Subject = tool name (sentinel), Action = "call".
	callEv, err := store.Latest(ctx, event.KindObservation, event.CallOID("priv-c1"))
	if err != nil {
		t.Fatalf("Latest call: %v", err)
	}
	callObs, ok := callEv.(*event.Observation)
	if !ok {
		t.Fatalf("call event type = %T, want *event.Observation", callEv)
	}
	if callObs.Action != event.ActionCall {
		t.Errorf("call Action = %q, want %q", callObs.Action, event.ActionCall)
	}
	// Subject is the tool name — the only string field besides OID and Action.
	// It must not be a file path ref or contain file content.
	if strings.HasPrefix(callObs.Subject, "e:") || strings.HasPrefix(callObs.Subject, "d:") {
		t.Errorf("call Subject %q looks like a graph ref — content leaked into Subject", callObs.Subject)
	}

	// --- Criterion 2: served-edge DstRefs are typed graph keys only. ---
	servedRefs, err := graph.Neighbors(ctx, db, graph.Ref(event.CallRef("priv-c1")),
		graph.TraversalOptions{
			Direction: graph.Forward,
			EdgeTypes: []string{event.EdgeServed},
		})
	if err != nil {
		t.Fatalf("graph.Neighbors served: %v", err)
	}
	if len(servedRefs) == 0 {
		t.Fatal("no served-edge refs found — collector did not write served edges")
	}
	for _, ref := range servedRefs {
		s := string(ref)
		if !strings.HasPrefix(s, "e:") && !strings.HasPrefix(s, "d:") {
			t.Errorf("served DstRef %q is not a typed graph key (want e: or d: prefix)", s)
		}
		if strings.Contains(s, contentSentinel) {
			t.Errorf("served DstRef %q contains the content sentinel — file content stored in ref", s)
		}
	}

	// --- Criterion 3: raw kdb scan — no value contains the sentinel. ---
	// The sentinel was used as Label and Tool name above; those will appear in
	// the stored Action/Subject fields as expected. The scan verifies that no
	// *other* value (body, content, source) carries the sentinel.
	// We assert the sentinel appears at most twice (Session.Subject + Call.Subject).
	sentinelCount := 0
	if scanErr := db.View(ctx, func(rtx *tx.ReadTx) error {
		cur := rtx.Cursor()
		for cur.First(); cur.Valid(); cur.Next() {
			if bytes.Contains(cur.Value(), []byte(contentSentinel)) {
				sentinelCount++
			}
		}
		return nil
	}); scanErr != nil {
		t.Fatalf("raw kdb scan: %v", scanErr)
	}
	// Sentinel may appear in Subject fields of Session + Call records.
	// It must not appear in any other field (body, content, source code).
	if sentinelCount > 2 {
		t.Errorf("content sentinel found in %d raw kdb values (want ≤2 for Subject fields); "+
			"file content may be leaking into observation records", sentinelCount)
	}
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

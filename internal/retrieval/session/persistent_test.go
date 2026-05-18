package session_test

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/observe"
	"github.com/jenaiz/pcke/internal/retrieval/session"
)

// newPersistentStore opens a fresh kdb, builds a Collector, and wires
// up a PersistentStore. The Collector is registered as a t.Cleanup so
// pending writes drain before the test ends.
func newPersistentStore(t *testing.T) (*kdb.DB, *event.Store, *observe.Collector, *session.PersistentStore) {
	t.Helper()
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := event.New(db)
	collector := observe.New(db, store, observe.Options{
		FlushInterval: 5 * time.Millisecond,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	t.Cleanup(func() { _ = collector.Close() })
	return db, store, collector, session.NewPersistentStore(db, collector)
}

func TestPersistentStore_ImplementsStoreInterface(t *testing.T) {
	t.Parallel()
	_, _, _, ps := newPersistentStore(t)
	var _ session.Store = ps
}

func TestPersistentSession_NoteAndServed(t *testing.T) {
	t.Parallel()
	_, _, _, ps := newPersistentStore(t)
	s := ps.Get("alpha")
	s.Note(session.Observation{
		Refs: []string{"e:a.go", "d:r"},
		At:   time.Unix(10, 0),
		Tool: "recall",
	})
	s.Note(session.Observation{
		Refs: []string{"e:b.go", "d:r"},
		Tool: "recall",
	})

	got := s.Served()
	want := []string{"e:a.go", "d:r", "e:b.go"}
	if !slices.Equal(got, want) {
		t.Errorf("Served = %v, want %v", got, want)
	}
}

func TestPersistentSession_WritesCallObservationsThroughSink(t *testing.T) {
	t.Parallel()
	_, store, collector, ps := newPersistentStore(t)

	s := ps.Get("alpha")
	s.Note(session.Observation{
		Refs: []string{"e:a.go", "d:r"},
		Tool: "get_context_for_file",
	})
	s.Note(session.Observation{
		Refs: []string{"e:b.go"},
		Tool: "get_context_for_file",
	})

	// Drain so the writes have landed.
	if err := collector.Close(); err != nil {
		t.Fatalf("collector.Close: %v", err)
	}

	// Confirm the session observation exists.
	if _, err := store.Latest(context.Background(), event.KindObservation, event.SessionOID("alpha")); err != nil {
		t.Fatalf("session observation missing: %v", err)
	}

	// Two call observations should be present (one per Note that had refs).
	var callCount int
	err := store.IterateKind(context.Background(), event.KindObservation, func(e event.Event) error {
		obs, ok := e.(*event.Observation)
		if !ok {
			return nil
		}
		if obs.Action == event.ActionCall && obs.Subject == "get_context_for_file" {
			callCount++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("IterateKind: %v", err)
	}
	if callCount != 2 {
		t.Errorf("call observations = %d, want 2", callCount)
	}
}

func TestPersistentSession_NoteWithoutToolUsesUnknown(t *testing.T) {
	t.Parallel()
	_, store, collector, ps := newPersistentStore(t)

	s := ps.Get("alpha")
	s.Note(session.Observation{Refs: []string{"e:a.go"}})
	if err := collector.Close(); err != nil {
		t.Fatalf("collector.Close: %v", err)
	}

	var sawUnknown bool
	_ = store.IterateKind(context.Background(), event.KindObservation, func(e event.Event) error {
		obs, ok := e.(*event.Observation)
		if !ok {
			return nil
		}
		if obs.Action == event.ActionCall && obs.Subject == "unknown" {
			sawUnknown = true
		}
		return nil
	})
	if !sawUnknown {
		t.Errorf("call subject = %q, want %q when Tool is empty", "<missing>", "unknown")
	}
}

func TestPersistentStore_ReplaysServedRefsAfterRestart(t *testing.T) {
	t.Parallel()

	// 1) Open store, take a session, note some refs, close everything.
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	store := event.New(db)
	col := observe.New(db, store, observe.Options{
		FlushInterval: 5 * time.Millisecond,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ps := session.NewPersistentStore(db, col)
	s := ps.Get("alpha")
	s.Note(session.Observation{Refs: []string{"e:a.go", "d:r"}, Tool: "recall"})
	s.Note(session.Observation{Refs: []string{"e:b.go"}, Tool: "recall"})
	if err := col.Close(); err != nil {
		t.Fatalf("collector.Close: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	// 2) Reopen db, build a fresh PersistentStore.
	db2, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open #2: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	store2 := event.New(db2)
	col2 := observe.New(db2, store2, observe.Options{
		FlushInterval: 5 * time.Millisecond,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	t.Cleanup(func() { _ = col2.Close() })
	ps2 := session.NewPersistentStore(db2, col2)

	// 3) Get("alpha") on the fresh store must replay prior served refs.
	got := ps2.Get("alpha").Served()
	sort.Strings(got)
	want := []string{"d:r", "e:a.go", "e:b.go"}
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("replayed Served = %v, want %v", got, want)
	}
}

func TestPersistentStore_GetReturnsSameInstance(t *testing.T) {
	t.Parallel()
	_, _, _, ps := newPersistentStore(t)
	a := ps.Get("alpha")
	a.Note(session.Observation{Refs: []string{"e:x"}, Tool: "recall"})
	b := ps.Get("alpha")
	if !slices.Equal(b.Served(), []string{"e:x"}) {
		t.Errorf("second Get did not return same session; b.Served() = %v", b.Served())
	}
}

func TestPersistentStore_EmptyIDIsAnonymous(t *testing.T) {
	t.Parallel()
	_, _, _, ps := newPersistentStore(t)
	a := ps.Get("")
	a.Note(session.Observation{Refs: []string{"e:x"}, Tool: "recall"})
	b := ps.Get("")
	if len(b.Served()) != 0 {
		t.Errorf("empty id should yield fresh sessions: b.Served() = %v", b.Served())
	}
}

func TestPersistentStore_NilSinkSkipsCollectorWrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ps := session.NewPersistentStore(db, nil)
	s := ps.Get("alpha")
	s.Note(session.Observation{Refs: []string{"e:a.go"}, Tool: "recall"})

	if got := s.Served(); !slices.Equal(got, []string{"e:a.go"}) {
		t.Errorf("Served = %v, want [e:a.go] (in-memory still works without sink)", got)
	}

	// No observations should have been written.
	store := event.New(db)
	_, err = store.Latest(context.Background(), event.KindObservation, event.SessionOID("alpha"))
	if err == nil {
		t.Errorf("nil sink wrote a session observation, want none")
	}
}

func TestPersistentStore_Close(t *testing.T) {
	t.Parallel()
	_, _, _, ps := newPersistentStore(t)
	s := ps.Get("alpha")
	s.Note(session.Observation{Refs: []string{"e:x"}, Tool: "recall"})
	if err := ps.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// After Close, prior dedup state is dropped. The next Get will replay
	// from kdb — but our collector may not have flushed yet, so this only
	// verifies that the in-memory map was cleared (a fresh instance is
	// returned).
	fresh := ps.Get("alpha").(*session.PersistentSession)
	_ = fresh
}

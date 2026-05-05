package event

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// Type alias to keep the migration-flow test signatures readable.
type txWriteTx = tx.WriteTx

// newTestStore opens a fresh kdb in a temp dir and returns a Store with a
// deterministic clock. The Close cleanup runs via t.Cleanup.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("kdb.Close: %v", err)
		}
	})
	s := New(db)
	// Deterministic clock: increments by 1ms each call.
	base := time.Unix(0, 1_715_000_000_000_000_000).UTC()
	tick := time.Duration(0)
	s.now = func() time.Time {
		t := base.Add(tick)
		tick += time.Millisecond
		return t
	}
	return s
}

func TestAppend_FirstVersionStampsHeader(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	e := &Entity{EID: "internal/kdb/db.go", Type: "file"}
	key, err := s.Append(ctx, e)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	wantKey, _ := BuildKey(KindEntity, "internal/kdb/db.go", 1)
	if !bytes.Equal(key, wantKey) {
		t.Errorf("key = %q, want %q", key, wantKey)
	}
	hdr := e.Header()
	if hdr.Version != 1 {
		t.Errorf("Version = %d, want 1", hdr.Version)
	}
	if hdr.Supersedes != nil {
		t.Errorf("Supersedes = %q, want nil for first version", hdr.Supersedes)
	}
	if hdr.Lifecycle != LifecycleActive {
		t.Errorf("Lifecycle = %d, want LifecycleActive (default)", hdr.Lifecycle)
	}
	if hdr.CreatedAt.IsZero() {
		t.Errorf("CreatedAt was not stamped")
	}
}

func TestAppend_SequentialVersionsLinkSupersedes(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	const id = "internal/kdb/db.go"
	var keys [][]byte
	for i := 1; i <= 3; i++ {
		e := &Entity{EID: id, Type: "file"}
		key, err := s.Append(ctx, e)
		if err != nil {
			t.Fatalf("Append v%d: %v", i, err)
		}
		hdr := e.Header()
		if got := hdr.Version; got != uint64(i) {
			t.Errorf("Version = %d, want %d", got, i)
		}
		if i == 1 {
			if hdr.Supersedes != nil {
				t.Errorf("v1.Supersedes = %q, want nil", hdr.Supersedes)
			}
		} else {
			if !bytes.Equal(hdr.Supersedes, keys[i-2]) {
				t.Errorf("v%d.Supersedes = %q, want %q", i, hdr.Supersedes, keys[i-2])
			}
		}
		keys = append(keys, key)
	}
}

func TestAppend_PreservesUserSetCreatedAt(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	custom := time.Unix(0, 1_700_000_000_000_000_000).UTC()
	e := &Entity{
		Hdr:  Header{CreatedAt: custom},
		EID:  "x",
		Type: "file",
	}
	if _, err := s.Append(ctx, e); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !e.Header().CreatedAt.Equal(custom) {
		t.Errorf("CreatedAt = %v, want %v (preserved)", e.Header().CreatedAt, custom)
	}
}

func TestAppend_ErrorPaths(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.Append(ctx, nil); !errors.Is(err, ErrCorrupt) {
		t.Errorf("nil event: got %v, want ErrCorrupt", err)
	}
	if _, err := s.Append(ctx, &Entity{}); !errors.Is(err, ErrEmptyID) {
		t.Errorf("empty id: got %v, want ErrEmptyID", err)
	}
}

func TestLatest_NotFound(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.Latest(ctx, KindEntity, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
	if _, err := s.Latest(ctx, KindEntity, ""); !errors.Is(err, ErrEmptyID) {
		t.Errorf("empty id: got %v, want ErrEmptyID", err)
	}
}

func TestLatest_ReturnsHighestVersion(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		e := &Entity{EID: "x", Type: fmt.Sprintf("v%d", i)}
		if _, err := s.Append(ctx, e); err != nil {
			t.Fatalf("Append v%d: %v", i, err)
		}
	}
	got, err := s.Latest(ctx, KindEntity, "x")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	ent, ok := got.(*Entity)
	if !ok {
		t.Fatalf("got %T, want *Entity", got)
	}
	if ent.Header().Version != 5 {
		t.Errorf("Version = %d, want 5", ent.Header().Version)
	}
	if ent.Type != "v5" {
		t.Errorf("Type = %q, want %q (latest payload)", ent.Type, "v5")
	}
}

func TestLatest_DistinctIdsDoNotBleed(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	// Two ids whose escaped chain prefixes share a common prefix in the
	// B+tree, exercising the bytes.HasPrefix stop condition.
	ids := []string{"foo", "foo.go", "foo/bar", "foo/baz", "foobar"}
	for _, id := range ids {
		for i := 1; i <= 2; i++ {
			if _, err := s.Append(ctx, &Entity{EID: id, Type: fmt.Sprintf("%s-v%d", id, i)}); err != nil {
				t.Fatalf("Append %q v%d: %v", id, i, err)
			}
		}
	}
	for _, id := range ids {
		got, err := s.Latest(ctx, KindEntity, id)
		if err != nil {
			t.Fatalf("Latest %q: %v", id, err)
		}
		if got.ID() != id {
			t.Errorf("Latest(%q).ID() = %q", id, got.ID())
		}
		if got.Header().Version != 2 {
			t.Errorf("Latest(%q).Version = %d, want 2", id, got.Header().Version)
		}
	}
}

func TestHistory_NotFound(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	err := s.History(ctx, KindEntity, "missing", func(Event) error { return nil })
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestHistory_OldestFirst(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	const id = "x"
	for i := 1; i <= 4; i++ {
		if _, err := s.Append(ctx, &Entity{EID: id, Type: fmt.Sprintf("v%d", i)}); err != nil {
			t.Fatalf("Append v%d: %v", i, err)
		}
	}

	var versions []uint64
	err := s.History(ctx, KindEntity, id, func(e Event) error {
		versions = append(versions, e.Header().Version)
		return nil
	})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	want := []uint64{1, 2, 3, 4}
	if fmt.Sprintf("%v", versions) != fmt.Sprintf("%v", want) {
		t.Errorf("versions = %v, want %v", versions, want)
	}
}

func TestHistory_CallbackErrorAborts(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		if _, err := s.Append(ctx, &Entity{EID: "x", Type: fmt.Sprintf("v%d", i)}); err != nil {
			t.Fatalf("Append v%d: %v", i, err)
		}
	}

	stopErr := errors.New("stop")
	count := 0
	err := s.History(ctx, KindEntity, "x", func(Event) error {
		count++
		if count == 2 {
			return stopErr
		}
		return nil
	})
	if !errors.Is(err, stopErr) {
		t.Errorf("got %v, want stopErr", err)
	}
	if count != 2 {
		t.Errorf("invoked %d times, want 2", count)
	}
}

func TestIterateKind_Empty(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	count := 0
	err := s.IterateKind(ctx, KindEntity, func(Event) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("IterateKind: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestIterateKind_OneLatestPerId(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	ids := []string{"a", "b", "c"}
	for _, id := range ids {
		for i := 1; i <= 3; i++ {
			if _, err := s.Append(ctx, &Entity{EID: id, Type: fmt.Sprintf("v%d", i)}); err != nil {
				t.Fatalf("Append %q v%d: %v", id, i, err)
			}
		}
	}

	got := make(map[string]uint64)
	err := s.IterateKind(ctx, KindEntity, func(e Event) error {
		got[e.ID()] = e.Header().Version
		return nil
	})
	if err != nil {
		t.Fatalf("IterateKind: %v", err)
	}
	if len(got) != len(ids) {
		t.Errorf("ids yielded = %d, want %d", len(got), len(ids))
	}
	for _, id := range ids {
		if got[id] != 3 {
			t.Errorf("id %q latest version = %d, want 3", id, got[id])
		}
	}
}

func TestIterateKind_DoesNotBleedAcrossKinds(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.Append(ctx, &Entity{EID: "ent-1", Type: "file"}); err != nil {
		t.Fatalf("Append entity: %v", err)
	}
	if _, err := s.Append(ctx, &Decision{
		DID: "dec-1", Title: "title", Severity: SeverityShould, Scope: ScopeFile,
	}); err != nil {
		t.Fatalf("Append decision: %v", err)
	}

	var entities, decisions int
	if err := s.IterateKind(ctx, KindEntity, func(Event) error { entities++; return nil }); err != nil {
		t.Fatalf("IterateKind entity: %v", err)
	}
	if err := s.IterateKind(ctx, KindDecision, func(Event) error { decisions++; return nil }); err != nil {
		t.Fatalf("IterateKind decision: %v", err)
	}
	if entities != 1 || decisions != 1 {
		t.Errorf("entities=%d decisions=%d, want 1/1", entities, decisions)
	}
}

func TestAppendInTx_MigrationFlow(t *testing.T) {
	t.Parallel()
	// Migrations need to write multiple events inside a single WriteTx.
	// Drive appendInTx directly via db.Update to confirm the contract.
	s := newTestStore(t)
	ctx := context.Background()

	err := s.db.Update(ctx, func(wtx *txWriteTx) error {
		for _, id := range []string{"a", "b", "c"} {
			if _, err := s.appendInTx(wtx, &Entity{EID: id, Type: "file"}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// All three should be readable post-commit.
	for _, id := range []string{"a", "b", "c"} {
		got, err := s.Latest(ctx, KindEntity, id)
		if err != nil {
			t.Fatalf("Latest(%q): %v", id, err)
		}
		if got.Header().Version != 1 {
			t.Errorf("Latest(%q).Version = %d, want 1", id, got.Header().Version)
		}
	}
}

func TestAppendInTx_ErrorPaths(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	err := s.db.Update(ctx, func(wtx *txWriteTx) error {
		if _, err := s.appendInTx(wtx, nil); !errors.Is(err, ErrCorrupt) {
			t.Errorf("nil event: got %v, want ErrCorrupt", err)
		}
		if _, err := s.appendInTx(wtx, &Entity{}); !errors.Is(err, ErrEmptyID) {
			t.Errorf("empty id: got %v, want ErrEmptyID", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
}

func TestLatest_CorruptValueReturnsErr(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.Append(ctx, &Entity{EID: "x", Type: "file"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// Overwrite the v1 record with bytes that pass the schema-version
	// check (first byte == 1) but claim 5 fields with no data following,
	// triggering the corrupt-record path during field reads.
	key, _ := BuildKey(KindEntity, "x", 1)
	err := s.db.Update(ctx, func(wtx *txWriteTx) error {
		return wtx.Put(key, []byte{1, 5})
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if _, err := s.Latest(ctx, KindEntity, "x"); !errors.Is(err, ErrCorrupt) {
		t.Errorf("got %v, want wrap of ErrCorrupt", err)
	}
}

func TestAppend_DefaultClockUsedWhenInjectionAbsent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// New() without overriding s.now — uses time.Now().
	s := New(db)
	before := time.Now().Add(-time.Second)

	e := &Entity{EID: "x", Type: "file"}
	if _, err := s.Append(context.Background(), e); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got := e.Header().CreatedAt
	if got.Before(before) {
		t.Errorf("CreatedAt = %v, want >= %v (real wall clock)", got, before)
	}
	if got.After(time.Now().Add(time.Second)) {
		t.Errorf("CreatedAt = %v, drifted into the future", got)
	}
}

func TestAppend_RoundTripsAllKinds(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	cases := []Event{
		&Entity{EID: "ent-1", Type: "file", Path: "internal/kdb/db.go"},
		&Decision{DID: "adr-0008", Title: "Pivot", Severity: SeverityMust, Scope: ScopeGlobal, Source: "adr"},
		&Link{SrcRef: "e:foo.go", EdgeType: "imports", DstRef: "e:bar.go"},
		&Observation{OID: "obs-1", Action: "scan", Subject: "e:foo.go"},
		&Outcome{XID: "out-1", Type: "respect", Subject: "d:adr-0008"},
	}
	for _, in := range cases {
		t.Run(in.Kind().String(), func(t *testing.T) {
			if _, err := s.Append(ctx, in); err != nil {
				t.Fatalf("Append: %v", err)
			}
			// Link.ID is computed from refs; other kinds carry their id
			// directly. Either way, ID() is the right cache key.
			id := in.ID()
			got, err := s.Latest(ctx, in.Kind(), id)
			if err != nil {
				t.Fatalf("Latest: %v", err)
			}
			if got.Kind() != in.Kind() {
				t.Errorf("Kind = %s, want %s", got.Kind(), in.Kind())
			}
			if got.ID() != id {
				t.Errorf("ID = %q, want %q", got.ID(), id)
			}
		})
	}
}

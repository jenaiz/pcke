package event

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
)

// timedStore returns a Store backed by a fresh kdb whose clock is driven
// by the supplied stamps slice. Each call to s.now() pops the next stamp;
// running out of stamps is a test bug and panics.
func timedStore(t *testing.T, stamps []time.Time) *Store {
	t.Helper()
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := New(db)
	idx := 0
	s.now = func() time.Time {
		if idx >= len(stamps) {
			t.Fatalf("clock exhausted: tests asked for %d stamps, only %d provided", idx+1, len(stamps))
		}
		v := stamps[idx]
		idx++
		return v
	}
	return s
}

func TestAsOf_NotFoundEmptyID(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	if _, err := s.AsOf(context.Background(), KindEntity, "", time.Now()); !errors.Is(err, ErrEmptyID) {
		t.Errorf("got %v, want ErrEmptyID", err)
	}
}

func TestAsOf_NoChain(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	if _, err := s.AsOf(context.Background(), KindEntity, "x", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestAsOf_BeforeFirstVersion(t *testing.T) {
	t.Parallel()
	t1 := time.Unix(1700000000, 0).UTC()
	t2 := t1.Add(time.Hour)
	s := timedStore(t, []time.Time{t1, t2})

	for i := 1; i <= 2; i++ {
		if _, err := s.Append(context.Background(), &Entity{EID: "x", Type: fmt.Sprintf("v%d", i)}); err != nil {
			t.Fatalf("Append v%d: %v", i, err)
		}
	}
	cutoff := t1.Add(-time.Minute)
	if _, err := s.AsOf(context.Background(), KindEntity, "x", cutoff); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound (all versions are after cutoff)", err)
	}
}

func TestAsOf_AtBoundaryReturnsThatVersion(t *testing.T) {
	t.Parallel()
	t1 := time.Unix(1700000000, 0).UTC()
	t2 := t1.Add(time.Hour)
	s := timedStore(t, []time.Time{t1, t2})

	for i := 1; i <= 2; i++ {
		if _, err := s.Append(context.Background(), &Entity{EID: "x", Type: fmt.Sprintf("v%d", i)}); err != nil {
			t.Fatalf("Append v%d: %v", i, err)
		}
	}
	got, err := s.AsOf(context.Background(), KindEntity, "x", t1)
	if err != nil {
		t.Fatalf("AsOf(t1): %v", err)
	}
	if got.Header().Version != 1 {
		t.Errorf("Version = %d, want 1 (boundary exact match)", got.Header().Version)
	}
}

func TestAsOf_BetweenVersionsReturnsEarlier(t *testing.T) {
	t.Parallel()
	t1 := time.Unix(1700000000, 0).UTC()
	t2 := t1.Add(time.Hour)
	t3 := t2.Add(time.Hour)
	s := timedStore(t, []time.Time{t1, t2, t3})

	for i := 1; i <= 3; i++ {
		if _, err := s.Append(context.Background(), &Entity{EID: "x", Type: fmt.Sprintf("v%d", i)}); err != nil {
			t.Fatalf("Append v%d: %v", i, err)
		}
	}
	cutoff := t2.Add(30 * time.Minute) // between v2 and v3
	got, err := s.AsOf(context.Background(), KindEntity, "x", cutoff)
	if err != nil {
		t.Fatalf("AsOf: %v", err)
	}
	if got.Header().Version != 2 {
		t.Errorf("Version = %d, want 2 (cutoff between v2 and v3)", got.Header().Version)
	}
}

func TestAsOf_FutureReturnsLatest(t *testing.T) {
	t.Parallel()
	t1 := time.Unix(1700000000, 0).UTC()
	t2 := t1.Add(time.Hour)
	s := timedStore(t, []time.Time{t1, t2})

	for i := 1; i <= 2; i++ {
		if _, err := s.Append(context.Background(), &Entity{EID: "x", Type: fmt.Sprintf("v%d", i)}); err != nil {
			t.Fatalf("Append v%d: %v", i, err)
		}
	}
	future := t2.Add(24 * time.Hour)
	got, err := s.AsOf(context.Background(), KindEntity, "x", future)
	if err != nil {
		t.Fatalf("AsOf(future): %v", err)
	}
	if got.Header().Version != 2 {
		t.Errorf("Version = %d, want 2 (future cutoff = latest)", got.Header().Version)
	}
}

func TestResolveSupersedes_EmptyKey(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	if _, err := s.ResolveSupersedes(context.Background(), nil, 5); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("got %v, want ErrInvalidKey", err)
	}
}

func TestResolveSupersedes_StartKeyMissing(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	missing, _ := BuildKey(KindEntity, "ghost", 1)
	if _, err := s.ResolveSupersedes(context.Background(), missing, 5); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestResolveSupersedes_NoPriorIsSingleEntry(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.Append(ctx, &Entity{EID: "x", Type: "file"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	startKey, _ := BuildKey(KindEntity, "x", 1)
	chain, err := s.ResolveSupersedes(ctx, startKey, 5)
	if err != nil {
		t.Fatalf("ResolveSupersedes: %v", err)
	}
	if len(chain) != 1 {
		t.Fatalf("len(chain) = %d, want 1", len(chain))
	}
	if chain[0].Header().Version != 1 {
		t.Errorf("chain[0].Version = %d, want 1", chain[0].Header().Version)
	}
}

func TestResolveSupersedes_FollowsFullChain(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	// Three appends → three versions linked by Supersedes pointers.
	const id = "x"
	for i := 1; i <= 3; i++ {
		if _, err := s.Append(ctx, &Entity{EID: id, Type: fmt.Sprintf("v%d", i)}); err != nil {
			t.Fatalf("Append v%d: %v", i, err)
		}
	}
	startKey, _ := BuildKey(KindEntity, id, 3)
	chain, err := s.ResolveSupersedes(ctx, startKey, 5)
	if err != nil {
		t.Fatalf("ResolveSupersedes: %v", err)
	}
	if len(chain) != 3 {
		t.Fatalf("len(chain) = %d, want 3", len(chain))
	}
	wantVersions := []uint64{3, 2, 1}
	for i, want := range wantVersions {
		if got := chain[i].Header().Version; got != want {
			t.Errorf("chain[%d].Version = %d, want %d", i, got, want)
		}
	}
}

func TestResolveSupersedes_HopLimitExceeded(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	for i := 1; i <= 4; i++ {
		if _, err := s.Append(ctx, &Entity{EID: "x", Type: fmt.Sprintf("v%d", i)}); err != nil {
			t.Fatalf("Append v%d: %v", i, err)
		}
	}
	startKey, _ := BuildKey(KindEntity, "x", 4)
	// maxHops=1 reads start + at most 1 follow = 2 records, then aborts.
	if _, err := s.ResolveSupersedes(ctx, startKey, 1); !errors.Is(err, ErrSupersedesLoop) {
		t.Errorf("got %v, want ErrSupersedesLoop", err)
	}
}

func TestResolveSupersedes_ZeroHopsReadsOnlyStart(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		if _, err := s.Append(ctx, &Entity{EID: "x", Type: fmt.Sprintf("v%d", i)}); err != nil {
			t.Fatalf("Append v%d: %v", i, err)
		}
	}
	startKey, _ := BuildKey(KindEntity, "x", 3)
	if _, err := s.ResolveSupersedes(ctx, startKey, 0); !errors.Is(err, ErrSupersedesLoop) {
		t.Errorf("got %v, want ErrSupersedesLoop (0 hops with prior version present)", err)
	}
}

func TestResolveSupersedes_DanglingPointer(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	// v1 then v2 normally, then delete v1 to make v2's Supersedes dangle.
	if _, err := s.Append(ctx, &Entity{EID: "x", Type: "v1"}); err != nil {
		t.Fatalf("Append v1: %v", err)
	}
	if _, err := s.Append(ctx, &Entity{EID: "x", Type: "v2"}); err != nil {
		t.Fatalf("Append v2: %v", err)
	}
	v1Key, _ := BuildKey(KindEntity, "x", 1)
	if err := s.db.Update(ctx, func(wtx *txWriteTx) error {
		return wtx.Delete(v1Key)
	}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	v2Key, _ := BuildKey(KindEntity, "x", 2)
	_, err := s.ResolveSupersedes(ctx, v2Key, 5)
	if !errors.Is(err, ErrSupersedesMissing) {
		t.Errorf("got %v, want ErrSupersedesMissing", err)
	}
}

func TestResolveSupersedes_CallerCanMutateStartKey(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	// ResolveSupersedes copies startKey internally; mutating the caller's
	// slice after the call must not affect the returned chain.
	if _, err := s.Append(ctx, &Entity{EID: "x", Type: "v1"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	startKey, _ := BuildKey(KindEntity, "x", 1)
	chain, err := s.ResolveSupersedes(ctx, startKey, 0)
	if err != nil {
		t.Fatalf("ResolveSupersedes: %v", err)
	}
	for i := range startKey {
		startKey[i] ^= 0xff
	}
	if len(chain) != 1 || chain[0].ID() != "x" {
		t.Errorf("chain affected by caller mutation: %+v", chain)
	}
}

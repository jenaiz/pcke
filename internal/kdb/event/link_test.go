package event

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

func TestAppendLink_PairedWriteCreatesBothRecords(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	l := &Link{SrcRef: "e:foo.go", EdgeType: "imports", DstRef: "e:bar.go"}
	fwdKey, err := s.AppendLink(ctx, l)
	if err != nil {
		t.Fatalf("AppendLink: %v", err)
	}

	// Forward key must round-trip.
	wantFwd, _ := BuildKey(KindLink, l.ID(), 1)
	if !bytes.Equal(fwdKey, wantFwd) {
		t.Errorf("forward key = %q, want %q", fwdKey, wantFwd)
	}

	// Reverse-index entry must exist and point at the forward key.
	rkey, _ := BuildReverseLinkKey(l.DstRef, l.EdgeType, l.SrcRef)
	if err := s.db.View(ctx, func(rtx *tx.ReadTx) error {
		val, err := rtx.Get(rkey)
		if err != nil {
			return err
		}
		if !bytes.Equal(val, fwdKey) {
			t.Errorf("lr: value = %q, want %q (forward key)", val, fwdKey)
		}
		return nil
	}); err != nil {
		t.Fatalf("View: %v", err)
	}
}

func TestAppendLink_NewVersionOverwritesReverseIndex(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	l := &Link{SrcRef: "e:foo.go", EdgeType: "imports", DstRef: "e:bar.go"}
	if _, err := s.AppendLink(ctx, l); err != nil {
		t.Fatalf("AppendLink v1: %v", err)
	}
	v2 := &Link{SrcRef: l.SrcRef, EdgeType: l.EdgeType, DstRef: l.DstRef}
	v2Key, err := s.AppendLink(ctx, v2)
	if err != nil {
		t.Fatalf("AppendLink v2: %v", err)
	}

	rkey, _ := BuildReverseLinkKey(l.DstRef, l.EdgeType, l.SrcRef)
	err = s.db.View(ctx, func(rtx *tx.ReadTx) error {
		val, err := rtx.Get(rkey)
		if err != nil {
			return err
		}
		if !bytes.Equal(val, v2Key) {
			t.Errorf("lr: value = %q, want v2 key %q", val, v2Key)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}
}

func TestAppendLink_TombstoneViaSupersededLifecycle(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	l1 := &Link{SrcRef: "e:foo.go", EdgeType: "imports", DstRef: "e:bar.go"}
	if _, err := s.AppendLink(ctx, l1); err != nil {
		t.Fatalf("AppendLink v1: %v", err)
	}

	// v2 explicitly tombstones the edge.
	l2 := &Link{
		Hdr:      Header{Lifecycle: LifecycleSuperseded},
		SrcRef:   l1.SrcRef,
		EdgeType: l1.EdgeType,
		DstRef:   l1.DstRef,
	}
	if _, err := s.AppendLink(ctx, l2); err != nil {
		t.Fatalf("AppendLink v2: %v", err)
	}

	var got []*Link
	err := s.ReverseLinks(ctx, l1.DstRef, l1.EdgeType, func(link *Link) error {
		got = append(got, link)
		return nil
	})
	if err != nil {
		t.Fatalf("ReverseLinks: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("yielded %d links, want 1", len(got))
	}
	if got[0].Header().Lifecycle != LifecycleSuperseded {
		t.Errorf("Lifecycle = %d, want %d (callers see the tombstone)",
			got[0].Header().Lifecycle, LifecycleSuperseded)
	}
}

func TestReverseLinks_NoMatches(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	count := 0
	err := s.ReverseLinks(ctx, "e:absent", "imports", func(*Link) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("ReverseLinks: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestReverseLinks_BasicTraversal(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	const dst = "e:internal/kdb/btree"
	srcs := []string{"e:cmd/pcke", "e:internal/analysis", "e:internal/output"}
	for _, src := range srcs {
		if _, err := s.AppendLink(ctx, &Link{
			SrcRef: src, EdgeType: "imports", DstRef: dst,
		}); err != nil {
			t.Fatalf("AppendLink %s -> %s: %v", src, dst, err)
		}
	}

	gotSrcs := make(map[string]bool)
	err := s.ReverseLinks(ctx, dst, "imports", func(l *Link) error {
		gotSrcs[l.SrcRef] = true
		return nil
	})
	if err != nil {
		t.Fatalf("ReverseLinks: %v", err)
	}
	if len(gotSrcs) != len(srcs) {
		t.Errorf("srcs yielded = %d, want %d", len(gotSrcs), len(srcs))
	}
	for _, src := range srcs {
		if !gotSrcs[src] {
			t.Errorf("src %q not yielded", src)
		}
	}
}

func TestReverseLinks_EdgeTypeFilter(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	const dst = "e:internal/kdb/btree"
	if _, err := s.AppendLink(ctx, &Link{SrcRef: "e:cmd/pcke", EdgeType: "imports", DstRef: dst}); err != nil {
		t.Fatalf("Append imports: %v", err)
	}
	if _, err := s.AppendLink(ctx, &Link{SrcRef: "d:adr-0008", EdgeType: "decision_link", DstRef: dst}); err != nil {
		t.Fatalf("Append decision_link: %v", err)
	}

	var imports, decisions int
	if err := s.ReverseLinks(ctx, dst, "imports", func(*Link) error {
		imports++
		return nil
	}); err != nil {
		t.Fatalf("ReverseLinks imports: %v", err)
	}
	if err := s.ReverseLinks(ctx, dst, "decision_link", func(*Link) error {
		decisions++
		return nil
	}); err != nil {
		t.Fatalf("ReverseLinks decision_link: %v", err)
	}
	if imports != 1 || decisions != 1 {
		t.Errorf("imports=%d decisions=%d, want 1/1 (edge filter)", imports, decisions)
	}
}

func TestReverseLinks_DoesNotBleedAcrossDsts(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	// dst1 and dst2 share a textual prefix; the cursor stop condition must
	// distinguish them via the trailing ':' that follows the dst segment.
	links := []Link{
		{SrcRef: "e:a", EdgeType: "imports", DstRef: "e:foo"},
		{SrcRef: "e:b", EdgeType: "imports", DstRef: "e:foo"},
		{SrcRef: "e:c", EdgeType: "imports", DstRef: "e:foobar"},
	}
	for _, l := range links {
		l := l
		if _, err := s.AppendLink(ctx, &l); err != nil {
			t.Fatalf("AppendLink: %v", err)
		}
	}

	var fooSrcs []string
	err := s.ReverseLinks(ctx, "e:foo", "imports", func(l *Link) error {
		fooSrcs = append(fooSrcs, l.SrcRef)
		return nil
	})
	if err != nil {
		t.Fatalf("ReverseLinks: %v", err)
	}
	if len(fooSrcs) != 2 {
		t.Errorf("e:foo yielded %d srcs, want 2 (e:foobar must not bleed)", len(fooSrcs))
	}
}

func TestReverseLinks_CallbackErrorAborts(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	const dst = "e:dst"
	for i, src := range []string{"e:a", "e:b", "e:c"} {
		_ = i
		if _, err := s.AppendLink(ctx, &Link{
			SrcRef: src, EdgeType: "imports", DstRef: dst,
		}); err != nil {
			t.Fatalf("AppendLink: %v", err)
		}
	}

	stopErr := errors.New("stop")
	count := 0
	err := s.ReverseLinks(ctx, dst, "imports", func(*Link) error {
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
		t.Errorf("count = %d, want 2", count)
	}
}

func TestReverseLinks_EmptyArgs(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	err := s.ReverseLinks(ctx, "", "imports", func(*Link) error { return nil })
	if !errors.Is(err, ErrEmptyID) {
		t.Errorf("empty dst: got %v, want ErrEmptyID", err)
	}
	err = s.ReverseLinks(ctx, "e:foo", "", func(*Link) error { return nil })
	if !errors.Is(err, ErrEmptyID) {
		t.Errorf("empty edge: got %v, want ErrEmptyID", err)
	}
}

func TestAppendLink_NilArgs(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	if _, err := s.AppendLink(context.Background(), nil); !errors.Is(err, ErrCorrupt) {
		t.Errorf("got %v, want ErrCorrupt", err)
	}
}

func TestAppendLink_RejectsEmptyRefs(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	cases := []*Link{
		{SrcRef: "", EdgeType: "imports", DstRef: "e:bar"},
		{SrcRef: "e:foo", EdgeType: "", DstRef: "e:bar"},
		{SrcRef: "e:foo", EdgeType: "imports", DstRef: ""},
	}
	for _, l := range cases {
		// Empty refs cause the Link.ID() composite to start with ":" or
		// have an empty middle segment; either Encode or appendInTx
		// rejects.
		if _, err := s.AppendLink(ctx, l); err == nil {
			t.Errorf("AppendLink(%+v): want error, got nil", l)
		}
	}
}

func TestReverseLinks_DanglingForwardKeyReportsError(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	l := &Link{SrcRef: "e:foo", EdgeType: "imports", DstRef: "e:bar"}
	fwdKey, err := s.AppendLink(ctx, l)
	if err != nil {
		t.Fatalf("AppendLink: %v", err)
	}
	// Delete the forward record but leave the lr: pointer dangling.
	if err := s.db.Update(ctx, func(wtx *tx.WriteTx) error {
		return wtx.Delete(fwdKey)
	}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	err = s.ReverseLinks(ctx, l.DstRef, l.EdgeType, func(*Link) error { return nil })
	if !errors.Is(err, ErrSupersedesMissing) {
		// Reuses ErrSupersedesMissing semantically: the dangling-pointer
		// failure mode is the same.
		// (If we ever introduce a more specific sentinel, update this.)
		// Accept any non-btree error so the test isn't brittle to wrapping.
		if !errors.Is(err, btree.ErrKeyNotFound) {
			t.Errorf("got %v, want ErrSupersedesMissing or ErrKeyNotFound", err)
		}
	}
}

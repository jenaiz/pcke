package event

import (
	"context"
	"fmt"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// Benchmark fixture: 100 sources × 10 outgoing links each = 1000 forward
// links, distributed evenly across 100 dsts so each dst has ~10 inbound
// edges. The benchmarks then time "find all srcs pointing at one
// specific dst with one specific edge type" — the canonical reverse-
// traversal query.
const (
	benchSrcs        = 100
	benchDsts        = 100
	benchEdgesPerSrc = 10
)

// setupLinkFixture seeds the benchmark store with 1000 forward links
// (and their paired reverse-index records). Writes are batched into
// transactions of 100 link-pairs to keep page churn manageable; the DB
// is pre-grown to provide enough free pages.
func setupLinkFixture(b *testing.B) *Store {
	b.Helper()
	dir := b.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		b.Fatalf("kdb.Open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })

	// Pre-grow the data file so the freelist always has headroom.
	// kdb's CoW page allocation pattern needs continuous free pages
	// during write-heavy seeding (cf. internal/kdb/migrate/bench_test.go).
	const totalLinks = benchSrcs * benchEdgesPerSrc
	for range (totalLinks / 5) + 4 {
		if err := db.Grow(); err != nil {
			b.Fatalf("db.Grow: %v", err)
		}
	}

	s := New(db)
	ctx := context.Background()
	const batch = 100
	links := make([]*Link, 0, batch)
	flush := func() error {
		if len(links) == 0 {
			return nil
		}
		err := db.Update(ctx, func(wtx *tx.WriteTx) error {
			for _, l := range links {
				if _, err := s.appendInTx(wtx, l); err != nil {
					return err
				}
			}
			return nil
		})
		links = links[:0]
		return err
	}

	for i := 0; i < benchSrcs; i++ {
		src := fmt.Sprintf("e:src-%03d", i)
		for j := 0; j < benchEdgesPerSrc; j++ {
			dst := fmt.Sprintf("e:dst-%03d", (i*benchEdgesPerSrc+j)%benchDsts)
			links = append(links, &Link{
				SrcRef:   src,
				EdgeType: "imports",
				DstRef:   dst,
			})
			if len(links) == batch {
				if err := flush(); err != nil {
					b.Fatalf("seed batch: %v", err)
				}
			}
		}
	}
	if err := flush(); err != nil {
		b.Fatalf("seed final batch: %v", err)
	}

	return s
}

// BenchmarkReverseLinks_WithIndex measures the lr:-backed reverse-
// traversal path. Expected work: O(matches_for_dst) ≈ 10 records read.
func BenchmarkReverseLinks_WithIndex(b *testing.B) {
	s := setupLinkFixture(b)
	target := "e:dst-000"
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var count int
		err := s.ReverseLinks(ctx, target, "imports", func(*Link) error {
			count++
			return nil
		})
		if err != nil {
			b.Fatalf("ReverseLinks: %v", err)
		}
		if count == 0 {
			b.Fatalf("expected >0 matches; fixture has 10 inbound for dst-000")
		}
	}
}

// BenchmarkReverseLinks_LinearScan simulates "no lr: index": iterate
// every link in the store and filter by (DstRef, EdgeType) inside the
// callback. Expected work: O(total_links) = 1000 records decoded per
// query, regardless of matches. The lr:-backed query reads ~100x less.
func BenchmarkReverseLinks_LinearScan(b *testing.B) {
	s := setupLinkFixture(b)
	target := "e:dst-000"
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var count int
		err := s.IterateKind(ctx, KindLink, func(e Event) error {
			link := e.(*Link)
			if link.DstRef == target && link.EdgeType == "imports" {
				count++
			}
			return nil
		})
		if err != nil {
			b.Fatalf("IterateKind: %v", err)
		}
		if count == 0 {
			b.Fatalf("expected >0 matches; fixture has 10 inbound for dst-000")
		}
	}
}

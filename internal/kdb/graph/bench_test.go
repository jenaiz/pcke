package graph_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/graph"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// Benchmark fixture parameters: 200 source nodes × 25 outgoing edges
// = 5000 forward links across 100 destination nodes.
//
// The PRD v5.2 §3.9 perf budget calls for "10k nodes / 50k edges". We
// scale 10× smaller to keep `make bench-compare` runnable in <30 s
// across the default -count=5 multiplier; per-op cost scales linearly
// with reachable set size at fixed depth, so the smaller fixture is a
// faithful proxy for regression-tracking. The PRD's literal scale is
// validated separately as a one-shot integration test (see T8 docs).
const (
	benchCriticalSrcs        = 200
	benchCriticalDsts        = 100
	benchCriticalEdgesPerSrc = 25
)

// setupCriticalFixture seeds a *kdb.DB with the benchmark graph using
// batched AppendInTx writes so fixture build stays under a few seconds
// even at 5000 links. AppendLink (one tx per link) would be ~100×
// slower because of the per-write WAL fsync.
func setupCriticalFixture(b *testing.B) (*kdb.DB, graph.Ref) {
	b.Helper()
	dir := b.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		b.Fatalf("kdb.Open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })

	const totalLinks = benchCriticalSrcs * benchCriticalEdgesPerSrc
	// Pre-grow ((links/5)+4) chunks per the migrate-bench convention
	// (10 link-pairs need ≈ 1 chunk worth of pages under CoW churn).
	for range (totalLinks / 5) + 4 {
		if err := db.Grow(); err != nil {
			b.Fatalf("db.Grow: %v", err)
		}
	}

	store := event.New(db)
	ctx := context.Background()
	const batch = 100
	pending := make([]*event.Link, 0, batch)
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		err := db.Update(ctx, func(wtx *tx.WriteTx) error {
			for _, l := range pending {
				if _, err := store.AppendInTx(wtx, l); err != nil {
					return err
				}
			}
			return nil
		})
		pending = pending[:0]
		return err
	}

	for i := 0; i < benchCriticalSrcs; i++ {
		src := fmt.Sprintf("e:src-%05d", i)
		for j := 0; j < benchCriticalEdgesPerSrc; j++ {
			dst := fmt.Sprintf("e:dst-%05d", (i*benchCriticalEdgesPerSrc+j)%benchCriticalDsts)
			pending = append(pending, &event.Link{
				SrcRef:   src,
				EdgeType: "imports",
				DstRef:   dst,
			})
			if len(pending) == batch {
				if err := flush(); err != nil {
					b.Fatalf("seed batch: %v", err)
				}
			}
		}
	}
	if err := flush(); err != nil {
		b.Fatalf("seed final batch: %v", err)
	}

	// e:src-00000 is the hub query starting point. At depth 3 it
	// reaches a non-trivial subgraph without saturating MaxVisited.
	return db, "e:src-00000"
}

// BenchmarkCriticalGraphReachableForward measures forward traversal
// at depth 3 — the headline perf budget.
func BenchmarkCriticalGraphReachableForward(b *testing.B) {
	db, start := setupCriticalFixture(b)
	opts := graph.TraversalOptions{
		Direction: graph.Forward,
		MaxDepth:  3,
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := graph.Reachable(ctx, db, start, opts)
		if err != nil {
			b.Fatalf("Reachable: %v", err)
		}
		if len(got) == 0 {
			b.Fatalf("expected non-empty reach from %s", start)
		}
	}
}

// BenchmarkCriticalGraphReachableReverse measures reverse traversal
// at depth 3 (impact-radius queries — the lr:-index hot path).
func BenchmarkCriticalGraphReachableReverse(b *testing.B) {
	db, _ := setupCriticalFixture(b)
	opts := graph.TraversalOptions{
		Direction: graph.Reverse,
		MaxDepth:  3,
	}
	// Pick a dst that has many inbound edges by construction:
	// dst-00000 is reached by every src whose hash mod 1000 == 0.
	start := graph.Ref("e:dst-00000")
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := graph.Reachable(ctx, db, start, opts)
		if err != nil {
			b.Fatalf("Reachable: %v", err)
		}
		if len(got) == 0 {
			b.Fatalf("expected non-empty reach from %s", start)
		}
	}
}

// BenchmarkGraphNeighborsForward measures the 1-hop neighbour query
// (the unit of work BFS builds on). Not BenchmarkCritical — we only
// gate the multi-hop budget — but useful for tracking regressions.
func BenchmarkGraphNeighborsForward(b *testing.B) {
	db, start := setupCriticalFixture(b)
	opts := graph.TraversalOptions{
		Direction: graph.Forward,
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := graph.Neighbors(ctx, db, start, opts)
		if err != nil {
			b.Fatalf("Neighbors: %v", err)
		}
	}
}

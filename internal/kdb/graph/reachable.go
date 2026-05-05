package graph

import (
	"context"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// Reachable returns every node reachable from start within MaxDepth hops
// in the configured direction(s).
//
// The returned slice contains each ref at most once even when reached
// from multiple sides under Direction.Both. The breadth-first traversal
// runs inside a single db.View, so all reads see a consistent snapshot.
//
// If the visited-set cap (MaxVisited) is exceeded, Reachable returns
// the partial result accumulated so far together with ErrVisitedCapExceeded.
// Callers can inspect the partial result to decide whether to retry with
// a higher cap, narrower edge filter, or smaller MaxDepth.
//
// Edges whose Lifecycle is Superseded are skipped, matching Neighbors.
func Reachable(ctx context.Context, db *kdb.DB, start Ref, opts TraversalOptions) ([]Ref, error) {
	if err := validateStart(start); err != nil {
		return nil, err
	}
	resolved, err := opts.resolved()
	if err != nil {
		return nil, err
	}

	state := newBFSState(start, resolved)
	err = db.View(ctx, func(rtx *tx.ReadTx) error {
		return state.run(rtx, resolved)
	})
	if err != nil {
		return state.result, err
	}
	if state.capExceeded {
		return state.result, ErrVisitedCapExceeded
	}
	return state.result, nil
}

// queued is one entry in the BFS frontier: a ref we still need to expand
// in a specific direction at a specific depth.
type queued struct {
	ref   Ref
	depth int
	dir   Direction
}

// bfsState holds the working set for a single Reachable call.
type bfsState struct {
	visited     map[visitKey]struct{}
	emitted     map[Ref]struct{}
	result      []Ref
	queue       []queued
	capExceeded bool
}

func newBFSState(start Ref, opts resolvedOpts) *bfsState {
	s := &bfsState{
		visited: make(map[visitKey]struct{}),
		emitted: make(map[Ref]struct{}),
	}
	if opts.direction == Forward || opts.direction == Both {
		s.enqueueStart(start, Forward)
	}
	if opts.direction == Reverse || opts.direction == Both {
		s.enqueueStart(start, Reverse)
	}
	return s
}

func (s *bfsState) enqueueStart(ref Ref, dir Direction) {
	key := visitKey{ref: ref, dir: dir}
	if _, dup := s.visited[key]; dup {
		return
	}
	s.visited[key] = struct{}{}
	s.queue = append(s.queue, queued{ref: ref, depth: 0, dir: dir})
}

// emit handles a single discovered neighbour: visited-set bookkeeping,
// MaxVisited enforcement, output deduplication, and queue extension.
func (s *bfsState) emit(item queued, n Ref, maxVisited int) {
	if s.capExceeded {
		return
	}
	key := visitKey{ref: n, dir: item.dir}
	if _, dup := s.visited[key]; dup {
		return
	}
	s.visited[key] = struct{}{}
	if len(s.visited) > maxVisited {
		s.capExceeded = true
		return
	}
	if _, dup := s.emitted[n]; !dup {
		s.emitted[n] = struct{}{}
		s.result = append(s.result, n)
	}
	s.queue = append(s.queue, queued{ref: n, depth: item.depth + 1, dir: item.dir})
}

// run drains the queue inside the supplied transaction.
func (s *bfsState) run(rtx *tx.ReadTx, opts resolvedOpts) error {
	for len(s.queue) > 0 {
		if s.capExceeded {
			return nil
		}
		item := s.queue[0]
		s.queue = s.queue[1:]
		if item.depth >= opts.maxDepth {
			continue
		}
		emit := func(n Ref) { s.emit(item, n, opts.maxVisited) }
		if err := s.expand(rtx, item, opts, emit); err != nil {
			return err
		}
	}
	return nil
}

// expand walks the appropriate side(s) for one queue item.
func (s *bfsState) expand(rtx *tx.ReadTx, item queued, opts resolvedOpts, emit func(Ref)) error {
	switch item.dir {
	case Forward:
		return walkForward(rtx, item.ref, opts, emit)
	case Reverse:
		return walkReverse(rtx, item.ref, opts, emit)
	case Both:
		// Defensive: Both is split into per-side queue items at start,
		// so this case should not appear during a normal BFS run.
		if err := walkForward(rtx, item.ref, opts, emit); err != nil {
			return err
		}
		return walkReverse(rtx, item.ref, opts, emit)
	}
	return nil
}

// ImpactRadius returns every node that transitively reaches target by
// following edges in reverse (i.e., the set of upstream dependencies).
// It is shorthand for Reachable(target, TraversalOptions{Direction: Reverse, MaxDepth: maxDepth}).
//
// A maxDepth of 0 is normalised to DefaultMaxDepth.
func ImpactRadius(ctx context.Context, db *kdb.DB, target Ref, maxDepth int) ([]Ref, error) {
	return Reachable(ctx, db, target, TraversalOptions{
		Direction: Reverse,
		MaxDepth:  maxDepth,
	})
}

// visitKey is the (ref, direction) tuple used to discriminate the BFS
// visited set so a node reached from each side under Direction.Both is
// explored independently from each side.
type visitKey struct {
	ref Ref
	dir Direction
}

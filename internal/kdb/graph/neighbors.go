package graph

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// Neighbors returns the 1-hop refs reachable from start in the given
// direction(s), filtered by EdgeTypes.
//
// Forward neighbors are the DstRef of every Link whose SrcRef == start;
// reverse neighbors are the SrcRef of every Link whose DstRef == start.
// Direction.Both returns the union (no duplicate refs in the slice;
// duplicates that arise from a node reachable via both sides are
// emitted once).
//
// The current version of each link is consulted (latest write wins);
// AsOf-based pinning is added in commit 3 of F12.T3.
//
// Edges whose Lifecycle is Superseded are skipped.
func Neighbors(ctx context.Context, db *kdb.DB, start Ref, opts TraversalOptions) ([]Ref, error) {
	if err := validateStart(start); err != nil {
		return nil, err
	}
	resolved, err := opts.resolved()
	if err != nil {
		return nil, err
	}

	seen := make(map[Ref]struct{})
	var out []Ref
	emit := func(r Ref) {
		if _, dup := seen[r]; dup {
			return
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}

	err = db.View(ctx, func(rtx *tx.ReadTx) error {
		if resolved.direction == Forward || resolved.direction == Both {
			if err := walkForward(rtx, start, resolved, emit); err != nil {
				return err
			}
		}
		if resolved.direction == Reverse || resolved.direction == Both {
			if err := walkReverse(rtx, start, resolved, emit); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// walkForward enumerates the active version of every link with
// SrcRef == start, applying the edge filter and lifecycle skip.
//
// The cursor scan is over the prefix l:<escape(escape(start))>\c which
// sorts all keys belonging to start contiguously by lexicographic order
// on (escape(edge), escape(dst), version-digits). Versions of the same
// (src, edge, dst) tuple share the body; we collapse them to the
// "active" one — the latest by default, or the highest with
// CreatedAt <= AsOf when AsOf is set.
func walkForward(rtx *tx.ReadTx, start Ref, opts resolvedOpts, emit func(Ref)) error {
	prefix := forwardSrcPrefix(start)
	var (
		currentTuple []byte // last seen tuple body
		chosen       *linkSnapshot
	)
	flush := func() {
		if currentTuple == nil || chosen == nil {
			return
		}
		if !opts.edgeAllowed(chosen.edge) || chosen.lifecycle == event.LifecycleSuperseded {
			return
		}
		emit(chosen.dst)
	}

	cursor := rtx.Cursor()
	if !cursor.Seek(prefix) {
		return nil
	}
	for cursor.Valid() {
		key := cursor.Key()
		if !bytes.HasPrefix(key, prefix) {
			break
		}
		body, err := splitChainTuple(key)
		if err != nil {
			return err
		}
		if !bytes.Equal(body, currentTuple) {
			flush()
			currentTuple = append(currentTuple[:0], body...)
			chosen = nil
		}
		snap, ok := decodeLinkSnapshot(cursor.Value())
		if !ok {
			return fmt.Errorf("forward walk: malformed link value")
		}
		if opts.asOf == nil || !snap.createdAt.After(*opts.asOf) {
			snapCopy := snap
			chosen = &snapCopy
		}
		cursor.Next()
	}
	flush()
	return nil
}

// walkReverse enumerates lr:<escape(start)>: entries, resolves each to
// the active version of its forward link, and yields the SrcRef.
//
// Without AsOf the lr: value points directly at the latest forward key,
// so a single Get suffices. With AsOf set, we instead scan the chain
// for that link to find the version active at the pinned timestamp;
// the lr: index serves only to enumerate which links exist for this
// dst (their identity, not their version).
func walkReverse(rtx *tx.ReadTx, start Ref, opts resolvedOpts, emit func(Ref)) error {
	prefix := reverseDstPrefix(start)
	cursor := rtx.Cursor()
	if !cursor.Seek(prefix) {
		return nil
	}
	for cursor.Valid() {
		key := cursor.Key()
		if !bytes.HasPrefix(key, prefix) {
			break
		}
		fwdKey := append([]byte(nil), cursor.Value()...)
		if err := emitReverseLink(rtx, fwdKey, opts, emit); err != nil {
			return err
		}
		cursor.Next()
	}
	return nil
}

// emitReverseLink resolves one lr: entry's forward record (latest by
// default, AsOf-pinned otherwise), applies filters, and emits SrcRef.
func emitReverseLink(rtx *tx.ReadTx, fwdKey []byte, opts resolvedOpts, emit func(Ref)) error {
	if opts.asOf == nil {
		return emitReverseLinkLatest(rtx, fwdKey, opts, emit)
	}
	return emitReverseLinkAsOf(rtx, fwdKey, opts, emit)
}

func emitReverseLinkLatest(rtx *tx.ReadTx, fwdKey []byte, opts resolvedOpts, emit func(Ref)) error {
	fwdValue, err := rtx.Get(fwdKey)
	if err != nil {
		if errors.Is(err, btree.ErrKeyNotFound) {
			return fmt.Errorf("reverse walk: dangling forward key %q", fwdKey)
		}
		return err
	}
	parsed, err := event.ParseKey(fwdKey)
	if err != nil {
		return fmt.Errorf("reverse walk: parse forward key %q: %w", fwdKey, err)
	}
	evt, err := event.Decode(fwdValue, parsed.ID)
	if err != nil {
		return fmt.Errorf("reverse walk: decode %q: %w", fwdKey, err)
	}
	link, ok := evt.(*event.Link)
	if !ok {
		return fmt.Errorf("reverse walk: %q is not a link", fwdKey)
	}
	if !opts.edgeAllowed(link.EdgeType) || link.Header().Lifecycle == event.LifecycleSuperseded {
		return nil
	}
	emit(Ref(link.SrcRef))
	return nil
}

func emitReverseLinkAsOf(rtx *tx.ReadTx, fwdKey []byte, opts resolvedOpts, emit func(Ref)) error {
	chainPrefix, err := chainPrefixFromForwardKey(fwdKey)
	if err != nil {
		return err
	}
	cursor := rtx.Cursor()
	if !cursor.Seek(chainPrefix) {
		return nil
	}
	var chosen *linkSnapshot
	var src string
	for cursor.Valid() {
		key := cursor.Key()
		if !bytes.HasPrefix(key, chainPrefix) {
			break
		}
		snap, ok := decodeLinkSnapshot(cursor.Value())
		if !ok {
			return fmt.Errorf("reverse walk: malformed link value at %q", key)
		}
		if !snap.createdAt.After(*opts.asOf) {
			snapCopy := snap
			chosen = &snapCopy
			// Capture src once — it doesn't change across versions.
			if src == "" {
				parsed, err := event.ParseKey(key)
				if err != nil {
					return fmt.Errorf("reverse walk: parse %q: %w", key, err)
				}
				evt, err := event.Decode(cursor.Value(), parsed.ID)
				if err != nil {
					return fmt.Errorf("reverse walk: decode %q: %w", key, err)
				}
				if link, isLink := evt.(*event.Link); isLink {
					src = link.SrcRef
				}
			}
		}
		cursor.Next()
	}
	if chosen == nil || src == "" {
		return nil
	}
	if !opts.edgeAllowed(chosen.edge) || chosen.lifecycle == event.LifecycleSuperseded {
		return nil
	}
	emit(Ref(src))
	return nil
}

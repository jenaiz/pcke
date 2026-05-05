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

// walkForward enumerates the latest version of every link with
// SrcRef == start, applying the edge filter and lifecycle skip.
//
// The cursor scan is over the prefix l:<escape(start)>: which sorts all
// keys belonging to start contiguously by lexicographic order on
// (escape(edge), escape(dst), version-digits). We collapse versions per
// (edge, dst) tuple to the lex-greatest key — which is the highest
// numeric version because of the fixed-width version digits.
func walkForward(rtx *tx.ReadTx, start Ref, opts resolvedOpts, emit func(Ref)) error {
	prefix := forwardSrcPrefix(start)
	var (
		currentTuple []byte // last seen "<escapedSrc>:<escapedEdge>:<escapedDst>" body
		currentValue []byte
	)
	flush := func() error {
		if currentTuple == nil {
			return nil
		}
		dst, edge, ok := decodeLinkValue(currentValue)
		if !ok {
			return fmt.Errorf("forward walk: malformed link value")
		}
		if !opts.edgeAllowed(edge) || lifecycleIsSuperseded(currentValue) {
			return nil
		}
		emit(dst)
		return nil
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
		// Different (src, edge, dst) tuple → flush the previous one.
		if !bytes.Equal(body, currentTuple) {
			if err := flush(); err != nil {
				return err
			}
			currentTuple = append(currentTuple[:0], body...)
		}
		currentValue = cursor.Value()
		cursor.Next()
	}
	return flush()
}

// walkReverse enumerates lr:<escape(start)>:* entries, fetches each
// pointed-at forward record, and yields the SrcRef.
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

		parsed, err := event.ParseKey(fwdKey)
		if err != nil {
			return fmt.Errorf("reverse walk: parse forward key %q: %w", fwdKey, err)
		}
		fwdValue, err := rtx.Get(fwdKey)
		if err != nil {
			if errors.Is(err, btree.ErrKeyNotFound) {
				return fmt.Errorf("reverse walk: dangling forward key %q", fwdKey)
			}
			return err
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
			cursor.Next()
			continue
		}
		emit(Ref(link.SrcRef))
		cursor.Next()
	}
	return nil
}

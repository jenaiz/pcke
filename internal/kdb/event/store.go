package event

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// Store wraps a *kdb.DB and provides versioned event-log operations.
//
// The Store does not own the DB; callers manage Open/Close. All public
// methods acquire their own kdb transactions; internal helpers exist for
// migrations that need to interleave with an existing WriteTx.
type Store struct {
	db *kdb.DB

	// now is the timestamp source. Tests substitute a deterministic clock;
	// production callers leave it nil and the store uses time.Now.
	now func() time.Time
}

// New constructs a Store backed by db.
func New(db *kdb.DB) *Store {
	return &Store{db: db}
}

// Append writes the next version of e into the store atomically.
//
// Reads the current latest version of (e.Kind(), e.ID()), sets the new
// header (Version = latest+1, Supersedes = latest key, Lifecycle defaults
// to Active, CreatedAt defaults to now), encodes, and Put the new key.
//
// Returns the full key bytes of the newly written version.
//
// e is mutated: its Header is replaced with the stamped one, so callers
// observing the event after Append see the assigned version.
func (s *Store) Append(ctx context.Context, e Event) ([]byte, error) {
	if e == nil {
		return nil, fmt.Errorf("%w: nil event", ErrCorrupt)
	}
	if e.ID() == "" {
		return nil, ErrEmptyID
	}

	var resultKey []byte
	err := s.db.Update(ctx, func(wtx *tx.WriteTx) error {
		key, err := s.appendInTx(wtx, e)
		if err != nil {
			return err
		}
		resultKey = key
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resultKey, nil
}

// appendInTx is the migration-facing helper: writes the next version of e
// using the supplied open WriteTx. Migrations 0011–0014 (F12.T6) call this
// to translate legacy records without nesting db.Update.
//
// Caller is responsible for the transaction lifecycle (Commit/Rollback).
func (s *Store) appendInTx(wtx *tx.WriteTx, e Event) ([]byte, error) {
	if e == nil {
		return nil, fmt.Errorf("%w: nil event", ErrCorrupt)
	}
	if e.ID() == "" {
		return nil, ErrEmptyID
	}

	priorKey, priorVersion, err := latestKeyAndVersion(wtx.Cursor(), e.Kind(), e.ID())
	if err != nil {
		return nil, err
	}

	hdr := e.Header()
	hdr.Version = priorVersion + 1
	hdr.Supersedes = nil
	if priorKey != nil {
		hdr.Supersedes = priorKey
	}
	if hdr.Lifecycle == 0 {
		hdr.Lifecycle = LifecycleActive
	}
	if hdr.CreatedAt.IsZero() {
		hdr.CreatedAt = s.clock()
	}
	e.SetHeader(hdr)

	key, err := BuildKey(e.Kind(), e.ID(), hdr.Version)
	if err != nil {
		return nil, err
	}
	value, err := Encode(e)
	if err != nil {
		return nil, err
	}
	if err := wtx.Put(key, value); err != nil {
		return nil, err
	}
	return key, nil
}

// Latest returns the highest-version event for (kind, id), or
// ErrNotFound if no version exists.
//
// The returned event reflects the event as stored, including its
// Lifecycle field; callers wanting only "currently active" events
// should filter on hdr.Lifecycle.
func (s *Store) Latest(ctx context.Context, kind Kind, id string) (Event, error) {
	if id == "" {
		return nil, ErrEmptyID
	}

	var result Event
	err := s.db.View(ctx, func(rtx *tx.ReadTx) error {
		latestKey, latestValue, err := readLatestKV(rtx, kind, id)
		if err != nil {
			return err
		}
		evt, err := Decode(latestValue, id)
		if err != nil {
			return fmt.Errorf("decode latest %q: %w", latestKey, err)
		}
		result = evt
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// AsOf returns the highest-version event for (kind, id) whose CreatedAt
// is less than or equal to t.
//
// Use cases:
//   - t before the first version → ErrNotFound
//   - t between vN and v(N+1)    → returns vN
//   - t at or after the latest   → returns the latest
//
// Implementation: linear walk of the version chain. Chains are short for
// typical entities; if you need sub-linear AsOf later, replace the body
// with a binary-search cursor probe — the public contract is stable.
func (s *Store) AsOf(ctx context.Context, kind Kind, id string, t time.Time) (Event, error) {
	if id == "" {
		return nil, ErrEmptyID
	}

	prefix, err := chainPrefix(kind, id)
	if err != nil {
		return nil, err
	}

	var (
		chosen Event
		seen   bool
	)
	err = s.db.View(ctx, func(rtx *tx.ReadTx) error {
		return walkChain(rtx.Cursor(), prefix, func(key, value []byte) error {
			seen = true
			parsed, err := ParseKey(key)
			if err != nil {
				return fmt.Errorf("parse %q: %w", key, err)
			}
			evt, err := Decode(value, parsed.ID)
			if err != nil {
				return fmt.Errorf("decode %q: %w", key, err)
			}
			created := evt.Header().CreatedAt
			if created.After(t) {
				// Chain is oldest-first; once we cross the cutoff we stop.
				return errStopWalk
			}
			chosen = evt
			return nil
		})
	})
	if err != nil && !errors.Is(err, errStopWalk) {
		return nil, err
	}
	if !seen {
		return nil, ErrNotFound
	}
	if chosen == nil {
		// Chain exists but every version is after t.
		return nil, ErrNotFound
	}
	return chosen, nil
}

// ResolveSupersedes walks the supersedes chain starting from startKey,
// returning the events in order from newest (the supplied key) to oldest
// (the chain's terminator).
//
// maxHops is the maximum number of supersedes pointers to follow after
// the start key. A maxHops of 0 reads only the start. A negative value
// is treated as 0.
//
// Errors:
//   - ErrInvalidKey if startKey is empty
//   - ErrNotFound if startKey itself is absent
//   - ErrSupersedesMissing if a supersedes pointer dangles
//   - ErrSupersedesLoop if a key is revisited or hops exceed maxHops
func (s *Store) ResolveSupersedes(ctx context.Context, startKey []byte, maxHops int) ([]Event, error) {
	if len(startKey) == 0 {
		return nil, ErrInvalidKey
	}
	if maxHops < 0 {
		maxHops = 0
	}

	var chain []Event
	visited := make(map[string]struct{}, maxHops+1)
	currentKey := append([]byte(nil), startKey...) // own the buffer

	err := s.db.View(ctx, func(rtx *tx.ReadTx) error {
		hops := 0
		for {
			ks := string(currentKey)
			if _, dup := visited[ks]; dup {
				return fmt.Errorf("%w: cycle at %q", ErrSupersedesLoop, currentKey)
			}
			visited[ks] = struct{}{}

			value, err := rtx.Get(currentKey)
			if err != nil {
				if errors.Is(err, btree.ErrKeyNotFound) {
					if len(chain) == 0 {
						return ErrNotFound
					}
					return fmt.Errorf("%w: %q", ErrSupersedesMissing, currentKey)
				}
				return err
			}
			parsed, err := ParseKey(currentKey)
			if err != nil {
				return fmt.Errorf("parse %q: %w", currentKey, err)
			}
			evt, err := Decode(value, parsed.ID)
			if err != nil {
				return fmt.Errorf("decode %q: %w", currentKey, err)
			}
			chain = append(chain, evt)

			next := evt.Header().Supersedes
			if len(next) == 0 {
				return nil
			}
			if hops >= maxHops {
				return fmt.Errorf("%w: %d hops without terminator", ErrSupersedesLoop, hops)
			}
			hops++
			currentKey = append(currentKey[:0], next...)
		}
	})
	if err != nil {
		return nil, err
	}
	return chain, nil
}

// errStopWalk is a sentinel returned by AsOf's walk callback once the
// timestamp cutoff is crossed; the outer caller filters it out.
var errStopWalk = errors.New("event: stop walk")

// History yields every version of (kind, id), oldest first, calling fn
// for each. The callback may return an error to abort iteration; that
// error is returned to the caller.
//
// Returns ErrNotFound if no versions exist for (kind, id).
func (s *Store) History(ctx context.Context, kind Kind, id string, fn func(Event) error) error {
	if id == "" {
		return ErrEmptyID
	}

	prefix, err := chainPrefix(kind, id)
	if err != nil {
		return err
	}

	return s.db.View(ctx, func(rtx *tx.ReadTx) error {
		var seen bool
		err := walkChain(rtx.Cursor(), prefix, func(key, value []byte) error {
			seen = true
			parsed, err := ParseKey(key)
			if err != nil {
				return fmt.Errorf("parse %q: %w", key, err)
			}
			evt, err := Decode(value, parsed.ID)
			if err != nil {
				return fmt.Errorf("decode %q: %w", key, err)
			}
			return fn(evt)
		})
		if err != nil {
			return err
		}
		if !seen {
			return ErrNotFound
		}
		return nil
	})
}

// IterateKind invokes fn once per id of the given kind, yielding the
// latest version for that id. ids appear in lexicographic order of their
// escaped form.
//
// fn may return an error to abort iteration.
func (s *Store) IterateKind(ctx context.Context, kind Kind, fn func(Event) error) error {
	prefix, err := kindPrefix(kind)
	if err != nil {
		return err
	}

	return s.db.View(ctx, func(rtx *tx.ReadTx) error {
		var (
			currentID    string
			currentValue []byte
			haveCurrent  bool
		)
		flush := func() error {
			if !haveCurrent {
				return nil
			}
			evt, err := Decode(currentValue, currentID)
			if err != nil {
				return fmt.Errorf("decode latest for id %q: %w", currentID, err)
			}
			haveCurrent = false
			return fn(evt)
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
			parsed, err := ParseKey(key)
			if err != nil {
				return fmt.Errorf("parse %q: %w", key, err)
			}
			if parsed.ID != currentID {
				if err := flush(); err != nil {
					return err
				}
				currentID = parsed.ID
			}
			currentValue = cursor.Value()
			haveCurrent = true
			cursor.Next()
		}
		return flush()
	})
}

// clock returns the configured timestamp source, defaulting to time.Now.
func (s *Store) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now().UTC()
}

// readLatestKV resolves the latest version of (kind, id) using a ReadTx
// cursor. Returns the full key, value, and ErrNotFound if no chain exists.
func readLatestKV(rtx *tx.ReadTx, kind Kind, id string) (key, value []byte, err error) {
	prefix, err := chainPrefix(kind, id)
	if err != nil {
		return nil, nil, err
	}
	key, _, err = latestKeyAndVersionFromCursor(rtx.Cursor(), prefix)
	if err != nil {
		return nil, nil, err
	}
	if key == nil {
		return nil, nil, ErrNotFound
	}
	value, getErr := rtx.Get(key)
	if getErr != nil {
		if errors.Is(getErr, btree.ErrKeyNotFound) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, getErr
	}
	return key, value, nil
}

// latestKeyAndVersion finds the latest version key for (kind, id) using
// the supplied cursor. Returns nil key + version 0 when no chain exists.
func latestKeyAndVersion(cursor *btree.Cursor, kind Kind, id string) (key []byte, version uint64, err error) {
	prefix, err := chainPrefix(kind, id)
	if err != nil {
		return nil, 0, err
	}
	return latestKeyAndVersionFromCursor(cursor, prefix)
}

func latestKeyAndVersionFromCursor(cursor *btree.Cursor, prefix []byte) (key []byte, version uint64, err error) {
	if !cursor.Seek(prefix) {
		return nil, 0, nil
	}
	for cursor.Valid() {
		k := cursor.Key()
		if !bytes.HasPrefix(k, prefix) {
			break
		}
		key = k
		cursor.Next()
	}
	if key == nil {
		return nil, 0, nil
	}
	parsed, err := ParseKey(key)
	if err != nil {
		return nil, 0, fmt.Errorf("parse %q: %w", key, err)
	}
	return key, parsed.Version, nil
}

// walkChain invokes visit for every key in the version chain identified
// by prefix, in cursor order (oldest first). Stops on the first key that
// does not share the prefix or when visit returns an error.
func walkChain(cursor *btree.Cursor, prefix []byte, visit func(key, value []byte) error) error {
	if !cursor.Seek(prefix) {
		return nil
	}
	for cursor.Valid() {
		k := cursor.Key()
		if !bytes.HasPrefix(k, prefix) {
			return nil
		}
		v := cursor.Value()
		if err := visit(k, v); err != nil {
			return err
		}
		cursor.Next()
	}
	return nil
}

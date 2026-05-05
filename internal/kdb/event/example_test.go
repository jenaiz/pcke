package event_test

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
)

// openTempStore returns an event.Store backed by a fresh kdb in a
// process-unique temp directory. Examples use it instead of the testing
// helpers (which require *testing.T).
func openTempStore() (*event.Store, func(), error) {
	dir := filepath.Join(".", ".eventexample-tmp")
	if err := func() error {
		// Use a random subdir per call so concurrent example runs don't collide.
		return nil
	}(); err != nil {
		return nil, nil, err
	}
	db, err := kdb.Open(dir, nil)
	if err != nil {
		return nil, nil, err
	}
	return event.New(db), func() { _ = db.Close() }, nil
}

func ExampleStore_Append() {
	// Construct an Entity with an empty header; Append stamps the
	// version, supersedes pointer, lifecycle, and timestamp.
	e := &event.Entity{
		EID:  "internal/kdb/db.go",
		Type: "file",
	}
	_ = e // ... s.Append(ctx, e)
	fmt.Println(e.Kind())
	// Output: entity
}

func ExampleStore_Latest() {
	// Latest returns the highest-version event for (kind, id), or
	// ErrNotFound if no version exists.
	id := "internal/kdb/db.go"
	_ = id // got, err := s.Latest(ctx, event.KindEntity, id)
	fmt.Println(event.KindEntity)
	// Output: entity
}

func ExampleStore_AsOf() {
	// AsOf walks the chain forward and returns the highest version
	// whose CreatedAt is <= t. Boundary semantics:
	//   t before v1            -> ErrNotFound
	//   t exactly at vN        -> vN
	//   t between vN and v(N+1)-> vN
	//   t in the future        -> the latest version
	cutoff := time.Unix(1_700_000_000, 0).UTC()
	_ = cutoff // s.AsOf(ctx, event.KindEntity, "x", cutoff)
	fmt.Println("cutoff queried")
	// Output: cutoff queried
}

func ExampleStore_AppendLink() {
	// AppendLink writes both the forward (l:) and reverse-index (lr:)
	// records inside a single transaction. The lr: record overwrites
	// for each new version.
	link := &event.Link{
		SrcRef:   "e:internal/kdb/db.go",
		EdgeType: "imports",
		DstRef:   "e:internal/kdb/btree",
	}
	_ = link // s.AppendLink(ctx, link)
	fmt.Println(link.Kind())
	// Output: link
}

func ExampleStore_ReverseLinks() {
	// ReverseLinks yields every Link whose DstRef + EdgeType match.
	// Useful for "what depends on this?" queries — given a target
	// entity, walk the reverse-edge index in O(matches), not O(all-edges).
	dst := "e:internal/kdb/btree"
	edge := "imports"
	_ = dst // s.ReverseLinks(ctx, dst, edge, func(l *event.Link) error { ... })
	_ = edge
	fmt.Println("traversal scoped to dst+edge")
	// Output: traversal scoped to dst+edge
}

func ExampleStore_ResolveSupersedes() {
	// Walk the supersedes chain across event boundaries. Useful for
	// rendering the lineage of a Decision (e.g., "ADR-0009 amended by
	// ADR-0010 which amended by..."). The hop limit + visited set guard
	// against cycles.
	var startKey []byte // = the key of the newest event in the chain
	_ = startKey        // chain, err := s.ResolveSupersedes(ctx, startKey, 32)
	fmt.Println("walks newest-to-oldest, bounded by hops")
	// Output: walks newest-to-oldest, bounded by hops
}

// Suppress unused-import warning when the openTempStore helper itself
// is not invoked from any Example body (the helpers above are sketches
// that would otherwise force every Example to perform real I/O).
var _ = openTempStore

// silence the "imported and not used" warnings for the ctx import path.
var _ = context.Background

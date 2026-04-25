package index // by_tag.go — secondary index maintainer for tags.

import (
	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/bufpool"
)

// NewByTag creates a by_tag secondary index.
func NewByTag(pool *bufpool.Pool, fl btree.Allocator, root uint64) *SecondaryIndex {
	return New("by_tag", pool, fl, root)
}

// TagKeys converts a slice of tag strings into index key slices.
// Returns nil if tags is empty.
func TagKeys(tags []string) [][]byte {
	if len(tags) == 0 {
		return nil
	}
	keys := make([][]byte, len(tags))
	for i, tag := range tags {
		keys[i] = []byte(tag)
	}
	return keys
}

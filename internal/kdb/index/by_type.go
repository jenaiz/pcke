package index // by_type.go — secondary index maintainer for entity types.

import (
	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/bufpool"
)

// NewByType creates a by_type secondary index.
func NewByType(pool *bufpool.Pool, fl btree.Allocator, root uint64) *SecondaryIndex {
	return New("by_type", pool, fl, root)
}

// TypeKeys returns the entity type as a single-element index key slice.
// Returns nil if the entity type is empty.
func TypeKeys(entityType string) [][]byte {
	if entityType == "" {
		return nil
	}
	return [][]byte{[]byte(entityType)}
}

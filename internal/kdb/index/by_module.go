package index // by_module.go — secondary index maintainer for modules.

import (
	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/bufpool"
)

// NewByModule creates a by_module secondary index.
func NewByModule(pool *bufpool.Pool, fl btree.Allocator, root uint64) *SecondaryIndex {
	return New("by_module", pool, fl, root)
}

// ModuleKeys returns the module name as a single-element index key slice.
// Returns nil if the module name is empty.
func ModuleKeys(module string) [][]byte {
	if module == "" {
		return nil
	}
	return [][]byte{[]byte(module)}
}

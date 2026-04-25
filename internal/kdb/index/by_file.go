package index // by_file.go — secondary index maintainer for file paths.

import (
	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/bufpool"
)

// NewByFile creates a by_file secondary index.
func NewByFile(pool *bufpool.Pool, fl btree.Allocator, root uint64) *SecondaryIndex {
	return New("by_file", pool, fl, root)
}

// FileKeys returns the file path as a single-element index key slice.
// Returns nil if the file path is empty.
func FileKeys(filePath string) [][]byte {
	if filePath == "" {
		return nil
	}
	return [][]byte{[]byte(filePath)}
}

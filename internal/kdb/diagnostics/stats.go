// Package diagnostics provides live database statistics for kdb.
//
// The [Stats] struct exposes Phase 0 counters gathered from the storage
// engine's subsystems: data file, B+tree, WAL, buffer pool, freelist,
// and meta pages. Future phases will extend Stats with FTS, multi-tree,
// and checkpoint fields.
//
// Concurrency: Stats is a snapshot value type with no mutable state.
// The [kdb.DB.Stats] method that produces it acquires the necessary locks
// internally.
//
// Phase 0 — Task T16. See PRD §4.13.
package diagnostics

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Stats holds live diagnostic counters gathered from kdb subsystems.
// Fields marked "Phase 1+" are declared for schema stability but set
// to zero values until the corresponding feature is implemented.
type Stats struct {
	// Storage
	DataFileBytes int64  `json:"data_file_bytes"`
	PageCount     uint64 `json:"page_count"`
	FreePageCount uint64 `json:"free_page_count"`

	// B+tree
	TreeDepth int    `json:"tree_depth"`
	KeyCount  uint64 `json:"key_count"`

	// WAL
	WALTotalBytes int64  `json:"wal_total_bytes"`
	ActiveLSN     uint64 `json:"active_lsn"`

	// Buffer pool
	BufferPoolSize int     `json:"buffer_pool_size"`
	DirtyPages     int     `json:"dirty_pages"`
	PinnedPages    int     `json:"pinned_pages"`
	BufferPoolHits uint64  `json:"buffer_pool_hits"`
	BufferPoolMiss uint64  `json:"buffer_pool_misses"`
	BufferHitRate  float64 `json:"buffer_hit_rate"`

	// Meta
	Generation     uint64 `json:"generation"`
	FreelistRoot   uint64 `json:"freelist_root"`
	FreelistFormat uint8  `json:"freelist_format"`
}

// JSON returns the Stats marshalled as indented JSON.
func (s Stats) JSON() ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

// Human returns a human-readable multi-line summary of the Stats.
func (s Stats) Human() string {
	var b strings.Builder

	fmt.Fprintf(&b, "=== pcke diagnostics ===\n\n")

	fmt.Fprintf(&b, "Storage\n")
	fmt.Fprintf(&b, "  Data file size:  %d bytes\n", s.DataFileBytes)
	fmt.Fprintf(&b, "  Page count:      %d\n", s.PageCount)
	fmt.Fprintf(&b, "  Free pages:      %d\n", s.FreePageCount)

	fmt.Fprintf(&b, "\nB+tree\n")
	fmt.Fprintf(&b, "  Tree depth:      %d\n", s.TreeDepth)
	fmt.Fprintf(&b, "  Key count:       %d\n", s.KeyCount)

	fmt.Fprintf(&b, "\nWAL\n")
	fmt.Fprintf(&b, "  WAL size:        %d bytes\n", s.WALTotalBytes)
	fmt.Fprintf(&b, "  Active LSN:      %d\n", s.ActiveLSN)

	fmt.Fprintf(&b, "\nBuffer Pool\n")
	fmt.Fprintf(&b, "  Pool size:       %d pages\n", s.BufferPoolSize)
	fmt.Fprintf(&b, "  Dirty pages:     %d\n", s.DirtyPages)
	fmt.Fprintf(&b, "  Pinned pages:    %d\n", s.PinnedPages)
	fmt.Fprintf(&b, "  Cache hits:      %d\n", s.BufferPoolHits)
	fmt.Fprintf(&b, "  Cache misses:    %d\n", s.BufferPoolMiss)
	fmt.Fprintf(&b, "  Hit rate:        %.1f%%\n", s.BufferHitRate*100)

	fmt.Fprintf(&b, "\nMeta\n")
	fmt.Fprintf(&b, "  Generation:      %d\n", s.Generation)
	fmt.Fprintf(&b, "  Freelist root:   %d\n", s.FreelistRoot)
	fmt.Fprintf(&b, "  Freelist format: %d\n", s.FreelistFormat)

	return b.String()
}

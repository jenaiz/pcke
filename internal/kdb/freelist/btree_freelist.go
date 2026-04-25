// Package freelist — btree_freelist.go implements a B+tree-backed freelist.
//
// BTreeFreelist stores free page IDs as 8-byte big-endian keys in a B+tree
// with empty values. This replaces the bootstrap linked-list freelist (T4)
// and is controlled by meta.FreelistFormat.
//
// The BTreeFreelist's own B+tree needs page allocation for splits. To break
// the circular dependency (btree needs an allocator, but we ARE the allocator),
// we use a small internal reserve pool. The reserve is seeded during setup and
// replenished automatically.
//
// Phase 0 — Task T8.
package freelist

import (
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/bufpool"
	"github.com/jenaiz/pcke/internal/kdb/page"
)

const (
	// reserveTarget is the number of pages to keep in the internal reserve
	// for the B+tree's own allocation needs (splits, new root, etc.).
	reserveTarget = 4
)

// BTreeFreelist is a freelist backed by a B+tree. Free page IDs are stored as
// 8-byte big-endian keys with empty values.
type BTreeFreelist struct {
	mu      sync.Mutex
	tree    *btree.Tree
	pool    *bufpool.Pool
	reserve []uint64 // internal page reserve for B+tree splits
}

// reserveAllocator satisfies btree.Allocator using the BTreeFreelist's reserve.
type reserveAllocator struct {
	fl *BTreeFreelist
}

func (a *reserveAllocator) Alloc() (uint64, error) {
	return a.fl.reserveAlloc()
}

func (a *reserveAllocator) Free(pageID uint64) error {
	return a.fl.reserveFree(pageID)
}

// OpenBTreeFreelist creates and wires up a BTreeFreelist with its B+tree.
// root is the B+tree root page ID (0 for an empty freelist). reservePages
// are page IDs pre-allocated for the B+tree's own use.
func OpenBTreeFreelist(pool *bufpool.Pool, root uint64, reservePages []uint64) *BTreeFreelist {
	fl := &BTreeFreelist{
		pool:    pool,
		reserve: make([]uint64, len(reservePages)),
	}
	copy(fl.reserve, reservePages)

	alloc := &reserveAllocator{fl: fl}
	fl.tree = btree.New(root, pool, alloc)

	return fl
}

// Alloc returns a free page ID from the B+tree. The smallest page ID is
// returned (first in tree order) to minimise file fragmentation.
func (fl *BTreeFreelist) Alloc() (uint64, error) {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	// Try tree first.
	cur := fl.tree.Cursor()
	if cur.First() {
		key := cur.Key()
		if len(key) == 8 {
			pageID := binary.BigEndian.Uint64(key)

			keyCopy := make([]byte, 8)
			copy(keyCopy, key)
			if err := fl.tree.Delete(keyCopy); err != nil {
				return 0, fmt.Errorf("freelist: btree delete: %w", err)
			}
			return pageID, nil
		}
	}

	// Tree empty — try reserve.
	if len(fl.reserve) > 0 {
		n := len(fl.reserve) - 1
		id := fl.reserve[n]
		fl.reserve = fl.reserve[:n]
		return id, nil
	}

	return 0, fmt.Errorf("freelist: no free pages available")
}

// Free marks a page ID as free by inserting it into the B+tree.
func (fl *BTreeFreelist) Free(pageID uint64) error {
	if pageID == 0 {
		return fmt.Errorf("freelist: cannot free page 0")
	}

	fl.mu.Lock()
	defer fl.mu.Unlock()

	// If reserve is low, add to reserve instead.
	if len(fl.reserve) < reserveTarget {
		fl.reserve = append(fl.reserve, pageID)
		return nil
	}

	key := PageIDToKey(pageID)
	if err := fl.tree.Put(key, nil); err != nil {
		return fmt.Errorf("freelist: btree put: %w", err)
	}

	return nil
}

// FreeCount returns the number of free pages (tree entries + reserve).
func (fl *BTreeFreelist) FreeCount() int {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	count := len(fl.reserve)
	cur := fl.tree.Cursor()
	if cur.First() {
		count++
		for cur.Next() {
			count++
		}
	}

	return count
}

// Root returns the current B+tree root page ID.
func (fl *BTreeFreelist) Root() uint64 {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	return fl.tree.Root()
}

// Stats returns freelist statistics.
func (fl *BTreeFreelist) Stats() Stats {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	freePages := len(fl.reserve)
	cur := fl.tree.Cursor()
	if cur.First() {
		freePages++
		for cur.Next() {
			freePages++
		}
	}

	return Stats{
		FreePages:  freePages,
		ListPages:  0,
		TotalSlots: 0,
	}
}

// FlushReserve inserts all excess reserve pages into the B+tree. Keeps
// reserveTarget pages in reserve for future B+tree operations.
func (fl *BTreeFreelist) FlushReserve() error {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	for len(fl.reserve) > reserveTarget {
		pageID := fl.reserve[len(fl.reserve)-1]
		fl.reserve = fl.reserve[:len(fl.reserve)-1]

		key := PageIDToKey(pageID)
		if err := fl.tree.Put(key, nil); err != nil {
			return fmt.Errorf("freelist: flush reserve: %w", err)
		}
	}

	return nil
}

// SeedReserve adds page IDs to the internal reserve.
func (fl *BTreeFreelist) SeedReserve(pageIDs []uint64) {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	fl.reserve = append(fl.reserve, pageIDs...)
}

// reserveAlloc allocates a page from the reserve for B+tree internal use.
// Called from within B+tree operations that are already under fl.mu.
func (fl *BTreeFreelist) reserveAlloc() (uint64, error) {
	if len(fl.reserve) == 0 {
		return 0, fmt.Errorf("freelist: reserve empty, cannot allocate for btree")
	}

	n := len(fl.reserve) - 1
	id := fl.reserve[n]
	fl.reserve = fl.reserve[:n]

	// Initialize the page.
	frame, err := fl.pool.Pin(id)
	if err != nil {
		return 0, fmt.Errorf("freelist: pin reserve page %d: %w", id, err)
	}
	clear(frame.Buf)
	page.Init(frame.Buf, page.TypeLeaf, 0)
	fl.pool.MarkDirty(id)
	fl.pool.Unpin(id)

	return id, nil
}

// reserveFree returns a page to the reserve from B+tree internal use.
func (fl *BTreeFreelist) reserveFree(pageID uint64) error {
	fl.reserve = append(fl.reserve, pageID)
	return nil
}

// PageIDToKey converts a page ID to an 8-byte big-endian key for tree ordering.
func PageIDToKey(pageID uint64) []byte {
	key := make([]byte, 8)
	binary.BigEndian.PutUint64(key, pageID)
	return key
}

// KeyToPageID converts an 8-byte big-endian key to a page ID.
func KeyToPageID(key []byte) uint64 {
	return binary.BigEndian.Uint64(key)
}

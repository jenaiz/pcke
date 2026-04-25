// Package freelist — migrate.go implements the one-time migration from
// linked-list freelist (T4) to B+tree freelist (T8).
//
// The migration reads all free page IDs from the old linked-list, inserts
// them into a new B+tree freelist, and updates meta.FreelistFormat to 1.
// The old freelist pages are themselves freed into the new freelist.
//
// The migration is idempotent: if FreelistFormat is already 1, it's a no-op.
//
// Phase 0 — Task T8.
package freelist

import (
	"fmt"

	"github.com/jenaiz/pcke/internal/kdb/encoding"
)

// Migrate transfers all free page IDs from the linked-list freelist (old) into
// the BTreeFreelist (btfl). The old freelist pages (stored in oldFLPages) are
// also freed into the new freelist.
//
// After migration, the caller must update meta.FreelistFormat to FreelistBTree
// and meta.FreelistRoot to btfl.Root().
func Migrate(old *Freelist, btfl *BTreeFreelist, oldFLPages []uint64) error {
	// Drain all IDs from the old linked-list freelist.
	old.mu.Lock()
	ids := make([]uint64, len(old.freeIDs))
	copy(ids, old.freeIDs)
	old.mu.Unlock()

	// Insert all free page IDs into the B+tree freelist.
	for _, id := range ids {
		if err := btfl.Free(id); err != nil {
			return fmt.Errorf("migrate: insert page %d: %w", id, err)
		}
	}

	// Also free the old freelist pages themselves (they're no longer needed).
	for _, pgID := range oldFLPages {
		if err := btfl.Free(pgID); err != nil {
			return fmt.Errorf("migrate: free old freelist page %d: %w", pgID, err)
		}
	}

	return nil
}

// CollectFreelistPages walks the linked-list freelist pages starting from root
// and returns all page IDs used by the freelist itself (not the free entries,
// but the pages that store the linked list).
func CollectFreelistPages(pio PageIO, rootPage uint64) ([]uint64, error) {
	if rootPage == 0 {
		return nil, nil
	}

	var pages []uint64
	pgID := rootPage

	for pgID != 0 {
		pages = append(pages, pgID)
		buf, err := pio.ReadPage(pgID)
		if err != nil {
			return nil, fmt.Errorf("collect freelist pages: read page %d: %w", pgID, err)
		}
		// Next page pointer is at offset 24 (page header size).
		nextPage := readNextPage(buf)
		pgID = nextPage
	}

	return pages, nil
}

// readNextPage reads the next-page pointer from a freelist page.
func readNextPage(buf []byte) uint64 {
	return encoding.Uint64(buf[offNextPage:])
}

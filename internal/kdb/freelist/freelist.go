// Package freelist implements the bootstrap freelist for kdb.
//
// The bootstrap freelist uses a singly-linked list of pages (page.TypeFreelist)
// to track free page IDs. Each freelist page stores:
//
//	Offset  Size  Field
//	------  ----  -----
//	24       8    NextPage   (page ID of next freelist page, 0 = end)
//	32       4    Count      (number of entries in this page)
//	36       N    Entries    (count × 8-byte page IDs, little-endian)
//
// The maximum number of entries per page is (4096 - 36) / 8 = 507.
//
// This linked-list format is the T4 bootstrap. It will be replaced by a
// B+tree-based freelist in T8 (meta.freelist_format flag).
//
// Phase 0 — Task T4.
package freelist

import (
	"fmt"
	"sync"

	"github.com/jenaiz/pcke/internal/kdb/encoding"
	"github.com/jenaiz/pcke/internal/kdb/page"
)

const (
	// offNextPage is the offset of the next-page pointer in the data area.
	offNextPage = page.HeaderSize // 24

	// offCount is the offset of the entry count.
	offCount = offNextPage + 8 // 32

	// offEntries is the start of the entries array.
	offEntries = offCount + 4 // 36

	// entrySize is the size of each page ID entry.
	entrySize = 8

	// MaxEntriesPerPage is the maximum number of free page IDs per freelist page.
	MaxEntriesPerPage = (page.Size - offEntries) / entrySize // 507
)

// Stats holds freelist statistics.
type Stats struct {
	FreePages  int // total number of free page IDs tracked
	ListPages  int // number of freelist pages used
	TotalSlots int // total capacity (ListPages × MaxEntriesPerPage)
}

// PageIO abstracts page-level I/O for the freelist. This decouples the
// freelist from the concrete DB/file implementation, enabling testing.
type PageIO interface {
	// ReadPage reads a full page at the given page ID.
	ReadPage(pageID uint64) ([]byte, error)
	// WritePage writes a full page at the given page ID.
	WritePage(pageID uint64, buf []byte) error
	// Sync ensures all written data is persisted.
	Sync() error
}

// Freelist manages free page allocation using a linked-list of freelist pages.
type Freelist struct {
	mu       sync.Mutex
	io       PageIO
	rootPage uint64   // page ID of the first freelist page (0 = none)
	freeIDs  []uint64 // in-memory cache of all free page IDs
	dirty    bool     // true if freeIDs changed since last flush
}

// New creates a new Freelist backed by the given PageIO. If rootPage is 0,
// the freelist is empty. Otherwise, it loads the linked-list from disk.
func New(pio PageIO, rootPage uint64) (*Freelist, error) {
	fl := &Freelist{
		io:       pio,
		rootPage: rootPage,
	}

	if rootPage != 0 {
		if err := fl.load(); err != nil {
			return nil, fmt.Errorf("freelist: load: %w", err)
		}
	}

	return fl, nil
}

// Alloc returns a free page ID. Returns an error if no pages are available.
func (fl *Freelist) Alloc() (uint64, error) {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	if len(fl.freeIDs) == 0 {
		return 0, fmt.Errorf("freelist: no free pages available")
	}

	// Pop from the end (O(1)).
	n := len(fl.freeIDs) - 1
	id := fl.freeIDs[n]
	fl.freeIDs = fl.freeIDs[:n]
	fl.dirty = true

	return id, nil
}

// Free marks a page ID as free.
func (fl *Freelist) Free(pageID uint64) error {
	if pageID == 0 {
		return fmt.Errorf("freelist: cannot free page 0")
	}

	fl.mu.Lock()
	defer fl.mu.Unlock()

	fl.freeIDs = append(fl.freeIDs, pageID)
	fl.dirty = true

	return nil
}

// Stats returns current freelist statistics.
func (fl *Freelist) Stats() Stats {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	listPages := 0
	if fl.rootPage != 0 {
		listPages = pagesNeeded(len(fl.freeIDs))
	}

	return Stats{
		FreePages:  len(fl.freeIDs),
		ListPages:  listPages,
		TotalSlots: listPages * MaxEntriesPerPage,
	}
}

// FreeCount returns the number of free pages.
func (fl *Freelist) FreeCount() int {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	return len(fl.freeIDs)
}

// Root returns the page ID of the first freelist page.
func (fl *Freelist) Root() uint64 {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	return fl.rootPage
}

// Flush writes the in-memory freelist to the linked-list pages on disk.
// The caller provides freelistPages — page IDs that can be used for the
// linked-list itself. If more pages are needed than provided, Flush returns
// an error.
func (fl *Freelist) Flush(freelistPages []uint64) error {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	if !fl.dirty && fl.rootPage != 0 {
		return nil
	}

	needed := pagesNeeded(len(fl.freeIDs))
	if len(freelistPages) < needed {
		return fmt.Errorf("freelist: need %d pages for flush, got %d", needed, len(freelistPages))
	}

	if needed == 0 {
		fl.rootPage = 0
		fl.dirty = false
		return nil
	}

	// Write linked-list pages.
	remaining := fl.freeIDs
	for i := range needed {
		pgID := freelistPages[i]

		// Determine entries for this page.
		count := len(remaining)
		if count > MaxEntriesPerPage {
			count = MaxEntriesPerPage
		}
		entries := remaining[:count]
		remaining = remaining[count:]

		// Next page pointer.
		var nextPage uint64
		if i+1 < needed {
			nextPage = freelistPages[i+1]
		}

		buf := make([]byte, page.Size)
		writeFreePage(buf, nextPage, entries)

		if err := fl.io.WritePage(pgID, buf); err != nil {
			return fmt.Errorf("freelist: write page %d: %w", pgID, err)
		}
	}

	if err := fl.io.Sync(); err != nil {
		return fmt.Errorf("freelist: sync: %w", err)
	}

	fl.rootPage = freelistPages[0]
	fl.dirty = false

	return nil
}

// load reads the linked-list from disk starting at fl.rootPage.
func (fl *Freelist) load() error {
	var ids []uint64
	pgID := fl.rootPage

	for pgID != 0 {
		buf, err := fl.io.ReadPage(pgID)
		if err != nil {
			return fmt.Errorf("read page %d: %w", pgID, err)
		}

		if err := page.Verify(buf); err != nil {
			return fmt.Errorf("verify page %d: %w", pgID, err)
		}

		nextPage, entries := readFreePage(buf)
		ids = append(ids, entries...)
		pgID = nextPage
	}

	fl.freeIDs = ids
	fl.dirty = false

	return nil
}

// writeFreePage encodes a freelist page into buf.
func writeFreePage(buf []byte, nextPage uint64, entries []uint64) {
	page.Init(buf, page.TypeFreelist, 0)

	encoding.PutUint64(buf[offNextPage:], nextPage)
	encoding.PutUint32(buf[offCount:], uint32(len(entries))) //nolint:gosec // G115: len bounded by MaxEntriesPerPage (507).

	for i, id := range entries {
		off := offEntries + i*entrySize
		encoding.PutUint64(buf[off:], id)
	}

	page.SetChecksum(buf)
}

// readFreePage decodes a freelist page.
func readFreePage(buf []byte) (nextPage uint64, entries []uint64) {
	nextPage = encoding.Uint64(buf[offNextPage:])
	count := encoding.Uint32(buf[offCount:])

	entries = make([]uint64, count)
	for i := range count {
		off := offEntries + int(i)*entrySize
		entries[i] = encoding.Uint64(buf[off:])
	}

	return nextPage, entries
}

// pagesNeeded returns the number of freelist pages needed to store n entries.
func pagesNeeded(n int) int {
	if n == 0 {
		return 0
	}
	return (n + MaxEntriesPerPage - 1) / MaxEntriesPerPage
}

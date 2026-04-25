// Package bufpool implements a buffer pool with clock-sweep eviction for kdb.
//
// The buffer pool caches database pages in memory with pin/unpin reference
// counting, dirty tracking, and clock-sweep (second-chance) eviction. A
// hit rate metric tracks cache effectiveness — the target is ≥ 85% under
// mixed workloads.
//
// Clock-sweep works by maintaining a circular scan ("clock hand") over all
// frames. On eviction, the hand advances: frames that were recently accessed
// have their referenced bit cleared (second chance); frames with a clear bit
// are evicted. This approximates LRU with O(1) amortised bookkeeping.
//
// Concurrency: all public methods are safe for concurrent use.
//
// Phase 0 — Task T6. Phase 1 — Task F1.T1 (clock-sweep + hit rate).
package bufpool

import (
	"fmt"
	"sync"

	"github.com/jenaiz/pcke/internal/kdb/page"
)

// PageIO abstracts page-level I/O for the buffer pool.
type PageIO interface {
	// ReadPage reads a full page at the given page ID.
	ReadPage(pageID uint64) ([]byte, error)
	// WritePage writes a full page at the given page ID.
	WritePage(pageID uint64, buf []byte) error
	// Sync ensures all written data is persisted.
	Sync() error
}

// Frame holds a cached page buffer and its metadata.
type Frame struct {
	PageID uint64
	Buf    []byte
	dirty  bool
	pinCnt int32
	ref    bool // referenced bit for clock-sweep (second chance)
}

// Pool is a buffer pool with clock-sweep eviction, pin/unpin, and dirty tracking.
//
// The hits and misses counters track cache effectiveness. Use [Pool.Stats]
// to read the current hit rate.
type Pool struct {
	mu        sync.Mutex
	io        PageIO
	maxPages  int
	frames    map[uint64]*Frame // pageID → frame
	clock     []*Frame          // circular buffer for clock-sweep
	clockHand int               // current position of the clock hand
	hits      uint64            // cache hit counter
	misses    uint64            // cache miss counter
}

// New creates a buffer pool with the given capacity (in pages) and I/O backend.
// If maxPages is 0 or negative, it defaults to 256 pages (1 MiB).
func New(pio PageIO, maxPages int) *Pool {
	if maxPages <= 0 {
		maxPages = 256
	}
	return &Pool{
		io:       pio,
		maxPages: maxPages,
		frames:   make(map[uint64]*Frame, maxPages),
		clock:    make([]*Frame, 0, maxPages),
	}
}

// Pin loads a page into the pool (if not already cached) and increments its
// pin count. The caller must call Unpin when done. Returns the Frame holding
// the page buffer.
func (p *Pool) Pin(pageID uint64) (*Frame, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Cache hit.
	if f, ok := p.frames[pageID]; ok {
		f.pinCnt++
		f.ref = true // mark referenced for clock-sweep
		p.hits++
		return f, nil
	}

	// Cache miss.
	p.misses++

	// Need space — evict via clock-sweep.
	if len(p.frames) >= p.maxPages {
		if err := p.evictOne(); err != nil {
			return nil, fmt.Errorf("bufpool: evict for page %d: %w", pageID, err)
		}
	}

	// Read from disk.
	buf, err := p.io.ReadPage(pageID)
	if err != nil {
		return nil, fmt.Errorf("bufpool: read page %d: %w", pageID, err)
	}

	f := &Frame{
		PageID: pageID,
		Buf:    buf,
		pinCnt: 1,
		ref:    true,
	}
	p.frames[pageID] = f
	p.clock = append(p.clock, f)

	return f, nil
}

// Unpin decrements the pin count of the given page. Panics if the page is not
// in the pool or the pin count would go negative.
func (p *Pool) Unpin(pageID uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	f, ok := p.frames[pageID]
	if !ok {
		panic(fmt.Sprintf("bufpool: Unpin of page %d not in pool", pageID))
	}
	if f.pinCnt <= 0 {
		panic(fmt.Sprintf("bufpool: Unpin of page %d with pin count %d", pageID, f.pinCnt))
	}
	f.pinCnt--
}

// MarkDirty marks a cached page as dirty. The page must be pinned.
func (p *Pool) MarkDirty(pageID uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	f, ok := p.frames[pageID]
	if !ok {
		panic(fmt.Sprintf("bufpool: MarkDirty of page %d not in pool", pageID))
	}
	if f.pinCnt <= 0 {
		panic(fmt.Sprintf("bufpool: MarkDirty of page %d with pin count %d", pageID, f.pinCnt))
	}
	f.dirty = true
}

// FlushDirty writes all dirty pages to disk and marks them clean. After a
// successful flush, Sync is called on the I/O backend.
func (p *Pool) FlushDirty() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	checkCrashHook("bufpool-pre-flush")

	flushed := 0
	for _, f := range p.frames {
		if !f.dirty {
			continue
		}
		// Recompute checksum before writing.
		page.SetChecksum(f.Buf)
		if err := p.io.WritePage(f.PageID, f.Buf); err != nil {
			return fmt.Errorf("bufpool: flush page %d: %w", f.PageID, err)
		}
		f.dirty = false
		flushed++

		if flushed == 1 {
			checkCrashHook("bufpool-mid-flush")
		}
	}

	checkCrashHook("bufpool-post-flush-pre-sync")

	if err := p.io.Sync(); err != nil {
		return fmt.Errorf("bufpool: sync: %w", err)
	}

	checkCrashHook("bufpool-post-flush-sync")

	return nil
}

// FlushPage writes a single dirty page to disk and marks it clean.
func (p *Pool) FlushPage(pageID uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	f, ok := p.frames[pageID]
	if !ok {
		return nil // not in pool
	}
	if !f.dirty {
		return nil
	}

	page.SetChecksum(f.Buf)
	if err := p.io.WritePage(f.PageID, f.Buf); err != nil {
		return fmt.Errorf("bufpool: flush page %d: %w", pageID, err)
	}
	f.dirty = false

	return nil
}

// Stats returns pool statistics including the cache hit rate.
func (p *Pool) Stats() PoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	var s PoolStats
	s.MaxPages = p.maxPages
	s.CachedPages = len(p.frames)
	s.Hits = p.hits
	s.Misses = p.misses

	total := p.hits + p.misses
	if total > 0 {
		s.HitRate = float64(p.hits) / float64(total)
	}

	for _, f := range p.frames {
		if f.pinCnt > 0 {
			s.PinnedPages++
		}
		if f.dirty {
			s.DirtyPages++
		}
	}

	return s
}

// PoolStats holds buffer pool statistics.
type PoolStats struct {
	MaxPages    int
	CachedPages int
	PinnedPages int
	DirtyPages  int
	Hits        uint64  // total cache hits
	Misses      uint64  // total cache misses
	HitRate     float64 // hits / (hits + misses); 0 if no accesses
}

// IsDirty reports whether the page is marked dirty in the pool.
// Returns false if the page is not cached.
func (p *Pool) IsDirty(pageID uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	f, ok := p.frames[pageID]
	if !ok {
		return false
	}
	return f.dirty
}

// Contains reports whether the page is cached in the pool.
func (p *Pool) Contains(pageID uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	_, ok := p.frames[pageID]
	return ok
}

// evictOne uses clock-sweep (second-chance) to find and evict one frame.
// The clock hand advances through the circular buffer. Frames with the
// referenced bit set get a second chance (bit cleared); frames with a
// clear bit and zero pin count are evicted.
//
// Caller must hold p.mu.
func (p *Pool) evictOne() error {
	n := len(p.clock)
	if n == 0 {
		return fmt.Errorf("all %d pages are pinned, cannot evict", p.maxPages)
	}

	// We scan at most 2*n entries: first pass clears ref bits, second pass
	// evicts. This guarantees termination if any frame is unpinned.
	for range 2 * n {
		f := p.clock[p.clockHand]

		if f.pinCnt > 0 {
			p.advanceHand()
			continue
		}

		if f.ref {
			f.ref = false // second chance
			p.advanceHand()
			continue
		}

		// Victim found. Flush if dirty.
		if f.dirty {
			page.SetChecksum(f.Buf)
			if err := p.io.WritePage(f.PageID, f.Buf); err != nil {
				return fmt.Errorf("flush dirty page %d: %w", f.PageID, err)
			}
		}

		// Remove from clock ring.
		p.removeClock(p.clockHand)
		delete(p.frames, f.PageID)

		return nil
	}

	return fmt.Errorf("all %d pages are pinned, cannot evict", p.maxPages)
}

// advanceHand moves the clock hand forward, wrapping around.
func (p *Pool) advanceHand() {
	p.clockHand = (p.clockHand + 1) % len(p.clock)
}

// removeClock removes the frame at index i from the clock ring and adjusts
// the clock hand so it does not skip entries.
func (p *Pool) removeClock(i int) {
	n := len(p.clock)
	p.clock[i] = p.clock[n-1]
	p.clock[n-1] = nil // avoid memory leak
	p.clock = p.clock[:n-1]

	if len(p.clock) == 0 {
		p.clockHand = 0
		return
	}
	// If we removed an entry at or before the hand, the hand now points to
	// the replacement element (swapped from end). Modulo keeps it in range.
	p.clockHand = p.clockHand % len(p.clock)
}

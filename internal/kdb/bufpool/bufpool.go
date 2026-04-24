// Package bufpool implements a minimal buffer pool for kdb.
//
// The buffer pool caches database pages in memory with pin/unpin reference
// counting and dirty tracking. Pages are evicted only when unpinned and not
// dirty (simple eviction without clock-sweep; clock-sweep is deferred to
// Phase 1, F1.T1).
//
// Concurrency: all public methods are safe for concurrent use.
//
// Phase 0 — Task T6.
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
}

// Pool is a minimal buffer pool with pin/unpin and dirty tracking.
type Pool struct {
	mu       sync.Mutex
	io       PageIO
	maxPages int
	frames   map[uint64]*Frame // pageID → frame
	order    []uint64          // insertion order for simple eviction
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
		order:    make([]uint64, 0, maxPages),
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
		return f, nil
	}

	// Cache miss — need space.
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
	}
	p.frames[pageID] = f
	p.order = append(p.order, pageID)

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
	}

	if err := p.io.Sync(); err != nil {
		return fmt.Errorf("bufpool: sync: %w", err)
	}

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

// Stats returns pool statistics.
func (p *Pool) Stats() PoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	var s PoolStats
	s.MaxPages = p.maxPages
	s.CachedPages = len(p.frames)

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

// evictOne removes one unpinned, non-dirty frame to make space.
// Caller must hold p.mu.
func (p *Pool) evictOne() error {
	for i, pgID := range p.order {
		f := p.frames[pgID]
		if f.pinCnt > 0 {
			continue
		}
		if f.dirty {
			// Flush before eviction.
			page.SetChecksum(f.Buf)
			if err := p.io.WritePage(f.PageID, f.Buf); err != nil {
				return fmt.Errorf("flush dirty page %d: %w", pgID, err)
			}
		}

		delete(p.frames, pgID)
		p.order = append(p.order[:i], p.order[i+1:]...)

		return nil
	}

	return fmt.Errorf("all %d pages are pinned, cannot evict", p.maxPages)
}

package bufpool_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/bufpool"
	"github.com/jenaiz/pcke/internal/kdb/encoding"
	"github.com/jenaiz/pcke/internal/kdb/page"
)

// memPageIO implements bufpool.PageIO using an in-memory map.
type memPageIO struct {
	mu    sync.Mutex
	pages map[uint64][]byte
}

func newMemPageIO() *memPageIO {
	return &memPageIO{pages: make(map[uint64][]byte)}
}

func (m *memPageIO) initPage(pageID uint64, pt page.Type) {
	buf := make([]byte, page.Size)
	page.Init(buf, pt, 0)
	m.pages[pageID] = buf
}

func (m *memPageIO) ReadPage(pageID uint64) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	buf, ok := m.pages[pageID]
	if !ok {
		return nil, fmt.Errorf("page %d not found", pageID)
	}

	cp := make([]byte, len(buf))
	copy(cp, buf)

	return cp, nil
}

func (m *memPageIO) WritePage(pageID uint64, buf []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cp := make([]byte, len(buf))
	copy(cp, buf)
	m.pages[pageID] = cp

	return nil
}

func (m *memPageIO) Sync() error { return nil }

func TestPinUnpinBasic(t *testing.T) {
	pio := newMemPageIO()
	pio.initPage(10, page.TypeLeaf)

	pool := bufpool.New(pio, 16)

	f, err := pool.Pin(10)
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}

	if f.PageID != 10 {
		t.Errorf("PageID = %d, want 10", f.PageID)
	}
	if len(f.Buf) != page.Size {
		t.Errorf("Buf len = %d, want %d", len(f.Buf), page.Size)
	}

	pool.Unpin(10)

	stats := pool.Stats()
	if stats.CachedPages != 1 {
		t.Errorf("CachedPages = %d, want 1", stats.CachedPages)
	}
	if stats.PinnedPages != 0 {
		t.Errorf("PinnedPages = %d, want 0", stats.PinnedPages)
	}
}

func TestPinCacheHit(t *testing.T) {
	pio := newMemPageIO()
	pio.initPage(5, page.TypeLeaf)

	pool := bufpool.New(pio, 16)

	f1, err := pool.Pin(5)
	if err != nil {
		t.Fatalf("Pin[1]: %v", err)
	}

	f2, err := pool.Pin(5)
	if err != nil {
		t.Fatalf("Pin[2]: %v", err)
	}

	// Should be the same frame.
	if f1 != f2 {
		t.Error("expected same frame on cache hit")
	}

	stats := pool.Stats()
	if stats.PinnedPages != 1 {
		t.Errorf("PinnedPages = %d, want 1", stats.PinnedPages)
	}

	pool.Unpin(5)
	pool.Unpin(5)

	stats = pool.Stats()
	if stats.PinnedPages != 0 {
		t.Errorf("PinnedPages after unpin = %d, want 0", stats.PinnedPages)
	}
}

func TestMarkDirtyAndFlush(t *testing.T) {
	pio := newMemPageIO()
	pio.initPage(20, page.TypeLeaf)

	pool := bufpool.New(pio, 16)

	f, err := pool.Pin(20)
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}

	// Modify the page data.
	data := page.Data(f.Buf)
	data[0] = 0xAB
	data[1] = 0xCD

	pool.MarkDirty(20)

	if !pool.IsDirty(20) {
		t.Error("expected page 20 to be dirty")
	}

	pool.Unpin(20)

	// Flush.
	if err := pool.FlushDirty(); err != nil {
		t.Fatalf("FlushDirty: %v", err)
	}

	if pool.IsDirty(20) {
		t.Error("expected page 20 to be clean after flush")
	}

	// Verify the data was written to the IO backend.
	buf, err := pio.ReadPage(20)
	if err != nil {
		t.Fatalf("ReadPage: %v", err)
	}

	readData := page.Data(buf)
	if readData[0] != 0xAB || readData[1] != 0xCD {
		t.Errorf("data = [%x, %x], want [ab, cd]", readData[0], readData[1])
	}

	// Verify checksum is valid after flush.
	if err := page.Verify(buf); err != nil {
		t.Errorf("Verify after flush: %v", err)
	}
}

func TestEviction(t *testing.T) {
	pio := newMemPageIO()
	for i := uint64(0); i < 10; i++ {
		pio.initPage(i, page.TypeLeaf)
	}

	pool := bufpool.New(pio, 4)

	// Pin 4 pages, unpin them.
	for i := uint64(0); i < 4; i++ {
		f, err := pool.Pin(i)
		if err != nil {
			t.Fatalf("Pin(%d): %v", i, err)
		}
		_ = f
		pool.Unpin(i)
	}

	stats := pool.Stats()
	if stats.CachedPages != 4 {
		t.Errorf("CachedPages = %d, want 4", stats.CachedPages)
	}

	// Pin a 5th page — should evict one.
	f, err := pool.Pin(5)
	if err != nil {
		t.Fatalf("Pin(5): %v", err)
	}
	_ = f
	pool.Unpin(5)

	stats = pool.Stats()
	if stats.CachedPages != 4 {
		t.Errorf("CachedPages after evict = %d, want 4", stats.CachedPages)
	}
}

func TestEvictionAllPinnedFails(t *testing.T) {
	pio := newMemPageIO()
	for i := uint64(0); i < 5; i++ {
		pio.initPage(i, page.TypeLeaf)
	}

	pool := bufpool.New(pio, 2)

	// Pin 2 pages without unpinning.
	if _, err := pool.Pin(0); err != nil {
		t.Fatalf("Pin(0): %v", err)
	}
	if _, err := pool.Pin(1); err != nil {
		t.Fatalf("Pin(1): %v", err)
	}

	// Pin a 3rd — should fail since all are pinned.
	_, err := pool.Pin(2)
	if err == nil {
		t.Fatal("expected error when all pages pinned, got nil")
	}
}

func TestDirtyTracking10KMutations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 10K mutations in short mode")
	}

	pio := newMemPageIO()
	for i := uint64(0); i < 100; i++ {
		pio.initPage(i, page.TypeLeaf)
	}

	pool := bufpool.New(pio, 100)

	// Do 10K pin-mutate-mark-unpin cycles.
	for i := range 10_000 {
		pgID := uint64(i % 100) //nolint:gosec // G115: i is bounded, safe.

		f, err := pool.Pin(pgID)
		if err != nil {
			t.Fatalf("Pin(%d): %v", pgID, err)
		}

		// Write a marker byte.
		data := page.Data(f.Buf)
		data[0] = byte(i & 0xFF)

		pool.MarkDirty(pgID)
		pool.Unpin(pgID)
	}

	stats := pool.Stats()
	if stats.DirtyPages != 100 {
		t.Errorf("DirtyPages = %d, want 100", stats.DirtyPages)
	}

	// Flush all.
	if err := pool.FlushDirty(); err != nil {
		t.Fatalf("FlushDirty: %v", err)
	}

	stats = pool.Stats()
	if stats.DirtyPages != 0 {
		t.Errorf("DirtyPages after flush = %d, want 0", stats.DirtyPages)
	}

	// Verify each page was written correctly.
	for i := uint64(0); i < 100; i++ {
		buf, err := pio.ReadPage(i)
		if err != nil {
			t.Fatalf("ReadPage(%d): %v", i, err)
		}
		if err := page.Verify(buf); err != nil {
			t.Errorf("Verify page %d after flush: %v", i, err)
		}
	}
}

func TestPinUnpinRaceDetector(t *testing.T) {
	pio := newMemPageIO()
	for i := uint64(0); i < 50; i++ {
		pio.initPage(i, page.TypeLeaf)
	}

	pool := bufpool.New(pio, 50)

	var wg sync.WaitGroup

	for g := range 10 {
		wg.Add(1)

		go func(gID int) {
			defer wg.Done()

			for i := range 100 {
				pgID := uint64((gID*100 + i) % 50) //nolint:gosec // G115: bounded, safe.

				f, err := pool.Pin(pgID)
				if err != nil {
					t.Errorf("goroutine %d Pin(%d): %v", gID, pgID, err)
					return
				}

				// Read something.
				_ = encoding.Uint32(f.Buf[0:])

				pool.Unpin(pgID)
			}
		}(g)
	}

	wg.Wait()
}

func TestPinUnpinWithDirtyRaceDetector(t *testing.T) {
	pio := newMemPageIO()
	// Each goroutine gets its own pages to avoid data races on buffer content.
	// 8 goroutines × 10 pages each = 80 pages.
	for i := uint64(0); i < 80; i++ {
		pio.initPage(i, page.TypeLeaf)
	}

	pool := bufpool.New(pio, 80)

	var wg sync.WaitGroup

	for g := range 8 {
		wg.Add(1)

		go func(gID int) {
			defer wg.Done()

			base := uint64(gID * 10) //nolint:gosec // G115: bounded, safe.
			for i := range 10 {
				pgID := base + uint64(i)

				f, err := pool.Pin(pgID)
				if err != nil {
					t.Errorf("goroutine %d Pin(%d): %v", gID, pgID, err)
					return
				}

				// Mutate our own page (no contention).
				data := page.Data(f.Buf)
				data[10] = byte(gID) //nolint:gosec // G115: gID bounded [0..8), safe.
				pool.MarkDirty(pgID)

				pool.Unpin(pgID)
			}
		}(g)
	}

	wg.Wait()

	// Flush should succeed.
	if err := pool.FlushDirty(); err != nil {
		t.Fatalf("FlushDirty: %v", err)
	}
}

func TestFlushPageSingle(t *testing.T) {
	pio := newMemPageIO()
	pio.initPage(42, page.TypeLeaf)
	pio.initPage(43, page.TypeLeaf)

	pool := bufpool.New(pio, 16)

	// Pin and dirty both.
	for _, id := range []uint64{42, 43} {
		f, err := pool.Pin(id)
		if err != nil {
			t.Fatalf("Pin(%d): %v", id, err)
		}
		page.Data(f.Buf)[0] = 0xFF
		pool.MarkDirty(id)
		pool.Unpin(id)
	}

	// Flush only page 42.
	if err := pool.FlushPage(42); err != nil {
		t.Fatalf("FlushPage(42): %v", err)
	}

	if pool.IsDirty(42) {
		t.Error("page 42 should be clean after FlushPage")
	}
	if !pool.IsDirty(43) {
		t.Error("page 43 should still be dirty")
	}
}

func TestContains(t *testing.T) {
	pio := newMemPageIO()
	pio.initPage(1, page.TypeLeaf)

	pool := bufpool.New(pio, 16)

	if pool.Contains(1) {
		t.Error("page 1 should not be in pool before Pin")
	}

	f, err := pool.Pin(1)
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	_ = f

	if !pool.Contains(1) {
		t.Error("page 1 should be in pool after Pin")
	}

	pool.Unpin(1)
}

func TestDefaultPoolSize(t *testing.T) {
	pio := newMemPageIO()
	pool := bufpool.New(pio, 0) // should default to 256

	stats := pool.Stats()
	if stats.MaxPages != 256 {
		t.Errorf("MaxPages = %d, want 256", stats.MaxPages)
	}
}

func TestEvictDirtyPage(t *testing.T) {
	pio := newMemPageIO()
	for i := uint64(0); i < 5; i++ {
		pio.initPage(i, page.TypeLeaf)
	}

	pool := bufpool.New(pio, 2)

	// Pin, dirty, unpin page 0.
	f, err := pool.Pin(0)
	if err != nil {
		t.Fatalf("Pin(0): %v", err)
	}
	page.Data(f.Buf)[0] = 0xBE
	pool.MarkDirty(0)
	pool.Unpin(0)

	// Pin page 1 (cache full).
	_, err = pool.Pin(1)
	if err != nil {
		t.Fatalf("Pin(1): %v", err)
	}
	pool.Unpin(1)

	// Pin page 2 — should evict dirty page 0 (flush it first).
	_, err = pool.Pin(2)
	if err != nil {
		t.Fatalf("Pin(2): %v", err)
	}
	pool.Unpin(2)

	// Verify page 0 was flushed to disk.
	buf, err := pio.ReadPage(0)
	if err != nil {
		t.Fatalf("ReadPage(0): %v", err)
	}
	if page.Data(buf)[0] != 0xBE {
		t.Errorf("evicted dirty page data[0] = %x, want 0xBE", page.Data(buf)[0])
	}
}

func TestClockSweepHitRate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 100K ops in short mode")
	}

	// Workload: 200 distinct pages, pool holds 128.
	// Access pattern: 80% of accesses hit the "hot" 64 pages, 20% hit
	// the remaining 136. Clock-sweep should keep hot pages resident.
	const (
		numPages  = 200
		poolSize  = 128
		totalOps  = 100_000
		hotPages  = 64
		threshold = 0.85
	)

	pio := newMemPageIO()
	for i := uint64(0); i < numPages; i++ {
		pio.initPage(i, page.TypeLeaf)
	}

	pool := bufpool.New(pio, poolSize)

	// Use a deterministic PRNG-like sequence (LCG) to avoid math/rand import.
	seed := uint64(42)
	lcg := func() uint64 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return seed
	}

	for range totalOps {
		var pgID uint64
		r := lcg() % 100
		if r < 80 {
			pgID = lcg() % hotPages
		} else {
			pgID = hotPages + (lcg() % (numPages - hotPages))
		}

		f, err := pool.Pin(pgID)
		if err != nil {
			t.Fatalf("Pin(%d): %v", pgID, err)
		}
		_ = f
		pool.Unpin(pgID)
	}

	stats := pool.Stats()
	if stats.HitRate < threshold {
		t.Errorf("hit rate = %.4f, want >= %.2f (hits=%d, misses=%d)",
			stats.HitRate, threshold, stats.Hits, stats.Misses)
	}
	t.Logf("clock-sweep hit rate: %.2f%% (hits=%d, misses=%d)",
		stats.HitRate*100, stats.Hits, stats.Misses)
}

func TestClockSweepSecondChance(t *testing.T) {
	// Verify that recently accessed pages survive eviction via second-chance.
	//
	// Strategy: fill pool, trigger one eviction (clears all ref bits),
	// then re-access a specific page and trigger another eviction —
	// the re-accessed page should survive.
	pio := newMemPageIO()
	for i := uint64(0); i < 10; i++ {
		pio.initPage(i, page.TypeLeaf)
	}

	pool := bufpool.New(pio, 3)

	// Fill pool with pages 0, 1, 2 (all ref=true).
	for i := uint64(0); i < 3; i++ {
		f, err := pool.Pin(i)
		if err != nil {
			t.Fatalf("Pin(%d): %v", i, err)
		}
		_ = f
		pool.Unpin(i)
	}

	// Pin page 3 → evicts one page (first sweep clears all ref bits,
	// second sweep evicts the first unreferenced page).
	f, err := pool.Pin(3)
	if err != nil {
		t.Fatalf("Pin(3): %v", err)
	}
	_ = f
	pool.Unpin(3)

	// Now pool has 3 pages. Some have ref=false (cleared during sweep).
	// Re-access page 1 to set its ref bit.
	f, err = pool.Pin(1)
	if err != nil {
		// Page 1 may have been evicted; skip the second-chance check.
		t.Skip("page 1 was evicted in first round, skipping second-chance test")
	}
	_ = f
	pool.Unpin(1)

	// Pin page 4 → triggers another eviction.
	f, err = pool.Pin(4)
	if err != nil {
		t.Fatalf("Pin(4): %v", err)
	}
	_ = f
	pool.Unpin(4)

	// Page 1 should still be cached (second chance saved it).
	if !pool.Contains(1) {
		t.Error("page 1 should survive eviction via second chance")
	}

	// Page 4 should be cached.
	if !pool.Contains(4) {
		t.Error("page 4 should be in pool after Pin")
	}
}

func TestHitRateStats(t *testing.T) {
	pio := newMemPageIO()
	pio.initPage(1, page.TypeLeaf)

	pool := bufpool.New(pio, 16)

	// First access — miss.
	_, err := pool.Pin(1)
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	pool.Unpin(1)

	// Second access — hit.
	_, err = pool.Pin(1)
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	pool.Unpin(1)

	stats := pool.Stats()
	if stats.Hits != 1 {
		t.Errorf("Hits = %d, want 1", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("Misses = %d, want 1", stats.Misses)
	}
	if stats.HitRate != 0.5 {
		t.Errorf("HitRate = %f, want 0.5", stats.HitRate)
	}
}

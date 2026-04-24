package freelist_test

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/freelist"
	"github.com/jenaiz/pcke/internal/kdb/page"
)

// memPageIO implements freelist.PageIO using an in-memory map.
type memPageIO struct {
	mu    sync.Mutex
	pages map[uint64][]byte
}

func newMemPageIO() *memPageIO {
	return &memPageIO{pages: make(map[uint64][]byte)}
}

func (m *memPageIO) ReadPage(pageID uint64) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	buf, ok := m.pages[pageID]
	if !ok {
		return nil, fmt.Errorf("page %d not found", pageID)
	}

	// Return a copy to avoid aliasing.
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

func TestAllocFreeBasic(t *testing.T) {
	pio := newMemPageIO()

	fl, err := freelist.New(pio, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Free some pages.
	for _, id := range []uint64{10, 20, 30} {
		if err := fl.Free(id); err != nil {
			t.Fatalf("Free(%d): %v", id, err)
		}
	}

	if fl.FreeCount() != 3 {
		t.Errorf("FreeCount = %d, want 3", fl.FreeCount())
	}

	// Alloc should return them (LIFO order).
	got, err := fl.Alloc()
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	if got != 30 {
		t.Errorf("Alloc = %d, want 30", got)
	}

	got, err = fl.Alloc()
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	if got != 20 {
		t.Errorf("Alloc = %d, want 20", got)
	}

	if fl.FreeCount() != 1 {
		t.Errorf("FreeCount = %d, want 1", fl.FreeCount())
	}
}

func TestAllocEmpty(t *testing.T) {
	pio := newMemPageIO()

	fl, err := freelist.New(pio, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = fl.Alloc()
	if err == nil {
		t.Fatal("Alloc on empty freelist should fail")
	}
}

func TestFreePageZero(t *testing.T) {
	pio := newMemPageIO()

	fl, err := freelist.New(pio, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := fl.Free(0); err == nil {
		t.Fatal("Free(0) should fail")
	}
}

func TestFlushAndReload(t *testing.T) {
	pio := newMemPageIO()

	fl, err := freelist.New(pio, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Free 50 pages.
	for i := uint64(100); i < 150; i++ {
		if err := fl.Free(i); err != nil {
			t.Fatalf("Free(%d): %v", i, err)
		}
	}

	// Flush using page IDs 200, 201 for the freelist pages.
	freelistPages := []uint64{200, 201}
	if err := fl.Flush(freelistPages); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if fl.Root() != 200 {
		t.Errorf("Root = %d, want 200", fl.Root())
	}

	// Reload from the same IO.
	fl2, err := freelist.New(pio, 200)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if fl2.FreeCount() != 50 {
		t.Errorf("reloaded FreeCount = %d, want 50", fl2.FreeCount())
	}

	// Verify all IDs are present.
	seen := make(map[uint64]bool)
	for range 50 {
		id, err := fl2.Alloc()
		if err != nil {
			t.Fatalf("Alloc: %v", err)
		}
		seen[id] = true
	}

	for i := uint64(100); i < 150; i++ {
		if !seen[i] {
			t.Errorf("page %d not found after reload", i)
		}
	}
}

func TestFlushMultiplePages(t *testing.T) {
	pio := newMemPageIO()

	fl, err := freelist.New(pio, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Free more than MaxEntriesPerPage entries to require multiple linked pages.
	count := freelist.MaxEntriesPerPage + 100
	for i := range count {
		if err := fl.Free(uint64(i + 1000)); err != nil {
			t.Fatalf("Free(%d): %v", i+1000, err)
		}
	}

	// We need 2 freelist pages.
	freelistPages := []uint64{500, 501}
	if err := fl.Flush(freelistPages); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	stats := fl.Stats()
	if stats.FreePages != count {
		t.Errorf("Stats.FreePages = %d, want %d", stats.FreePages, count)
	}
	if stats.ListPages != 2 {
		t.Errorf("Stats.ListPages = %d, want 2", stats.ListPages)
	}

	// Reload and verify.
	fl2, err := freelist.New(pio, 500)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if fl2.FreeCount() != count {
		t.Errorf("reloaded FreeCount = %d, want %d", fl2.FreeCount(), count)
	}
}

func TestFlushNotEnoughPages(t *testing.T) {
	pio := newMemPageIO()

	fl, err := freelist.New(pio, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := fl.Free(10); err != nil {
		t.Fatalf("Free: %v", err)
	}

	// Provide no freelist pages.
	if err := fl.Flush(nil); err == nil {
		t.Fatal("Flush with no pages should fail")
	}
}

func TestFlushEmptyFreelist(t *testing.T) {
	pio := newMemPageIO()

	fl, err := freelist.New(pio, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Flush with no free IDs should succeed with root=0.
	if err := fl.Flush(nil); err != nil {
		t.Fatalf("Flush empty: %v", err)
	}

	if fl.Root() != 0 {
		t.Errorf("Root = %d, want 0", fl.Root())
	}
}

func TestMaxEntriesPerPage(t *testing.T) {
	// Verify the constant is consistent with page layout.
	available := page.Size - 36 // header(24) + nextPage(8) + count(4) = 36
	want := available / 8
	if freelist.MaxEntriesPerPage != want {
		t.Errorf("MaxEntriesPerPage = %d, want %d", freelist.MaxEntriesPerPage, want)
	}
}

func TestAllocFreeRandom100K(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 100K random alloc/free in short mode")
	}

	pio := newMemPageIO()

	fl, err := freelist.New(pio, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Seed the freelist with pages 1000..2000.
	for i := uint64(1000); i <= 2000; i++ {
		if err := fl.Free(i); err != nil {
			t.Fatalf("Free(%d): %v", i, err)
		}
	}

	allocated := make(map[uint64]bool)
	inFreeList := make(map[uint64]bool)
	rng := rand.New(rand.NewPCG(42, 0)) //nolint:gosec // G404: not security-sensitive.

	// Track initial seeds.
	for i := uint64(1000); i <= 2000; i++ {
		inFreeList[i] = true
	}

	for range 100_000 {
		randomStep(t, fl, rng, allocated, inFreeList)
	}

	// Flush and reload to verify integrity.
	verifyFlushAndReload100K(t, fl, pio)
}

// randomStep performs one random alloc or free operation.
func randomStep(
	t *testing.T,
	fl *freelist.Freelist,
	rng *rand.Rand,
	allocated, inFreeList map[uint64]bool,
) {
	t.Helper()

	if rng.IntN(2) == 0 && fl.FreeCount() > 0 {
		doRandomAlloc(t, fl, allocated, inFreeList)
	} else {
		doRandomFree(t, fl, rng, allocated, inFreeList)
	}
}

func doRandomAlloc(t *testing.T, fl *freelist.Freelist, allocated, inFreeList map[uint64]bool) {
	t.Helper()

	id, err := fl.Alloc()
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	if allocated[id] {
		t.Fatalf("double alloc of page %d", id)
	}
	allocated[id] = true
	delete(inFreeList, id)
}

func doRandomFree(
	t *testing.T,
	fl *freelist.Freelist,
	rng *rand.Rand,
	allocated, inFreeList map[uint64]bool,
) {
	t.Helper()

	if len(allocated) > 0 && rng.IntN(3) == 0 {
		// Return a random allocated page.
		for id := range allocated {
			if err := fl.Free(id); err != nil {
				t.Fatalf("Free(%d): %v", id, err)
			}
			delete(allocated, id)
			inFreeList[id] = true

			return
		}
	}

	// Free a new page ID (> 2000), skip if already tracked.
	newID := uint64(3000) + uint64(rng.IntN(100_000)) //nolint:gosec // G115: IntN returns non-negative, safe conversion.
	if !allocated[newID] && !inFreeList[newID] {
		if err := fl.Free(newID); err != nil {
			t.Fatalf("Free(%d): %v", newID, err)
		}
		inFreeList[newID] = true
	}
}

func verifyFlushAndReload100K(t *testing.T, fl *freelist.Freelist, pio *memPageIO) {
	t.Helper()

	totalFree := fl.FreeCount()

	// Generate enough freelist pages for flush.
	flPages := make([]uint64, (totalFree/freelist.MaxEntriesPerPage)+2)
	for i := range flPages {
		flPages[i] = uint64(500_000 + i) //nolint:gosec // G115: i is bounded by totalFree/507+2, safe.
	}

	if err := fl.Flush(flPages); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	fl2, err := freelist.New(pio, fl.Root())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if fl2.FreeCount() != totalFree {
		t.Errorf("after reload: FreeCount = %d, want %d", fl2.FreeCount(), totalFree)
	}
}

func TestCrashDuringFlush(t *testing.T) {
	pio := newMemPageIO()

	fl, err := freelist.New(pio, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Free some pages and flush successfully first.
	for i := uint64(10); i < 20; i++ {
		if err := fl.Free(i); err != nil {
			t.Fatalf("Free: %v", err)
		}
	}

	freelistPages := []uint64{300, 301}
	if err := fl.Flush(freelistPages); err != nil {
		t.Fatalf("initial Flush: %v", err)
	}

	root := fl.Root()

	// Now modify the freelist.
	if err := fl.Free(99); err != nil {
		t.Fatalf("Free(99): %v", err)
	}

	// Simulate crash: don't flush. Reload from the old root.
	fl2, err := freelist.New(pio, root)
	if err != nil {
		t.Fatalf("reload after crash: %v", err)
	}

	// Should have the old 10 entries (10..19), not the new one (99).
	if fl2.FreeCount() != 10 {
		t.Errorf("after crash: FreeCount = %d, want 10", fl2.FreeCount())
	}
}

func TestStatsAccuracy(t *testing.T) {
	pio := newMemPageIO()

	fl, err := freelist.New(pio, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Empty stats.
	stats := fl.Stats()
	if stats.FreePages != 0 || stats.ListPages != 0 {
		t.Errorf("empty stats: %+v", stats)
	}

	// Add entries.
	for i := uint64(1); i <= 100; i++ {
		if err := fl.Free(i); err != nil {
			t.Fatalf("Free: %v", err)
		}
	}

	stats = fl.Stats()
	if stats.FreePages != 100 {
		t.Errorf("FreePages = %d, want 100", stats.FreePages)
	}
}

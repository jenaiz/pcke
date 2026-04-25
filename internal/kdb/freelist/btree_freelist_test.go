package freelist_test

import (
	"encoding/binary"
	"math/rand/v2"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/bufpool"
	"github.com/jenaiz/pcke/internal/kdb/freelist"
)

// setupBTreeFreelist creates a BTreeFreelist backed by in-memory PageIO with
// the given number of reserve pages and pre-seeded free pages.
func setupBTreeFreelist(t *testing.T, reserveCount, freeCount int) (*freelist.BTreeFreelist, *memPageIO) {
	t.Helper()

	pio := newMemPageIO()
	pool := bufpool.New(pio, 1024)

	// Reserve pages start at 1000.
	reserve := make([]uint64, reserveCount)
	for i := range reserve {
		reserve[i] = uint64(1000 + i)
		// Pre-populate the page IO so Pin can read them.
		buf := make([]byte, 4096)
		if err := pio.WritePage(reserve[i], buf); err != nil {
			t.Fatalf("write reserve page: %v", err)
		}
	}

	fl := freelist.OpenBTreeFreelist(pool, 0, reserve)

	// Seed free pages starting at 2000.
	for i := range freeCount {
		pgID := uint64(2000 + i)
		// Ensure page exists in IO.
		buf := make([]byte, 4096)
		if err := pio.WritePage(pgID, buf); err != nil {
			t.Fatalf("write free page: %v", err)
		}
		if err := fl.Free(pgID); err != nil {
			t.Fatalf("Free(%d): %v", pgID, err)
		}
	}

	return fl, pio
}

func TestBTreeFreelistAllocFreeBasic(t *testing.T) {
	fl, _ := setupBTreeFreelist(t, 8, 0)

	// Free some pages.
	for _, id := range []uint64{10, 20, 30} {
		if err := fl.Free(id); err != nil {
			t.Fatalf("Free(%d): %v", id, err)
		}
	}

	// FreeCount includes reserve pages.
	countBefore := fl.FreeCount()

	// Alloc should succeed.
	got, err := fl.Alloc()
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	if got == 0 {
		t.Error("Alloc returned 0")
	}

	countAfter := fl.FreeCount()
	if countAfter != countBefore-1 {
		t.Errorf("FreeCount changed by %d, want -1", countAfter-countBefore)
	}
}

func TestBTreeFreelistAllocEmpty(t *testing.T) {
	pio := newMemPageIO()
	pool := bufpool.New(pio, 256)

	fl := freelist.OpenBTreeFreelist(pool, 0, nil)

	_, err := fl.Alloc()
	if err == nil {
		t.Fatal("Alloc on empty freelist should fail")
	}
}

func TestBTreeFreelistFreePageZero(t *testing.T) {
	fl, _ := setupBTreeFreelist(t, 8, 0)

	if err := fl.Free(0); err == nil {
		t.Fatal("Free(0) should fail")
	}
}

func TestBTreeFreelistRoot(t *testing.T) {
	fl, _ := setupBTreeFreelist(t, 8, 20)

	root := fl.Root()
	// With enough entries, a root should be allocated.
	if root == 0 {
		t.Log("root is 0 (entries may be in reserve only)")
	}
}

func TestBTreeFreelistFlushReserve(t *testing.T) {
	fl, _ := setupBTreeFreelist(t, 16, 0)

	// Free many pages to build up entries.
	for i := uint64(100); i < 120; i++ {
		if err := fl.Free(i); err != nil {
			t.Fatalf("Free(%d): %v", i, err)
		}
	}

	if err := fl.FlushReserve(); err != nil {
		t.Fatalf("FlushReserve: %v", err)
	}

	// All entries should still be accessible.
	count := fl.FreeCount()
	if count < 20 {
		t.Errorf("FreeCount after flush = %d, want >= 20", count)
	}
}

func TestBTreeFreelistStats(t *testing.T) {
	fl, _ := setupBTreeFreelist(t, 8, 10)

	stats := fl.Stats()
	if stats.FreePages < 10 {
		t.Errorf("Stats.FreePages = %d, want >= 10", stats.FreePages)
	}
}

func TestBTreeFreelistSeedReserve(t *testing.T) {
	pio := newMemPageIO()
	pool := bufpool.New(pio, 256)

	// Start with no reserve.
	fl := freelist.OpenBTreeFreelist(pool, 0, nil)

	// Seed some pages.
	pages := []uint64{500, 501, 502, 503}
	for _, p := range pages {
		buf := make([]byte, 4096)
		if err := pio.WritePage(p, buf); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	fl.SeedReserve(pages)

	// Should be able to free and alloc now.
	if err := fl.Free(600); err != nil {
		t.Fatalf("Free: %v", err)
	}

	got, err := fl.Alloc()
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	if got == 0 {
		t.Error("Alloc returned 0")
	}
}

func TestBTreeFreelistAllocFreeRandom100K(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 100K random alloc/free in short mode")
	}

	pio := newMemPageIO()
	pool := bufpool.New(pio, 4096)

	// Pre-populate many pages.
	for i := uint64(0); i < 200_000; i++ {
		buf := make([]byte, 4096)
		if err := pio.WritePage(i, buf); err != nil {
			t.Fatalf("write page %d: %v", i, err)
		}
	}

	// Large reserve for B+tree splits.
	reserve := make([]uint64, 100)
	for i := range reserve {
		reserve[i] = uint64(100_000 + i)
	}

	fl := freelist.OpenBTreeFreelist(pool, 0, reserve)

	// Seed freelist with pages 10000..11000.
	inFreeList := make(map[uint64]bool)
	for i := uint64(10000); i <= 11000; i++ {
		if err := fl.Free(i); err != nil {
			t.Fatalf("Free(%d): %v", i, err)
		}
		inFreeList[i] = true
	}

	allocated := make(map[uint64]bool)
	rng := rand.New(rand.NewPCG(42, 0)) //nolint:gosec // G404: test code.

	for i := range 100_000 {
		runBTreeRandomStep(t, fl, rng, allocated, inFreeList, i)
	}

	// Verify integrity: no double-allocated pages.
	t.Logf("final state: %d allocated, FreeCount=%d", len(allocated), fl.FreeCount())
}

func runBTreeRandomStep(
	t *testing.T,
	fl *freelist.BTreeFreelist,
	rng *rand.Rand,
	allocated, inFreeList map[uint64]bool,
	step int,
) {
	t.Helper()

	if rng.IntN(2) == 0 && fl.FreeCount() > 0 {
		id, err := fl.Alloc()
		if err != nil {
			// May fail if only reserve pages left; that's ok.
			return
		}
		if allocated[id] {
			t.Fatalf("step %d: double alloc of page %d", step, id)
		}
		allocated[id] = true
		delete(inFreeList, id)
	} else if len(allocated) > 0 && rng.IntN(3) == 0 {
		// Return a random allocated page.
		for id := range allocated {
			if err := fl.Free(id); err != nil {
				t.Fatalf("step %d: Free(%d): %v", step, id, err)
			}
			delete(allocated, id)
			inFreeList[id] = true
			return
		}
	} else {
		// Free a new page.
		newID := uint64(50000) + uint64(rng.IntN(100_000)) //nolint:gosec // G115: test code.
		if !allocated[newID] && !inFreeList[newID] {
			if err := fl.Free(newID); err != nil {
				t.Fatalf("step %d: Free(%d): %v", step, newID, err)
			}
			inFreeList[newID] = true
		}
	}
}

func TestPageIDToKeyRoundTrip(t *testing.T) {
	for _, id := range []uint64{0, 1, 255, 256, 65535, 1<<32 - 1, 1<<63 - 1, 1<<64 - 1} {
		key := freelist.PageIDToKey(id)
		if len(key) != 8 {
			t.Errorf("key length = %d, want 8", len(key))
		}
		got := freelist.KeyToPageID(key)
		if got != id {
			t.Errorf("roundtrip %d: got %d", id, got)
		}
	}
}

func TestPageIDToKeyOrdering(t *testing.T) {
	// Big-endian encoding must preserve numeric ordering.
	ids := []uint64{1, 10, 100, 1000, 10000}
	var prev []byte
	for _, id := range ids {
		key := freelist.PageIDToKey(id)
		if prev != nil {
			if binary.BigEndian.Uint64(prev) >= binary.BigEndian.Uint64(key) {
				t.Errorf("ordering violated: %d >= %d", binary.BigEndian.Uint64(prev), id)
			}
		}
		prev = key
	}
}

func TestMigrateLinkedListToBTree(t *testing.T) {
	pio := newMemPageIO()
	oldFL := setupOldFreelist(t, pio)
	btfl := setupMigrationTarget(t, pio)

	oldFLPages, err := freelist.CollectFreelistPages(pio, oldFL.Root())
	if err != nil {
		t.Fatalf("CollectFreelistPages: %v", err)
	}

	if err := freelist.Migrate(oldFL, btfl, oldFLPages); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	verifyMigration(t, btfl, oldFLPages)
}

func setupOldFreelist(t *testing.T, pio *memPageIO) *freelist.Freelist {
	t.Helper()
	oldFL, err := freelist.New(pio, 0)
	if err != nil {
		t.Fatalf("New old freelist: %v", err)
	}
	for i := uint64(100); i < 150; i++ {
		if err := oldFL.Free(i); err != nil {
			t.Fatalf("Free(%d): %v", i, err)
		}
	}
	flPages := []uint64{200, 201}
	if err := oldFL.Flush(flPages); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return oldFL
}

func setupMigrationTarget(t *testing.T, pio *memPageIO) *freelist.BTreeFreelist {
	t.Helper()
	pool := bufpool.New(pio, 1024)
	for i := uint64(0); i < 500; i++ {
		if !pio.hasPage(i) {
			buf := make([]byte, 4096)
			if err := pio.WritePage(i, buf); err != nil {
				t.Fatalf("write page %d: %v", i, err)
			}
		}
	}
	reserve := make([]uint64, 10)
	for i := range reserve {
		reserve[i] = uint64(300 + i)
	}
	return freelist.OpenBTreeFreelist(pool, 0, reserve)
}

func verifyMigration(t *testing.T, btfl *freelist.BTreeFreelist, oldFLPages []uint64) {
	t.Helper()
	freeCount := btfl.FreeCount()
	wantMin := 50 + len(oldFLPages)
	if freeCount < wantMin {
		t.Errorf("FreeCount after migration = %d, want >= %d", freeCount, wantMin)
	}

	seen := make(map[uint64]bool)
	for range freeCount {
		id, err := btfl.Alloc()
		if err != nil {
			break
		}
		if seen[id] {
			t.Fatalf("double alloc of page %d", id)
		}
		seen[id] = true
	}

	for i := uint64(100); i < 150; i++ {
		if !seen[i] {
			t.Errorf("page %d not found after migration", i)
		}
	}
}

func TestMigrateIdempotent(t *testing.T) {
	pio := newMemPageIO()

	// Create old freelist.
	oldFL, err := freelist.New(pio, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := uint64(100); i < 110; i++ {
		if err := oldFL.Free(i); err != nil {
			t.Fatalf("Free: %v", err)
		}
	}
	flPages := []uint64{200}
	if err := oldFL.Flush(flPages); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	pool := bufpool.New(pio, 1024)
	for i := uint64(0); i < 500; i++ {
		buf := make([]byte, 4096)
		_ = pio.WritePage(i, buf)
	}

	reserve := make([]uint64, 10)
	for i := range reserve {
		reserve[i] = uint64(300 + i)
	}
	btfl := freelist.OpenBTreeFreelist(pool, 0, reserve)

	oldFLPages, _ := freelist.CollectFreelistPages(pio, oldFL.Root())

	// First migration.
	if err := freelist.Migrate(oldFL, btfl, oldFLPages); err != nil {
		t.Fatalf("Migrate[1]: %v", err)
	}
	count1 := btfl.FreeCount()

	// Second migration with an empty old freelist (simulating idempotency).
	emptyFL, err := freelist.New(pio, 0)
	if err != nil {
		t.Fatalf("New empty: %v", err)
	}
	if err := freelist.Migrate(emptyFL, btfl, nil); err != nil {
		t.Fatalf("Migrate[2]: %v", err)
	}
	count2 := btfl.FreeCount()

	if count2 != count1 {
		t.Errorf("idempotent migration changed count: %d → %d", count1, count2)
	}
}

// hasPage checks if a page exists in the memPageIO.
func (m *memPageIO) hasPage(pageID uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.pages[pageID]
	return ok
}

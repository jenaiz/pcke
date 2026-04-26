package bufpool_test

import (
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/bufpool"
	"github.com/jenaiz/pcke/internal/kdb/page"
)

func TestDynamicPool_GrowOnLowHitRate(t *testing.T) {
	t.Parallel()

	pio := newMemPageIO()
	// Create many pages so we can cause cache misses.
	for i := uint64(0); i < 200; i++ {
		pio.initPage(i, page.TypeLeaf)
	}

	// Small pool that will thrash.
	pool := bufpool.New(pio, 4)
	dp := bufpool.NewDynamic(pool, &bufpool.DynamicPoolConfig{
		MinPages: 4,
		MaxPages: 512,
	})

	// Access pattern that causes many misses (sequential scan exceeding pool).
	for i := uint64(0); i < 200; i++ {
		f, err := pool.Pin(i)
		if err != nil {
			t.Fatalf("Pin(%d): %v", i, err)
		}
		pool.Unpin(f.PageID)
	}

	// Sample should detect low hit rate and grow.
	initialMax := pool.MaxPages()
	dp.Sample()
	newMax := pool.MaxPages()

	if newMax <= initialMax {
		t.Errorf("expected pool to grow from %d, got %d", initialMax, newMax)
	}
}

func TestDynamicPool_ShrinkOnHighHitRate(t *testing.T) {
	t.Parallel()

	pio := newMemPageIO()
	for i := uint64(0); i < 10; i++ {
		pio.initPage(i, page.TypeLeaf)
	}

	// Large pool with small working set → high hit rate.
	pool := bufpool.New(pio, 256)

	// Pre-warm: load pages into the cache (causes initial misses).
	for i := uint64(0); i < 5; i++ {
		f, err := pool.Pin(i)
		if err != nil {
			t.Fatalf("warmup Pin(%d): %v", i, err)
		}
		pool.Unpin(f.PageID)
	}

	// Create DynamicPool after warmup so lastHits/lastMisses start from
	// the current counter values, avoiding cold-start misses.
	dp := bufpool.NewDynamic(pool, &bufpool.DynamicPoolConfig{
		MinPages: 16,
		MaxPages: 512,
	})
	// Seed the baseline counters.
	dp.Sample()

	// Now all accesses are hits. Generate enough consecutive high samples.
	for round := 0; round < 15; round++ {
		for i := uint64(0); i < 5; i++ {
			f, err := pool.Pin(i)
			if err != nil {
				t.Fatalf("Pin(%d): %v", i, err)
			}
			pool.Unpin(f.PageID)
		}
		dp.Sample()
	}

	newMax := pool.MaxPages()
	if newMax >= 256 {
		t.Errorf("expected pool to shrink from 256, got %d", newMax)
	}
}

func TestDynamicPool_RespectsMinMax(t *testing.T) {
	t.Parallel()

	pio := newMemPageIO()
	for i := uint64(0); i < 200; i++ {
		pio.initPage(i, page.TypeLeaf)
	}

	pool := bufpool.New(pio, 100)
	cfg := &bufpool.DynamicPoolConfig{
		MinPages: 50,
		MaxPages: 150,
	}
	dp := bufpool.NewDynamic(pool, cfg)

	// Force misses to trigger growth.
	for i := uint64(0); i < 200; i++ {
		f, err := pool.Pin(i)
		if err != nil {
			t.Fatalf("Pin(%d): %v", i, err)
		}
		pool.Unpin(f.PageID)
	}

	// Sample multiple times to grow.
	for range 5 {
		for i := uint64(100); i < 200; i++ {
			f, err := pool.Pin(i)
			if err != nil {
				t.Fatalf("Pin(%d): %v", i, err)
			}
			pool.Unpin(f.PageID)
		}
		dp.Sample()
	}

	poolMax := pool.MaxPages()
	if poolMax > 150 {
		t.Errorf("pool exceeded MaxPages bound: got %d, want <= 150", poolMax)
	}
}

func TestDynamicPool_NoActivityNoResize(t *testing.T) {
	t.Parallel()

	pio := newMemPageIO()
	pool := bufpool.New(pio, 128)
	dp := bufpool.NewDynamic(pool, nil)

	// Sample with no activity.
	dp.Sample()
	dp.Sample()
	dp.Sample()

	if pool.MaxPages() != 128 {
		t.Errorf("expected no resize, got maxPages=%d", pool.MaxPages())
	}
}

func TestDynamicPool_Stats(t *testing.T) {
	t.Parallel()

	pio := newMemPageIO()
	pool := bufpool.New(pio, 128)
	dp := bufpool.NewDynamic(pool, &bufpool.DynamicPoolConfig{
		MinPages: 32,
		MaxPages: 512,
	})

	stats := dp.Stats()
	if stats.MinPages != 32 {
		t.Errorf("MinPages: got %d, want 32", stats.MinPages)
	}
	if stats.MaxPages != 512 {
		t.Errorf("MaxPages: got %d, want 512", stats.MaxPages)
	}
	if stats.PoolStats.MaxPages != 128 {
		t.Errorf("current capacity: got %d, want 128", stats.PoolStats.MaxPages)
	}
}

func TestDynamicPool_ResizeConvenience(t *testing.T) {
	t.Parallel()

	pio := newMemPageIO()
	for i := uint64(0); i < 50; i++ {
		pio.initPage(i, page.TypeLeaf)
	}

	pool := bufpool.New(pio, 4)
	dp := bufpool.NewDynamic(pool, &bufpool.DynamicPoolConfig{
		MinPages: 4,
		MaxPages: 256,
	})

	// Force misses.
	for i := uint64(0); i < 50; i++ {
		f, err := pool.Pin(i)
		if err != nil {
			t.Fatalf("Pin(%d): %v", i, err)
		}
		pool.Unpin(f.PageID)
	}

	// Resize is a convenience wrapper around Sample.
	dp.Resize()

	if pool.MaxPages() <= 4 {
		t.Error("expected pool to grow after Resize()")
	}
}

func TestDynamicPool_StringStats(t *testing.T) {
	t.Parallel()

	pio := newMemPageIO()
	pool := bufpool.New(pio, 128)
	dp := bufpool.NewDynamic(pool, nil)

	s := dp.Stats().String()
	if s == "" {
		t.Error("expected non-empty stats string")
	}
}

func TestPool_MaxPagesAndSetMaxPages(t *testing.T) {
	t.Parallel()

	pio := newMemPageIO()
	pool := bufpool.New(pio, 128)

	if got := pool.MaxPages(); got != 128 {
		t.Errorf("MaxPages: got %d, want 128", got)
	}

	pool.SetMaxPages(256)
	if got := pool.MaxPages(); got != 256 {
		t.Errorf("after SetMaxPages(256): got %d, want 256", got)
	}

	// SetMaxPages with 0 clamps to 1.
	pool.SetMaxPages(0)
	if got := pool.MaxPages(); got != 1 {
		t.Errorf("after SetMaxPages(0): got %d, want 1", got)
	}
}

func TestDynamicPool_Pool(t *testing.T) {
	t.Parallel()

	pio := newMemPageIO()
	pool := bufpool.New(pio, 64)
	dp := bufpool.NewDynamic(pool, nil)

	if dp.Pool() != pool {
		t.Error("Pool() did not return the underlying pool")
	}
}

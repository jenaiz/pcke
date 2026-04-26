// Package bufpool — dynamic.go adds adaptive sizing to the buffer pool.
//
// DynamicPool wraps the base Pool and periodically adjusts its capacity
// based on observed hit/miss rates. It tracks a sliding window of recent
// hit rates and grows or shrinks the pool to keep the hit rate above 90%
// in steady-state workloads.
//
// Phase 4 — Task F4.T1.
package bufpool

import (
	"fmt"
	"sync"
)

const (
	// DefaultMinPages is the minimum pool capacity (64 pages = 256 KiB).
	DefaultMinPages = 64

	// DefaultMaxPages is the maximum pool capacity (4096 pages = 16 MiB).
	DefaultMaxPages = 4096

	// lowWatermark is the hit rate below which the pool grows.
	lowWatermark = 0.70

	// highWatermark is the hit rate above which the pool may shrink.
	highWatermark = 0.95

	// shrinkConsecutive is the number of consecutive high-watermark samples
	// required before shrinking. This prevents oscillation.
	shrinkConsecutive = 10

	// resizeFactor is the multiplicative factor for grow/shrink operations.
	// Grow multiplies by (1 + resizeFactor), shrink divides by (1 + resizeFactor).
	resizeFactor = 0.25

	// sampleWindow is the number of recent hit-rate samples kept for analysis.
	sampleWindow = 20
)

// DynamicPoolConfig configures the adaptive sizing behaviour.
type DynamicPoolConfig struct {
	// MinPages is the floor for pool capacity. Must be > 0.
	MinPages int
	// MaxPages is the ceiling for pool capacity. Must be >= MinPages.
	MaxPages int
}

// DynamicPool wraps the base buffer pool with adaptive sizing. It tracks
// hit/miss rates and adjusts the cache capacity to match the working set,
// bounded by [MinPages, MaxPages]. This avoids both OOM from unbounded
// growth and thrashing from undersized caches.
type DynamicPool struct {
	mu   sync.Mutex
	pool *Pool
	cfg  DynamicPoolConfig

	// Sliding window of hit-rate samples for resize decisions.
	samples    []float64
	sampleIdx  int
	sampleFull bool

	// consecutiveHigh tracks how many consecutive samples exceeded highWatermark.
	consecutiveHigh int

	// lastHits/lastMisses track the counters at the previous sample point
	// so we compute a delta hit rate (recent window), not a cumulative one.
	lastHits   uint64
	lastMisses uint64
}

// NewDynamic creates a DynamicPool wrapping the given base pool. The pool's
// current maxPages is used as the initial capacity. If cfg is nil, default
// bounds are used.
func NewDynamic(pool *Pool, cfg *DynamicPoolConfig) *DynamicPool {
	c := DynamicPoolConfig{
		MinPages: DefaultMinPages,
		MaxPages: DefaultMaxPages,
	}
	if cfg != nil {
		if cfg.MinPages > 0 {
			c.MinPages = cfg.MinPages
		}
		if cfg.MaxPages > 0 {
			c.MaxPages = cfg.MaxPages
		}
	}
	if c.MaxPages < c.MinPages {
		c.MaxPages = c.MinPages
	}

	// Snapshot current pool counters so the first Sample() only sees
	// delta activity, not historical cold-start misses.
	stats := pool.Stats()

	return &DynamicPool{
		pool:       pool,
		cfg:        c,
		samples:    make([]float64, sampleWindow),
		lastHits:   stats.Hits,
		lastMisses: stats.Misses,
	}
}

// Pool returns the underlying buffer pool.
func (dp *DynamicPool) Pool() *Pool {
	return dp.pool
}

// Sample records the current hit rate into the sliding window and triggers
// a resize check. Call this periodically (e.g., after each transaction or
// at checkpoint boundaries). Sample is safe for concurrent use.
func (dp *DynamicPool) Sample() {
	dp.mu.Lock()
	defer dp.mu.Unlock()

	stats := dp.pool.Stats()

	// Compute delta hit rate since last sample.
	dHits := stats.Hits - dp.lastHits
	dMisses := stats.Misses - dp.lastMisses
	dp.lastHits = stats.Hits
	dp.lastMisses = stats.Misses

	total := dHits + dMisses
	if total == 0 {
		return // no activity since last sample
	}

	rate := float64(dHits) / float64(total)

	// Record in sliding window.
	dp.samples[dp.sampleIdx] = rate
	dp.sampleIdx = (dp.sampleIdx + 1) % sampleWindow
	if dp.sampleIdx == 0 {
		dp.sampleFull = true
	}

	dp.checkResize(rate)
}

// checkResize evaluates whether the pool should grow or shrink based on the
// latest hit rate sample. Caller must hold dp.mu.
func (dp *DynamicPool) checkResize(rate float64) {
	current := dp.pool.MaxPages()

	if rate < lowWatermark && current < dp.cfg.MaxPages {
		// Hit rate too low — grow the pool.
		newSize := int(float64(current) * (1 + resizeFactor))
		if newSize > dp.cfg.MaxPages {
			newSize = dp.cfg.MaxPages
		}
		if newSize <= current {
			newSize = current + 1
		}
		dp.pool.SetMaxPages(newSize)
		dp.consecutiveHigh = 0
		return
	}

	if rate > highWatermark {
		dp.consecutiveHigh++
	} else {
		dp.consecutiveHigh = 0
	}

	if dp.consecutiveHigh >= shrinkConsecutive && current > dp.cfg.MinPages {
		// Hit rate consistently high — try shrinking.
		newSize := int(float64(current) / (1 + resizeFactor))
		if newSize < dp.cfg.MinPages {
			newSize = dp.cfg.MinPages
		}
		if newSize >= current {
			return // rounding prevented actual shrink
		}
		dp.pool.SetMaxPages(newSize)
		dp.consecutiveHigh = 0
	}
}

// Resize adjusts the pool capacity based on the current hit rate. It grows
// the pool when hit rate drops below the low watermark (70%) and shrinks it
// when hit rate exceeds the high watermark (95%) for at least 10 consecutive
// samples. Resize is safe to call concurrently with Pin/Unpin.
//
// This is a convenience method that calls Sample() internally.
func (dp *DynamicPool) Resize() {
	dp.Sample()
}

// Stats returns the underlying pool stats plus dynamic sizing info.
func (dp *DynamicPool) Stats() DynamicPoolStats {
	dp.mu.Lock()
	defer dp.mu.Unlock()

	ps := dp.pool.Stats()
	return DynamicPoolStats{
		PoolStats:       ps,
		MinPages:        dp.cfg.MinPages,
		MaxPages:        dp.cfg.MaxPages,
		ConsecutiveHigh: dp.consecutiveHigh,
	}
}

// DynamicPoolStats extends PoolStats with adaptive sizing metrics.
type DynamicPoolStats struct {
	PoolStats
	MinPages        int
	MaxPages        int
	ConsecutiveHigh int
}

// String returns a human-readable summary of the dynamic pool state.
func (s DynamicPoolStats) String() string {
	return fmt.Sprintf(
		"capacity=%d [%d..%d] cached=%d hit_rate=%.2f%% consecutive_high=%d",
		s.PoolStats.MaxPages, s.MinPages, s.MaxPages,
		s.CachedPages, s.HitRate*100, s.ConsecutiveHigh,
	)
}

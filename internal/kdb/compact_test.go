package kdb_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// TestCompactReducesSize verifies that compaction reduces database size
// by > 10% after synthetic churn (create+delete cycles). This is the
// F2.T8 acceptance criterion.
func TestCompactReducesSize(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	ctx := context.Background()

	// Pre-grow to have a reasonable base size.
	for range 50 {
		if err := db.Grow(); err != nil {
			t.Fatalf("Grow: %v", err)
		}
	}

	compactChurn(ctx, t, db, 0, 200)
	compactChurn(ctx, t, db, 200, 500)

	if err := db.Checkpoint(ctx); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	preCount := compactCount(ctx, t, db)

	result, err := db.Compact(ctx)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	t.Logf("compact: %d → %d bytes (%d keys)", result.OldSize, result.NewSize, result.KeysCopied)

	reduction := float64(result.OldSize-result.NewSize) / float64(result.OldSize) * 100
	if reduction < 10 {
		t.Errorf("compact reduction = %.1f%%, want > 10%%", reduction)
	}

	postCount := compactCount(ctx, t, db)
	if postCount != preCount {
		t.Errorf("data loss: had %d keys, now have %d", preCount, postCount)
	}

	compactVerifyKeys(ctx, t, db)

	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// compactChurn creates keys [start, end) then deletes 80% of them.
func compactChurn(ctx context.Context, t *testing.T, db *kdb.DB, start, end int) {
	t.Helper()
	err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		for i := start; i < end; i++ {
			key := fmt.Sprintf("churn-%05d", i)
			val := fmt.Sprintf("value-%05d-padding-to-make-it-larger-and-use-more-space-in-pages", i)
			if err := wtx.Put([]byte(key), []byte(val)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("create [%d,%d): %v", start, end, err)
	}

	err = db.Update(ctx, func(wtx *tx.WriteTx) error {
		for i := start; i < end; i++ {
			if i%5 == 0 {
				continue // keep every 5th key
			}
			key := fmt.Sprintf("churn-%05d", i)
			if err := wtx.Delete([]byte(key)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("delete [%d,%d): %v", start, end, err)
	}
}

// compactCount returns the number of live keys.
func compactCount(ctx context.Context, t *testing.T, db *kdb.DB) int {
	t.Helper()
	var count int
	err := db.View(ctx, func(rtx *tx.ReadTx) error {
		c := rtx.Cursor()
		if !c.First() {
			return nil
		}
		for c.Valid() {
			count++
			if !c.Next() {
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	return count
}

// compactVerifyKeys spot-checks that kept keys are readable.
func compactVerifyKeys(ctx context.Context, t *testing.T, db *kdb.DB) {
	t.Helper()
	err := db.View(ctx, func(rtx *tx.ReadTx) error {
		for _, i := range []int{0, 5, 10, 200, 205} {
			key := fmt.Sprintf("churn-%05d", i)
			val, err := rtx.Get([]byte(key))
			if err != nil {
				return fmt.Errorf("get %s: %w", key, err)
			}
			if len(val) == 0 {
				return fmt.Errorf("empty value for %s", key)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("post-compact verify: %v", err)
	}
}

// TestCompactMultiBatch triggers the multi-batch write path (>500 keys).
func TestCompactMultiBatch(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	ctx := context.Background()

	// Pre-grow to have pages available.
	for range 100 {
		if err := db.Grow(); err != nil {
			t.Fatalf("Grow: %v", err)
		}
	}

	compactMultiBatchInsert(ctx, t, db)

	if err := db.Checkpoint(ctx); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	result, err := db.Compact(ctx)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if result.KeysCopied < 650 {
		t.Errorf("expected >= 650 keys copied, got %d", result.KeysCopied)
	}

	compactMultiBatchVerify(ctx, t, db)

	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func compactMultiBatchInsert(ctx context.Context, t *testing.T, db *kdb.DB) {
	t.Helper()
	for batch := 0; batch < 13; batch++ {
		err := db.Update(ctx, func(wtx *tx.WriteTx) error {
			for i := batch * 50; i < (batch+1)*50; i++ {
				key := fmt.Sprintf("batch-%05d", i)
				val := fmt.Sprintf("val-%05d-some-padding", i)
				if err := wtx.Put([]byte(key), []byte(val)); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("insert batch %d: %v", batch, err)
		}
	}
}

func compactMultiBatchVerify(ctx context.Context, t *testing.T, db *kdb.DB) {
	t.Helper()
	err := db.View(ctx, func(rtx *tx.ReadTx) error {
		val, err := rtx.Get([]byte("batch-00000"))
		if err != nil {
			return err
		}
		if len(val) == 0 {
			return fmt.Errorf("empty value for batch-00000")
		}
		val, err = rtx.Get([]byte("batch-00649"))
		if err != nil {
			return err
		}
		if len(val) == 0 {
			return fmt.Errorf("empty value for batch-00649")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("post-compact verify: %v", err)
	}
}

// TestCompactEmptyDB tests compaction on a database with no user data.
func TestCompactEmptyDB(t *testing.T) {
	dir := testDir(t)
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	ctx := context.Background()
	result, err := db.Compact(ctx)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if result.KeysCopied != 0 {
		t.Errorf("expected 0 keys on empty DB, got %d", result.KeysCopied)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

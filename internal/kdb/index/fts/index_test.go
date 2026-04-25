package fts

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

func TestIndexAddAndSearch(t *testing.T) {
	idx := NewIndex()
	idx.AddDocument("hello world")
	idx.AddDocument("hello universe")
	idx.Commit()

	postings := idx.Search("hello")
	if len(postings) != 2 {
		t.Fatalf("Search(hello) = %d postings, want 2", len(postings))
	}

	postings = idx.Search("world")
	if len(postings) != 1 {
		t.Fatalf("Search(world) = %d postings, want 1", len(postings))
	}
}

func TestIndexDeleteAndSearch(t *testing.T) {
	idx := NewIndex()
	d1 := idx.AddDocument("hello world")
	idx.AddDocument("hello universe")
	idx.Commit()

	idx.DeleteDocument(d1)

	postings := idx.Search("hello")
	if len(postings) != 1 {
		t.Fatalf("Search(hello) after delete = %d, want 1", len(postings))
	}
	if postings[0].DocID == d1 {
		t.Error("deleted doc should not appear in results")
	}
}

func TestIndexMergeReducesSegments(t *testing.T) {
	idx := NewIndex()

	// Create multiple segments.
	for i := range 15 {
		idx.AddDocument(fmt.Sprintf("doc %d common term", i))
		idx.Commit()
	}

	if idx.SegmentCount() != 15 {
		t.Fatalf("SegmentCount = %d before merge, want 15", idx.SegmentCount())
	}

	idx.Merge()

	if idx.SegmentCount() != 1 {
		t.Fatalf("SegmentCount = %d after merge, want 1", idx.SegmentCount())
	}

	// All docs should still be searchable.
	postings := idx.Search("common")
	if len(postings) != 15 {
		t.Errorf("Search(common) after merge = %d, want 15", len(postings))
	}
}

func TestIndexMergeDropsTombstones(t *testing.T) {
	idx := NewIndex()

	var docIDs []uint64
	for range 10 {
		id := idx.AddDocument("searchable content")
		docIDs = append(docIDs, id)
		idx.Commit()
	}

	// Delete half the docs.
	for _, id := range docIDs[:5] {
		idx.DeleteDocument(id)
	}

	idx.Merge()

	postings := idx.Search("searchable")
	if len(postings) != 5 {
		t.Errorf("Search after merge+tombstones = %d, want 5", len(postings))
	}

	// TotalDocs should reflect only live docs.
	if idx.TotalDocs() != 5 {
		t.Errorf("TotalDocs = %d, want 5", idx.TotalDocs())
	}
}

func TestIndexMergeSingleSegment(t *testing.T) {
	idx := NewIndex()
	idx.AddDocument("hello")
	idx.Commit()

	idx.Merge() // Should be a no-op with 1 segment.

	if idx.SegmentCount() != 1 {
		t.Errorf("SegmentCount = %d, want 1", idx.SegmentCount())
	}
}

func TestIndexSearchUncommitted(t *testing.T) {
	idx := NewIndex()
	idx.AddDocument("uncommitted data")

	// Should find uncommitted data in active segment.
	postings := idx.Search("uncommitted")
	if len(postings) != 1 {
		t.Errorf("Search(uncommitted) = %d, want 1", len(postings))
	}
}

func TestIndexConsistencyRandomMutations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping random mutation test in short mode")
	}

	idx := NewIndex()
	rng := rand.New(rand.NewPCG(42, 0)) //nolint:gosec // G404: deterministic seed.

	alive := make(map[uint64]bool)
	var allDocs []uint64

	for i := range 10_000 {
		switch rng.IntN(4) {
		case 0, 1: // Add doc (50%).
			id := idx.AddDocument(fmt.Sprintf("word_%d common", i))
			alive[id] = true
			allDocs = append(allDocs, id)
		case 2: // Delete random doc (25%).
			if len(allDocs) > 0 {
				pick := allDocs[rng.IntN(len(allDocs))]
				idx.DeleteDocument(pick)
				delete(alive, pick)
			}
		case 3: // Commit (25%).
			idx.Commit()
		}

		// Periodic merge.
		if i%1000 == 999 {
			idx.Merge()
		}
	}

	idx.Commit()
	idx.Merge()

	// Verify "common" postings match alive set.
	postings := idx.Search("common")
	got := make(map[uint64]bool)
	for _, p := range postings {
		got[p.DocID] = true
	}

	for id := range alive {
		if !got[id] {
			t.Errorf("alive doc %d missing from search results", id)
		}
	}
	for id := range got {
		if !alive[id] {
			t.Errorf("deleted/unknown doc %d present in search results", id)
		}
	}

	t.Logf("10K ops: %d alive docs, %d segments after merge",
		len(alive), idx.SegmentCount())
}

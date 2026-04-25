package fts

import "testing"

func TestTombstoneMarkAndQuery(t *testing.T) {
	ts := NewTombstones()

	if ts.IsDeleted(1) {
		t.Error("doc 1 should not be deleted initially")
	}

	ts.Mark(1)
	ts.Mark(5)

	if !ts.IsDeleted(1) {
		t.Error("doc 1 should be deleted after Mark")
	}
	if !ts.IsDeleted(5) {
		t.Error("doc 5 should be deleted after Mark")
	}
	if ts.IsDeleted(2) {
		t.Error("doc 2 should not be deleted")
	}
	if ts.Count() != 2 {
		t.Errorf("Count = %d, want 2", ts.Count())
	}
}

func TestTombstoneFilterPostings(t *testing.T) {
	ts := NewTombstones()
	ts.Mark(2)
	ts.Mark(4)

	postings := []Posting{
		{DocID: 1, Freq: 1},
		{DocID: 2, Freq: 1},
		{DocID: 3, Freq: 1},
		{DocID: 4, Freq: 1},
		{DocID: 5, Freq: 1},
	}

	filtered := ts.FilterPostings(postings)
	if len(filtered) != 3 {
		t.Fatalf("filtered len = %d, want 3", len(filtered))
	}

	want := []uint64{1, 3, 5}
	for i, p := range filtered {
		if p.DocID != want[i] {
			t.Errorf("filtered[%d].DocID = %d, want %d", i, p.DocID, want[i])
		}
	}

	// Original slice should be unmodified.
	if len(postings) != 5 {
		t.Errorf("original postings modified: len = %d", len(postings))
	}
}

func TestTombstoneFilterPostingsEmpty(t *testing.T) {
	ts := NewTombstones()

	postings := []Posting{{DocID: 1, Freq: 1}}
	filtered := ts.FilterPostings(postings)

	// No tombstones → should return original slice.
	if len(filtered) != 1 || filtered[0].DocID != 1 {
		t.Error("filter with no tombstones should return input unchanged")
	}
}

func TestTombstoneClear(t *testing.T) {
	ts := NewTombstones()
	ts.Mark(1)
	ts.Mark(2)

	if ts.Count() != 2 {
		t.Fatalf("Count = %d before clear", ts.Count())
	}

	ts.Clear()

	if ts.Count() != 0 {
		t.Errorf("Count = %d after clear, want 0", ts.Count())
	}
	if ts.IsDeleted(1) {
		t.Error("doc 1 should not be deleted after clear")
	}
}

func TestTombstoneDeletedIDs(t *testing.T) {
	ts := NewTombstones()
	ts.Mark(10)
	ts.Mark(5)
	ts.Mark(20)

	ids := ts.DeletedIDs()
	if len(ids) != 3 {
		t.Fatalf("DeletedIDs len = %d, want 3", len(ids))
	}

	// IDs may be in any order; just check all are present.
	seen := make(map[uint64]bool)
	for _, id := range ids {
		seen[id] = true
	}
	for _, want := range []uint64{5, 10, 20} {
		if !seen[want] {
			t.Errorf("missing ID %d in DeletedIDs", want)
		}
	}
}

func TestSearchWithTombstones(t *testing.T) {
	// Build a segment.
	ms := NewMemSegment(1)
	ms.AddDocument(1, "hello world")
	ms.AddDocument(2, "hello universe")
	ms.AddDocument(3, "hello earth")
	seg := ms.Freeze()

	// Delete doc 2.
	ts := NewTombstones()
	ts.Mark(2)

	// Search and filter.
	postings := seg.Search("hello")
	filtered := ts.FilterPostings(postings)

	if len(filtered) != 2 {
		t.Fatalf("filtered len = %d, want 2", len(filtered))
	}

	for _, p := range filtered {
		if p.DocID == 2 {
			t.Error("tombstoned doc 2 should be excluded from results")
		}
	}
}

func TestTombstoneMarkIdempotent(t *testing.T) {
	ts := NewTombstones()
	ts.Mark(1)
	ts.Mark(1)
	ts.Mark(1)

	if ts.Count() != 1 {
		t.Errorf("Count = %d after marking same ID 3 times, want 1", ts.Count())
	}
}

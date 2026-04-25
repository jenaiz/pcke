package fts

import "sync"

// Tombstones tracks deleted document IDs across all segments.
//
// When a document is deleted, its ID is added to the tombstone set.
// Queries filter out tombstoned docs from search results. During
// segment merge (F1.T8), tombstoned docs are physically removed.
//
// Concurrency: Tombstones is safe for concurrent read access after
// construction. Mutations (Mark) must be serialized by the caller
// (typically via the DB write lock).
type Tombstones struct {
	mu      sync.RWMutex
	deleted map[uint64]struct{}
}

// NewTombstones creates an empty tombstone set.
func NewTombstones() *Tombstones {
	return &Tombstones{
		deleted: make(map[uint64]struct{}),
	}
}

// Mark adds a document ID to the tombstone set.
func (ts *Tombstones) Mark(docID uint64) {
	ts.mu.Lock()
	ts.deleted[docID] = struct{}{}
	ts.mu.Unlock()
}

// IsDeleted returns true if the document ID has been tombstoned.
func (ts *Tombstones) IsDeleted(docID uint64) bool {
	ts.mu.RLock()
	_, ok := ts.deleted[docID]
	ts.mu.RUnlock()
	return ok
}

// Count returns the number of tombstoned document IDs.
func (ts *Tombstones) Count() int {
	ts.mu.RLock()
	n := len(ts.deleted)
	ts.mu.RUnlock()
	return n
}

// FilterPostings returns a new slice of postings with tombstoned docs removed.
// The original slice is not modified.
func (ts *Tombstones) FilterPostings(postings []Posting) []Posting {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	if len(ts.deleted) == 0 {
		return postings
	}

	result := make([]Posting, 0, len(postings))
	for _, p := range postings {
		if _, del := ts.deleted[p.DocID]; !del {
			result = append(result, p)
		}
	}
	return result
}

// DeletedIDs returns all tombstoned document IDs as a sorted slice.
// Used during merge to drop deleted docs from merged segments.
func (ts *Tombstones) DeletedIDs() []uint64 {
	ts.mu.RLock()
	ids := make([]uint64, 0, len(ts.deleted))
	for id := range ts.deleted {
		ids = append(ids, id)
	}
	ts.mu.RUnlock()
	return ids
}

// Clear removes all tombstones. Called after a merge has physically
// removed the deleted docs from the merged segments.
func (ts *Tombstones) Clear() {
	ts.mu.Lock()
	ts.deleted = make(map[uint64]struct{})
	ts.mu.Unlock()
}

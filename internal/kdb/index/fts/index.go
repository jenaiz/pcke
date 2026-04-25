package fts

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Index is the top-level full-text search index that manages multiple
// immutable segments and a mutable in-memory segment.
//
// The Index lifecycle:
//  1. Documents are added to the active [MemSegment] via AddDocument.
//  2. On commit, the MemSegment is frozen into an immutable [Segment].
//  3. At checkpoint, [Index.Merge] reduces segments via tiered merge.
//
// Concurrency: Index is safe for concurrent reads (Search) and
// serialized writes (AddDocument, Commit, Merge). The caller (kdb.DB)
// ensures write serialization via its global RWMutex.
//
// Phase 1 — Tasks F1.T5–F1.T8.
type Index struct {
	mu         sync.RWMutex
	segments   []*Segment
	active     *MemSegment
	tombstones *Tombstones
	nextSegID  atomic.Uint64
	nextDocID  atomic.Uint64
}

// NewIndex creates an empty FTS index.
func NewIndex() *Index {
	idx := &Index{
		tombstones: NewTombstones(),
	}
	idx.nextSegID.Store(1)
	idx.nextDocID.Store(1)
	idx.active = NewMemSegment(idx.nextSegID.Add(1) - 1)
	return idx
}

// AddDocument tokenizes text and adds it to the active in-memory segment.
// Returns the assigned document ID.
func (idx *Index) AddDocument(text string) uint64 {
	docID := idx.nextDocID.Add(1) - 1
	idx.active.AddDocument(docID, text)
	return docID
}

// DeleteDocument marks a document as deleted. The document will be
// excluded from future search results and physically removed on merge.
func (idx *Index) DeleteDocument(docID uint64) {
	idx.tombstones.Mark(docID)
}

// Commit freezes the active MemSegment into an immutable Segment and
// starts a new MemSegment for the next transaction.
func (idx *Index) Commit() {
	if idx.active.DocCount() == 0 {
		return
	}

	seg := idx.active.Freeze()

	idx.mu.Lock()
	idx.segments = append(idx.segments, seg)
	idx.mu.Unlock()

	idx.active = NewMemSegment(idx.nextSegID.Add(1) - 1)
}

// Search returns all non-tombstoned postings for the given term across
// all segments, merged by document ID.
func (idx *Index) Search(term string) []Posting {
	idx.mu.RLock()
	segs := idx.segments
	idx.mu.RUnlock()

	var all []Posting
	for _, seg := range segs {
		postings := seg.Search(term)
		all = append(all, postings...)
	}

	// Also search the active MemSegment (unfrozen docs).
	if idx.active.DocCount() > 0 {
		frozen := idx.active.Freeze()
		all = append(all, frozen.Search(term)...)
	}

	// Filter tombstoned docs.
	all = idx.tombstones.FilterPostings(all)

	// Sort by DocID for deterministic output.
	sort.Slice(all, func(i, j int) bool {
		return all[i].DocID < all[j].DocID
	})

	return all
}

// SegmentCount returns the number of immutable segments.
func (idx *Index) SegmentCount() int {
	idx.mu.RLock()
	n := len(idx.segments)
	idx.mu.RUnlock()
	return n
}

// TotalDocs returns the total number of documents across all segments
// (not counting tombstoned docs).
func (idx *Index) TotalDocs() uint32 {
	idx.mu.RLock()
	segs := idx.segments
	idx.mu.RUnlock()

	var total uint32
	for _, seg := range segs {
		total += seg.DocCount
	}
	return total
}

// Merge performs a tiered merge of segments, reducing segment count.
//
// The merge policy: when the total number of segments exceeds
// [maxSegmentsPerTier], merge all segments into a single new segment,
// dropping tombstoned documents. This is a simple policy for Phase 1;
// true tiered merge (grouping by size tiers) is a post-v1 optimization.
//
// Called at checkpoint boundaries to share fsync cost.
func (idx *Index) Merge() {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if len(idx.segments) <= 1 {
		return
	}

	// Merge all segments into one.
	merged := idx.mergeSegments(idx.segments)
	idx.segments = []*Segment{merged}
	idx.tombstones.Clear()
}

// mergeSegments combines multiple segments into one, dropping tombstoned docs.
func (idx *Index) mergeSegments(segments []*Segment) *Segment {
	newID := idx.nextSegID.Add(1) - 1
	ms := NewMemSegment(newID)

	// Collect all unique terms.
	termSet := make(map[string]struct{})
	for _, seg := range segments {
		for term := range seg.Terms {
			termSet[term] = struct{}{}
		}
	}

	// For each term, merge posting lists across all segments.
	mergedTerms := make(map[string]*PostingList, len(termSet))
	for term := range termSet {
		var merged []Posting
		for _, seg := range segments {
			pl, ok := seg.Terms[term]
			if !ok {
				continue
			}
			// Filter tombstoned docs.
			for _, p := range pl.Postings {
				if !idx.tombstones.IsDeleted(p.DocID) {
					merged = append(merged, p)
				}
			}
		}
		if len(merged) == 0 {
			continue
		}
		// Sort by DocID and deduplicate.
		sort.Slice(merged, func(i, j int) bool {
			return merged[i].DocID < merged[j].DocID
		})
		merged = deduplicatePostings(merged)
		mergedTerms[term] = &PostingList{Postings: merged}
	}

	// Merge norms, dropping tombstoned docs.
	mergedNorms := make(map[uint64]uint32)
	var docCount uint32
	var totalLen uint64

	for _, seg := range segments {
		for docID, norm := range seg.Norms {
			if idx.tombstones.IsDeleted(docID) {
				continue
			}
			if _, exists := mergedNorms[docID]; !exists {
				mergedNorms[docID] = norm
				docCount++
				totalLen += uint64(norm)
			}
		}
	}

	_ = ms // we built the result directly

	return &Segment{
		ID:       newID,
		Terms:    mergedTerms,
		Norms:    mergedNorms,
		DocCount: docCount,
		TotalLen: totalLen,
	}
}

// deduplicatePostings removes duplicate DocID entries, keeping the last one.
func deduplicatePostings(postings []Posting) []Posting {
	if len(postings) <= 1 {
		return postings
	}
	result := make([]Posting, 0, len(postings))
	for i, p := range postings {
		if i+1 < len(postings) && postings[i+1].DocID == p.DocID {
			continue // skip, next posting for same doc wins
		}
		result = append(result, p)
	}
	return result
}

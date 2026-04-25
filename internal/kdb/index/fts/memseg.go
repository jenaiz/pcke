package fts

import "sort"

// MemSegment accumulates postings for the current write transaction.
//
// Documents are added via [MemSegment.AddDocument], which tokenizes the text
// and builds an in-memory inverted index. When the transaction commits,
// [MemSegment.Freeze] converts this into an immutable [Segment].
//
// Concurrency: MemSegment is NOT safe for concurrent use. It is owned by
// a single write transaction; kdb serializes writes with a global RWMutex.
type MemSegment struct {
	id       uint64
	terms    map[string][]Posting
	norms    map[uint64]uint32
	docCount uint32
	totalLen uint64
	memSize  uint64
}

// NewMemSegment creates a mutable in-memory segment with the given ID.
func NewMemSegment(id uint64) *MemSegment {
	return &MemSegment{
		id:    id,
		terms: make(map[string][]Posting),
		norms: make(map[uint64]uint32),
	}
}

// AddDocument tokenizes text and indexes it under the given docID.
//
// Each call contributes to the document's field length (norm). Calling
// AddDocument multiple times for the same docID with different text
// is valid — it models indexing multiple fields of the same document.
// Positions continue from the previous call's last position.
func (ms *MemSegment) AddDocument(docID uint64, text string) {
	tokens := Tokenize(text)
	if len(tokens) == 0 {
		return
	}

	// Accumulate field length.
	fieldLen := uint32(len(tokens)) //nolint:gosec // G115: token count bounded by input text length.
	ms.norms[docID] += fieldLen
	ms.totalLen += uint64(fieldLen)

	// Group tokens by term for this document.
	type termHit struct {
		freq      uint32
		positions []uint32
	}

	hits := make(map[string]*termHit)
	for _, tok := range tokens {
		h, ok := hits[tok.Term]
		if !ok {
			h = &termHit{}
			hits[tok.Term] = h
		}
		h.freq++
		h.positions = append(h.positions, uint32(tok.Position)) //nolint:gosec // G115: position bounded by token count.
	}

	// Merge into the segment's inverted index.
	for term, h := range hits {
		sort.Slice(h.positions, func(i, j int) bool {
			return h.positions[i] < h.positions[j]
		})
		ms.terms[term] = append(ms.terms[term], Posting{
			DocID:     docID,
			Freq:      h.freq,
			Positions: h.positions,
		})
		// Rough memory estimate: term string + posting overhead.
		ms.memSize += uint64(len(term)) + uint64(16+4*len(h.positions))
	}

	// Track unique documents.
	if ms.norms[docID] == fieldLen {
		// First time seeing this docID.
		ms.docCount++
	}
}

// Freeze converts this mutable segment into an immutable [Segment].
//
// After Freeze, the MemSegment should not be used — its internal maps
// are transferred to the returned Segment to avoid copying.
func (ms *MemSegment) Freeze() *Segment {
	terms := make(map[string]*PostingList, len(ms.terms))
	for term, postings := range ms.terms {
		// Sort postings by DocID for deterministic output.
		sort.Slice(postings, func(i, j int) bool {
			return postings[i].DocID < postings[j].DocID
		})
		terms[term] = &PostingList{Postings: postings}
	}

	return &Segment{
		ID:       ms.id,
		Terms:    terms,
		Norms:    ms.norms,
		DocCount: ms.docCount,
		TotalLen: ms.totalLen,
	}
}

// Size returns the estimated memory usage in bytes.
func (ms *MemSegment) Size() uint64 {
	return ms.memSize
}

// DocCount returns the number of unique documents indexed.
func (ms *MemSegment) DocCount() uint32 {
	return ms.docCount
}

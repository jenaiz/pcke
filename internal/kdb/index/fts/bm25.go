package fts

import "math"

// BM25 scoring parameters (standard Okapi BM25).
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// BM25Result holds a scored document from a BM25 query.
type BM25Result struct {
	DocID uint64
	Score float64
}

// ScoreBM25 computes BM25 scores for a multi-term query across the index.
//
// The BM25 formula:
//
//	score(D, Q) = Σ IDF(qi) · f(qi,D)·(k1+1) / (f(qi,D) + k1·(1-b+b·|D|/avgdl))
//
// Where:
//   - f(qi,D) = term frequency of qi in document D
//   - |D| = document length (number of tokens)
//   - avgdl = average document length across the collection
//   - IDF(qi) = ln((N - n(qi) + 0.5) / (n(qi) + 0.5) + 1)
//   - N = total document count
//   - n(qi) = number of documents containing qi
//
// Results are returned sorted by score descending.
func (idx *Index) ScoreBM25(queryTerms []string) []BM25Result {
	idx.mu.RLock()
	segs := idx.segments
	idx.mu.RUnlock()

	// Compute global stats.
	var totalDocs uint32
	var totalLen uint64

	for _, seg := range segs {
		totalDocs += seg.DocCount
		totalLen += seg.TotalLen
	}

	if totalDocs == 0 {
		return nil
	}

	avgdl := float64(totalLen) / float64(totalDocs)
	n := float64(totalDocs)

	// lookupNorm finds the document length for a given docID by scanning
	// segments. This avoids copying all norms into a temporary map on every
	// query — only docs that match a query term are looked up.
	lookupNorm := func(docID uint64) uint32 {
		for _, seg := range segs {
			if norm, ok := seg.Norms[docID]; ok {
				return norm
			}
		}
		return 0
	}

	// Score accumulator per document.
	scores := make(map[uint64]float64)

	for _, term := range queryTerms {
		// Gather postings for this term across all segments.
		var termPostings []Posting
		for _, seg := range segs {
			termPostings = append(termPostings, seg.Search(term)...)
		}

		// Filter tombstoned docs.
		termPostings = idx.tombstones.FilterPostings(termPostings)

		if len(termPostings) == 0 {
			continue
		}

		// IDF for this term.
		df := float64(len(termPostings))
		idf := math.Log((n-df+0.5)/(df+0.5) + 1)

		// Score each document.
		for _, p := range termPostings {
			tf := float64(p.Freq)
			dl := float64(lookupNorm(p.DocID))
			num := tf * (bm25K1 + 1)
			denom := tf + bm25K1*(1-bm25B+bm25B*dl/avgdl)
			scores[p.DocID] += idf * num / denom
		}
	}

	// Convert to sorted results.
	results := make([]BM25Result, 0, len(scores))
	for docID, score := range scores {
		results = append(results, BM25Result{DocID: docID, Score: score})
	}

	// Sort by score descending, then by DocID ascending for stability.
	sortResults(results)
	return results
}

// sortResults sorts BM25 results by score descending, DocID ascending.
func sortResults(results []BM25Result) {
	for i := 1; i < len(results); i++ {
		for j := i; j > 0; j-- {
			if results[j].Score > results[j-1].Score ||
				(results[j].Score == results[j-1].Score &&
					results[j].DocID < results[j-1].DocID) {
				results[j], results[j-1] = results[j-1], results[j]
			} else {
				break
			}
		}
	}
}

// ReferenceBM25 computes a BM25 score for a single term in a single document
// using explicit parameters. This is the reference implementation used for
// parity testing.
//
// Parameters:
//   - tf: term frequency in the document
//   - dl: document length (token count)
//   - avgdl: average document length
//   - numDocs: total number of documents
//   - df: document frequency (number of docs containing the term)
func ReferenceBM25(tf, dl, avgdl float64, numDocs, df int) float64 {
	n := float64(numDocs)
	nq := float64(df)
	idf := math.Log((n-nq+0.5)/(nq+0.5) + 1)
	num := tf * (bm25K1 + 1)
	denom := tf + bm25K1*(1-bm25B+bm25B*dl/avgdl)
	return idf * num / denom
}

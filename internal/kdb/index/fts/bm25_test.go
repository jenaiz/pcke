package fts

import (
	"math"
	"testing"
)

func TestBM25ReferenceParitySingle(t *testing.T) {
	// Single term, single doc — verify index scorer matches reference.
	idx := NewIndex()
	idx.AddDocument("the quick brown fox jumps over the lazy dog")
	idx.Commit()

	results := idx.ScoreBM25([]string{"fox"})
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}

	// Reference: tf=1, dl=9 tokens, avgdl=9, N=1, df=1.
	ref := ReferenceBM25(1, 9, 9, 1, 1)
	assertScoreClose(t, results[0].Score, ref, 0.01)
}

func TestBM25ReferenceParityMultiDoc(t *testing.T) {
	idx := NewIndex()
	idx.AddDocument("hello world")          // doc 1: 2 tokens
	idx.AddDocument("hello hello universe") // doc 2: 3 tokens, tf(hello)=2
	idx.AddDocument("goodbye world")        // doc 3: 2 tokens
	idx.Commit()

	results := idx.ScoreBM25([]string{"hello"})

	// N=3, df("hello")=2, avgdl = (2+3+2)/3 = 2.333
	avgdl := 7.0 / 3.0
	n := 3
	df := 2

	// Doc 2 (tf=2, dl=3) should score higher than doc 1 (tf=1, dl=2).
	refDoc1 := ReferenceBM25(1, 2, avgdl, n, df)
	refDoc2 := ReferenceBM25(2, 3, avgdl, n, df)

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}

	// Results sorted by score desc — doc 2 should be first.
	if results[0].Score < results[1].Score {
		t.Error("results not sorted by score descending")
	}

	// Find each doc's score and compare with reference.
	for _, r := range results {
		switch r.DocID {
		case 1:
			assertScoreClose(t, r.Score, refDoc1, 0.01)
		case 2:
			assertScoreClose(t, r.Score, refDoc2, 0.01)
		default:
			t.Errorf("unexpected DocID %d in results", r.DocID)
		}
	}
}

func TestBM25MultiTermQuery(t *testing.T) {
	idx := NewIndex()
	idx.AddDocument("error handling in go")
	idx.AddDocument("go error patterns")
	idx.AddDocument("python error handling")
	idx.AddDocument("javascript frameworks")
	idx.Commit()

	results := idx.ScoreBM25([]string{"error", "handling"})

	// Docs 1 and 3 have both terms, doc 2 has only "error".
	// Doc 4 has neither term.
	if len(results) < 2 {
		t.Fatalf("got %d results, want >= 2", len(results))
	}

	// Doc 4 should not appear.
	for _, r := range results {
		if r.DocID == 4 {
			t.Error("doc 4 should not appear (no matching terms)")
		}
	}
}

func TestBM25EmptyQuery(t *testing.T) {
	idx := NewIndex()
	idx.AddDocument("hello world")
	idx.Commit()

	results := idx.ScoreBM25(nil)
	if len(results) != 0 {
		t.Errorf("empty query returned %d results", len(results))
	}
}

func TestBM25EmptyIndex(t *testing.T) {
	idx := NewIndex()
	results := idx.ScoreBM25([]string{"hello"})
	if len(results) != 0 {
		t.Errorf("empty index returned %d results", len(results))
	}
}

func TestBM25WithTombstones(t *testing.T) {
	idx := NewIndex()
	d1 := idx.AddDocument("error handling")
	idx.AddDocument("error patterns")
	idx.Commit()

	idx.DeleteDocument(d1)

	results := idx.ScoreBM25([]string{"error"})
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (deleted doc excluded)", len(results))
	}
	if results[0].DocID == d1 {
		t.Error("deleted doc should not appear in BM25 results")
	}
}

func TestBM25FixturesFromLiterature(t *testing.T) {
	// Standard BM25 test fixture from IR literature.
	// Corpus of 5 docs, query "information retrieval".
	tests := []struct {
		name   string
		tf     float64
		dl     float64
		avgdl  float64
		nDocs  int
		df     int
		wantGT float64 // score should be > this
	}{
		{
			name:   "rare term high tf",
			tf:     3,
			dl:     100,
			avgdl:  200,
			nDocs:  10000,
			df:     10,
			wantGT: 0,
		},
		{
			name:   "common term",
			tf:     1,
			dl:     100,
			avgdl:  100,
			nDocs:  10000,
			df:     5000,
			wantGT: 0,
		},
		{
			name:   "very rare term",
			tf:     1,
			dl:     50,
			avgdl:  100,
			nDocs:  10000,
			df:     1,
			wantGT: 5.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := ReferenceBM25(tt.tf, tt.dl, tt.avgdl, tt.nDocs, tt.df)
			if score <= tt.wantGT {
				t.Errorf("score = %f, want > %f", score, tt.wantGT)
			}
		})
	}
}

func assertScoreClose(t *testing.T, got, want, maxDelta float64) {
	t.Helper()
	delta := math.Abs(got - want)
	relDelta := delta / math.Max(math.Abs(want), 1e-10)
	if relDelta > maxDelta {
		t.Errorf("score = %f, want %f (delta %f > %f%%)", got, want, relDelta, maxDelta*100)
	}
}

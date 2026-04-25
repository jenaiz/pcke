package fts

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

func TestMemSegmentAddAndFreeze(t *testing.T) {
	ms := NewMemSegment(1)
	ms.AddDocument(10, "hello world")
	ms.AddDocument(20, "hello universe")

	seg := ms.Freeze()
	if seg.ID != 1 {
		t.Errorf("ID = %d, want 1", seg.ID)
	}
	if seg.DocCount != 2 {
		t.Errorf("DocCount = %d, want 2", seg.DocCount)
	}

	// "hello" should appear in both documents.
	postings := seg.Search("hello")
	if len(postings) != 2 {
		t.Fatalf("Search(hello) = %d postings, want 2", len(postings))
	}
	if postings[0].DocID != 10 || postings[1].DocID != 20 {
		t.Errorf("unexpected DocIDs: %d, %d", postings[0].DocID, postings[1].DocID)
	}

	// "world" only in doc 10.
	postings = seg.Search("world")
	if len(postings) != 1 || postings[0].DocID != 10 {
		t.Errorf("Search(world) = %v, want [{DocID:10}]", postings)
	}

	// Unknown term.
	if got := seg.Search("missing"); got != nil {
		t.Errorf("Search(missing) = %v, want nil", got)
	}
}

func TestMemSegmentMultipleFieldsSameDoc(t *testing.T) {
	ms := NewMemSegment(2)
	ms.AddDocument(1, "func parseJSON")
	ms.AddDocument(1, "handles JSON parsing")

	seg := ms.Freeze()
	if seg.DocCount != 1 {
		t.Errorf("DocCount = %d, want 1", seg.DocCount)
	}

	// "json" should appear in both calls for doc 1 → 2 postings entries.
	// The MemSegment creates separate posting entries per AddDocument call.
	postings := seg.Search("json")
	if len(postings) < 1 {
		t.Fatal("no postings for 'json'")
	}

	// Verify norms accumulated.
	norm, ok := seg.DocNorm(1)
	if !ok {
		t.Fatal("no norm for doc 1")
	}
	if norm == 0 {
		t.Error("norm should be > 0")
	}
}

func TestSegmentEncodeDecodeRoundtrip(t *testing.T) {
	ms := NewMemSegment(42)
	ms.AddDocument(1, "hello world")
	ms.AddDocument(2, "hello universe foo bar")
	ms.AddDocument(3, "parseJSON error_code")

	original := ms.Freeze()
	data := original.Encode()

	decoded, err := DecodeSegment(data)
	if err != nil {
		t.Fatalf("DecodeSegment: %v", err)
	}

	assertSegmentsEqual(t, original, decoded)
}

func TestSegmentEncodeDecodeEmpty(t *testing.T) {
	ms := NewMemSegment(99)
	original := ms.Freeze()
	data := original.Encode()

	decoded, err := DecodeSegment(data)
	if err != nil {
		t.Fatalf("DecodeSegment: %v", err)
	}

	if decoded.ID != 99 {
		t.Errorf("ID = %d, want 99", decoded.ID)
	}
	if decoded.DocCount != 0 {
		t.Errorf("DocCount = %d, want 0", decoded.DocCount)
	}
}

func TestDecodeSegmentCorrupted(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"short", []byte{0x01, 0x02}},
		{"bad magic", []byte{0, 0, 0, 0, 1, 0, 0, 0, 0}},
		{"bad version", append(appendUint32LE(nil, segMagic), 99)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeSegment(tt.data)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestSegmentSearch(t *testing.T) {
	ms := NewMemSegment(1)
	for i := range 100 {
		ms.AddDocument(uint64(i), fmt.Sprintf("term_%d common", i))
	}
	seg := ms.Freeze()

	// "common" should be in all 100 docs.
	postings := seg.Search("common")
	if len(postings) != 100 {
		t.Errorf("Search(common) = %d postings, want 100", len(postings))
	}

	// Verify postings are sorted by DocID.
	for i := 1; i < len(postings); i++ {
		if postings[i].DocID <= postings[i-1].DocID {
			t.Errorf("postings not sorted at %d: %d <= %d",
				i, postings[i].DocID, postings[i-1].DocID)
			break
		}
	}
}

func TestSegmentAvgDocLen(t *testing.T) {
	ms := NewMemSegment(1)
	ms.AddDocument(1, "a b c")   // 3 tokens
	ms.AddDocument(2, "d e f g") // 4 tokens
	seg := ms.Freeze()

	avg := seg.AvgDocLen()
	if avg < 3.0 || avg > 4.0 {
		t.Errorf("AvgDocLen = %f, want between 3.0 and 4.0", avg)
	}
}

func TestSegmentPositions(t *testing.T) {
	ms := NewMemSegment(1)
	ms.AddDocument(1, "the quick brown fox jumps over the lazy dog")
	seg := ms.Freeze()

	// "the" appears at positions 0 and 6.
	postings := seg.Search("the")
	if len(postings) != 1 {
		t.Fatalf("Search(the) = %d postings, want 1", len(postings))
	}
	p := postings[0]
	if p.Freq != 2 {
		t.Errorf("Freq = %d, want 2", p.Freq)
	}
	if len(p.Positions) != 2 {
		t.Fatalf("Positions len = %d, want 2", len(p.Positions))
	}
}

func TestEncodeDecodeRoundtripLarge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large roundtrip in short mode")
	}

	ms := NewMemSegment(1000)
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // G404: deterministic seed for reproducibility.

	words := []string{
		"func", "error", "return", "package", "import",
		"struct", "interface", "channel", "goroutine", "context",
		"handler", "request", "response", "middleware", "database",
	}

	for i := range 500 {
		// Random sentence of 5-15 words.
		n := 5 + rng.IntN(11)
		text := ""
		for j := range n {
			if j > 0 {
				text += " "
			}
			text += words[rng.IntN(len(words))]
		}
		ms.AddDocument(uint64(i), text)
	}

	original := ms.Freeze()
	data := original.Encode()

	decoded, err := DecodeSegment(data)
	if err != nil {
		t.Fatalf("DecodeSegment: %v", err)
	}

	assertSegmentsEqual(t, original, decoded)
	t.Logf("500 docs: encoded size = %d bytes, terms = %d", len(data), len(original.Terms))
}

func TestMemSegmentSize(t *testing.T) {
	ms := NewMemSegment(1)
	if ms.Size() != 0 {
		t.Errorf("empty Size = %d, want 0", ms.Size())
	}

	ms.AddDocument(1, "hello world")
	if ms.Size() == 0 {
		t.Error("Size should be > 0 after adding a document")
	}
}

func FuzzSegmentRoundtrip(f *testing.F) {
	f.Add(uint64(1), "hello world")
	f.Add(uint64(0), "")
	f.Add(uint64(100), "camelCase snake_case 日本語テスト")
	f.Add(uint64(42), "func parseJSON(data []byte) error")

	f.Fuzz(func(t *testing.T, docID uint64, text string) {
		ms := NewMemSegment(1)
		ms.AddDocument(docID, text)
		original := ms.Freeze()

		data := original.Encode()
		decoded, err := DecodeSegment(data)
		if err != nil {
			t.Fatalf("DecodeSegment: %v", err)
		}

		if decoded.DocCount != original.DocCount {
			t.Errorf("DocCount = %d, want %d", decoded.DocCount, original.DocCount)
		}
		if decoded.TotalLen != original.TotalLen {
			t.Errorf("TotalLen = %d, want %d", decoded.TotalLen, original.TotalLen)
		}
	})
}

// --- test helpers ---

func assertSegmentsEqual(t *testing.T, a, b *Segment) {
	t.Helper()

	if a.ID != b.ID {
		t.Errorf("ID: %d != %d", a.ID, b.ID)
	}
	if a.DocCount != b.DocCount {
		t.Errorf("DocCount: %d != %d", a.DocCount, b.DocCount)
	}
	if a.TotalLen != b.TotalLen {
		t.Errorf("TotalLen: %d != %d", a.TotalLen, b.TotalLen)
	}

	assertTermsEqual(t, a.Terms, b.Terms)
	assertNormsEqual(t, a.Norms, b.Norms)
}

func assertTermsEqual(t *testing.T, a, b map[string]*PostingList) {
	t.Helper()

	if len(a) != len(b) {
		t.Fatalf("Terms count: %d != %d", len(a), len(b))
	}
	for term, apl := range a {
		bpl, ok := b[term]
		if !ok {
			t.Errorf("term %q missing in decoded segment", term)
			continue
		}
		if len(apl.Postings) != len(bpl.Postings) {
			t.Errorf("term %q: %d postings != %d", term, len(apl.Postings), len(bpl.Postings))
			continue
		}
		for i := range apl.Postings {
			ap, bp := apl.Postings[i], bpl.Postings[i]
			if ap.DocID != bp.DocID || ap.Freq != bp.Freq {
				t.Errorf("term %q posting %d: %+v != %+v", term, i, ap, bp)
			}
			if len(ap.Positions) != len(bp.Positions) {
				t.Errorf("term %q posting %d positions: %v != %v", term, i, ap.Positions, bp.Positions)
			}
		}
	}
}

func assertNormsEqual(t *testing.T, a, b map[uint64]uint32) {
	t.Helper()

	if len(a) != len(b) {
		t.Fatalf("Norms count: %d != %d", len(a), len(b))
	}
	for docID, aNorm := range a {
		bNorm, ok := b[docID]
		if !ok {
			t.Errorf("norm for doc %d missing in decoded segment", docID)
			continue
		}
		if aNorm != bNorm {
			t.Errorf("norm for doc %d: %d != %d", docID, aNorm, bNorm)
		}
	}
}

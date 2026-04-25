package fts

import (
	"math/rand/v2"
	"sort"
	"testing"
)

func TestEncodeDecodePostingsRoundtrip(t *testing.T) {
	postings := []Posting{
		{DocID: 1, Freq: 3, Positions: []uint32{0, 5, 10}},
		{DocID: 5, Freq: 1, Positions: []uint32{7}},
		{DocID: 100, Freq: 2, Positions: []uint32{0, 20}},
		{DocID: 101, Freq: 1, Positions: []uint32{3}},
	}

	data := EncodePostings(postings)
	decoded, err := DecodePostings(data)
	if err != nil {
		t.Fatalf("DecodePostings: %v", err)
	}

	assertPostingsEqual(t, postings, decoded)
}

func TestEncodeDecodePostingsEmpty(t *testing.T) {
	data := EncodePostings(nil)
	decoded, err := DecodePostings(data)
	if err != nil {
		t.Fatalf("DecodePostings: %v", err)
	}
	if len(decoded) != 0 {
		t.Errorf("got %d postings, want 0", len(decoded))
	}
}

func TestEncodeDecodePostingsSingle(t *testing.T) {
	postings := []Posting{
		{DocID: 42, Freq: 1, Positions: []uint32{0}},
	}

	data := EncodePostings(postings)
	decoded, err := DecodePostings(data)
	if err != nil {
		t.Fatalf("DecodePostings: %v", err)
	}

	assertPostingsEqual(t, postings, decoded)
}

func TestPostingCompressionRatio(t *testing.T) {
	// Simulate a realistic posting list: 1000 docs with small gaps.
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // G404: deterministic seed.
	var postings []Posting
	docID := uint64(0)
	for range 1000 {
		docID += uint64(1 + rng.IntN(10)) //nolint:gosec // G115: small positive int.
		freq := uint32(1 + rng.IntN(5))   //nolint:gosec // G115: small freq.
		positions := make([]uint32, freq)
		pos := uint32(0)
		for j := range positions {
			pos += uint32(1 + rng.IntN(20)) //nolint:gosec // G115: small gaps.
			positions[j] = pos
		}
		postings = append(postings, Posting{
			DocID:     docID,
			Freq:      freq,
			Positions: positions,
		})
	}

	data := EncodePostings(postings)
	rawSize := RawPostingSize(postings)
	ratio := float64(len(data)) / float64(rawSize)

	t.Logf("1000 postings: raw=%d bytes, encoded=%d bytes, ratio=%.2f",
		rawSize, len(data), ratio)

	// Delta+varint encoding should achieve at least 2x compression.
	if ratio > 0.5 {
		t.Errorf("compression ratio %.2f > 0.5; expected at least 2x compression", ratio)
	}

	// Verify roundtrip.
	decoded, err := DecodePostings(data)
	if err != nil {
		t.Fatalf("DecodePostings: %v", err)
	}
	assertPostingsEqual(t, postings, decoded)
}

func TestEncodeDecodePostingsLargeDocIDs(t *testing.T) {
	postings := []Posting{
		{DocID: 1_000_000, Freq: 1, Positions: []uint32{0}},
		{DocID: 1_000_001, Freq: 1, Positions: []uint32{5}},
		{DocID: 2_000_000, Freq: 2, Positions: []uint32{0, 100}},
	}

	data := EncodePostings(postings)
	decoded, err := DecodePostings(data)
	if err != nil {
		t.Fatalf("DecodePostings: %v", err)
	}
	assertPostingsEqual(t, postings, decoded)
}

func TestEncodeDecodePostingsNoPositions(t *testing.T) {
	postings := []Posting{
		{DocID: 1, Freq: 0, Positions: nil},
		{DocID: 2, Freq: 0, Positions: []uint32{}},
	}

	data := EncodePostings(postings)
	decoded, err := DecodePostings(data)
	if err != nil {
		t.Fatalf("DecodePostings: %v", err)
	}

	if len(decoded) != 2 {
		t.Fatalf("got %d postings, want 2", len(decoded))
	}
}

func FuzzPostingRoundtrip(f *testing.F) {
	f.Add(uint64(1), uint32(1), uint32(0))
	f.Add(uint64(100), uint32(5), uint32(10))
	f.Add(uint64(0), uint32(0), uint32(0))

	f.Fuzz(func(t *testing.T, docID uint64, freq uint32, pos uint32) {
		postings := []Posting{
			{DocID: docID, Freq: freq, Positions: []uint32{pos}},
		}
		data := EncodePostings(postings)
		decoded, err := DecodePostings(data)
		if err != nil {
			t.Fatalf("DecodePostings: %v", err)
		}
		if len(decoded) != 1 {
			t.Fatalf("got %d postings, want 1", len(decoded))
		}
		if decoded[0].DocID != docID {
			t.Errorf("DocID = %d, want %d", decoded[0].DocID, docID)
		}
		if decoded[0].Freq != freq {
			t.Errorf("Freq = %d, want %d", decoded[0].Freq, freq)
		}
	})
}

func assertPostingsEqual(t *testing.T, a, b []Posting) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("len: %d != %d", len(a), len(b))
	}
	for i := range a {
		if a[i].DocID != b[i].DocID {
			t.Errorf("[%d] DocID: %d != %d", i, a[i].DocID, b[i].DocID)
		}
		if a[i].Freq != b[i].Freq {
			t.Errorf("[%d] Freq: %d != %d", i, a[i].Freq, b[i].Freq)
		}
		ap := a[i].Positions
		bp := b[i].Positions
		if len(ap) == 0 && len(bp) == 0 {
			continue
		}
		sort.Slice(ap, func(x, y int) bool { return ap[x] < ap[y] })
		sort.Slice(bp, func(x, y int) bool { return bp[x] < bp[y] })
		if len(ap) != len(bp) {
			t.Errorf("[%d] Positions len: %d != %d", i, len(ap), len(bp))
			continue
		}
		for j := range ap {
			if ap[j] != bp[j] {
				t.Errorf("[%d] Position[%d]: %d != %d", i, j, ap[j], bp[j])
			}
		}
	}
}

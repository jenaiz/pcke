package fts

import (
	"fmt"

	enc "github.com/jenaiz/pcke/internal/kdb/encoding"
)

// Posting encoding for on-disk segment format.
//
// Doc IDs are delta-encoded (gaps stored as varints). Term frequencies
// and positions are also delta-encoded as varints. This achieves good
// compression for the typical case where doc IDs are clustered and
// frequencies/positions are small.
//
// Phase 1 — Task F1.T6.

// EncodePostings encodes a sorted posting list into a compact byte slice.
//
// Format:
//
//	[uvarint count]
//	for each posting (sorted by DocID):
//	  [uvarint delta_docID]     // gap from previous docID
//	  [uvarint freq]            // term frequency
//	  [uvarint posCount]
//	  for each position:
//	    [uvarint delta_pos]     // gap from previous position
func EncodePostings(postings []Posting) []byte {
	buf := make([]byte, 0, len(postings)*8)
	buf = enc.AppendUvarint(buf, uint64(len(postings)))

	var prevDocID uint64
	for _, p := range postings {
		delta := p.DocID - prevDocID
		buf = enc.AppendUvarint(buf, delta)
		prevDocID = p.DocID

		buf = enc.AppendUvarint(buf, uint64(p.Freq))

		buf = enc.AppendUvarint(buf, uint64(len(p.Positions)))
		var prevPos uint32
		for _, pos := range p.Positions {
			buf = enc.AppendUvarint(buf, uint64(pos-prevPos))
			prevPos = pos
		}
	}

	return buf
}

// DecodePostings decodes a posting list from bytes produced by [EncodePostings].
func DecodePostings(data []byte) ([]Posting, error) {
	d := &decoder{buf: data}
	count := d.uvarint()
	if d.err != nil {
		return nil, fmt.Errorf("kdb/fts: decode posting count: %w", d.err)
	}

	postings := make([]Posting, count)
	var prevDocID uint64

	for i := range postings {
		delta := d.uvarint()
		prevDocID += delta
		postings[i].DocID = prevDocID

		postings[i].Freq = uint32(d.uvarint()) //nolint:gosec // G115: freq encoded from uint32.

		posCount := d.uvarint()
		if d.err != nil {
			return nil, fmt.Errorf("kdb/fts: decode posting %d: %w", i, d.err)
		}

		positions := make([]uint32, posCount)
		var prevPos uint32
		for j := range positions {
			delta := d.uvarint()
			prevPos += uint32(delta) //nolint:gosec // G115: position fits in uint32.
			positions[j] = prevPos
		}
		postings[i].Positions = positions
	}

	if d.err != nil {
		return nil, fmt.Errorf("kdb/fts: decode postings: %w", d.err)
	}

	return postings, nil
}

// RawPostingSize returns the uncompressed size in bytes if each posting were
// stored as fixed-width fields: 8 (docID) + 4 (freq) + 4*len(positions).
// Used to measure compression ratio.
func RawPostingSize(postings []Posting) int {
	size := 0
	for _, p := range postings {
		size += 8 + 4 + 4*len(p.Positions)
	}
	return size
}

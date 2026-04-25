package fts

import (
	"errors"
	"fmt"
	"sort"

	enc "github.com/jenaiz/pcke/internal/kdb/encoding"
)

// Segment format version and magic bytes.
const (
	segMagic   = 0x46545301 // "FTS\x01"
	segVersion = 1
)

// Errors for segment encoding/decoding.
var (
	// ErrSegmentCorrupted indicates that a FTS segment failed integrity
	// verification during decode. The caller should trigger a full reindex.
	ErrSegmentCorrupted = errors.New("kdb/fts: segment corrupted")
)

// Posting represents a single occurrence of a term in a document.
type Posting struct {
	DocID     uint64
	Freq      uint32
	Positions []uint32
}

// PostingList is a sorted list of postings for a single term.
//
// Postings are sorted by DocID. The tiered merge strategy creates multiple
// small segments on each commit and periodically merges them into larger
// ones at checkpoint. This amortizes write cost while keeping read
// amplification bounded.
type PostingList struct {
	Postings []Posting
}

// Segment represents an immutable inverted index fragment.
//
// Each committed write transaction produces one frozen Segment from its
// [MemSegment]. Segments are later merged at checkpoint boundaries (F1.T8).
// A query fans out across all live segments and merges results by score.
//
// Concurrency: Segment is immutable after creation and safe for concurrent
// read access. It must not be modified after [MemSegment.Freeze] returns.
type Segment struct {
	ID       uint64
	Terms    map[string]*PostingList
	Norms    map[uint64]uint32 // docID → field length
	DocCount uint32
	TotalLen uint64
}

// Search returns all postings for the given term, or nil if the term
// does not appear in this segment.
func (s *Segment) Search(term string) []Posting {
	pl, ok := s.Terms[term]
	if !ok {
		return nil
	}
	return pl.Postings
}

// DocNorm returns the field length for a document in this segment.
func (s *Segment) DocNorm(docID uint64) (uint32, bool) {
	n, ok := s.Norms[docID]
	return n, ok
}

// AvgDocLen returns the average document length across this segment.
// Returns 0 if the segment is empty.
func (s *Segment) AvgDocLen() float64 {
	if s.DocCount == 0 {
		return 0
	}
	return float64(s.TotalLen) / float64(s.DocCount)
}

// Encode serializes the segment to a self-contained byte slice.
//
// Format (v1):
//
//	[4B magic][1B version]
//	[uvarint segID][uvarint docCount][uvarint totalLen]
//	[uvarint termCount]
//	  for each term (sorted by term string):
//	    [uvarint termLen][termBytes]
//	    [uvarint postingCount]
//	      for each posting (sorted by docID):
//	        [uvarint docID][uvarint freq]
//	        [uvarint posCount][uvarint pos...]
//	[uvarint normCount]
//	  for each norm (sorted by docID):
//	    [uvarint docID][uvarint fieldLen]
//
// This is a simple format for Phase 1. F1.T6 adds delta+gamma
// compression for posting lists.
func (s *Segment) Encode() []byte {
	// Pre-allocate a reasonable buffer.
	buf := make([]byte, 0, 4096)

	// Header.
	buf = appendUint32LE(buf, segMagic)
	buf = append(buf, segVersion)

	// Metadata.
	buf = enc.AppendUvarint(buf, s.ID)
	buf = enc.AppendUvarint(buf, uint64(s.DocCount))
	buf = enc.AppendUvarint(buf, s.TotalLen)

	// Terms — sorted for deterministic output.
	termKeys := sortedTermKeys(s.Terms)
	buf = enc.AppendUvarint(buf, uint64(len(termKeys)))

	for _, term := range termKeys {
		pl := s.Terms[term]
		buf = appendString(buf, term)
		buf = enc.AppendUvarint(buf, uint64(len(pl.Postings)))
		for _, p := range pl.Postings {
			buf = enc.AppendUvarint(buf, p.DocID)
			buf = enc.AppendUvarint(buf, uint64(p.Freq))
			buf = enc.AppendUvarint(buf, uint64(len(p.Positions)))
			for _, pos := range p.Positions {
				buf = enc.AppendUvarint(buf, uint64(pos))
			}
		}
	}

	// Norms — sorted by docID.
	normIDs := sortedNormKeys(s.Norms)
	buf = enc.AppendUvarint(buf, uint64(len(normIDs)))
	for _, docID := range normIDs {
		buf = enc.AppendUvarint(buf, docID)
		buf = enc.AppendUvarint(buf, uint64(s.Norms[docID]))
	}

	return buf
}

// DecodeSegment deserializes a segment from bytes produced by [Segment.Encode].
func DecodeSegment(data []byte) (*Segment, error) {
	if len(data) < 5 {
		return nil, ErrSegmentCorrupted
	}

	d := &decoder{buf: data}

	// Header.
	magic := d.uint32LE()
	version := d.byte()
	if d.err != nil || magic != segMagic || version != segVersion {
		return nil, fmt.Errorf("%w: bad header", ErrSegmentCorrupted)
	}

	// Metadata.
	segID := d.uvarint()
	docCount := d.uvarint()
	totalLen := d.uvarint()
	if d.err != nil {
		return nil, fmt.Errorf("%w: metadata: %v", ErrSegmentCorrupted, d.err)
	}

	// Terms.
	termCount := d.uvarint()
	if d.err != nil {
		return nil, fmt.Errorf("%w: term count: %v", ErrSegmentCorrupted, d.err)
	}

	terms := make(map[string]*PostingList, termCount)
	for range termCount {
		term := d.string()
		postingCount := d.uvarint()
		if d.err != nil {
			return nil, fmt.Errorf("%w: term header: %v", ErrSegmentCorrupted, d.err)
		}
		postings := make([]Posting, postingCount)
		for j := range postings {
			postings[j] = d.posting()
		}
		if d.err != nil {
			return nil, fmt.Errorf("%w: postings for %q: %v", ErrSegmentCorrupted, term, d.err)
		}
		terms[term] = &PostingList{Postings: postings}
	}

	// Norms.
	normCount := d.uvarint()
	if d.err != nil {
		return nil, fmt.Errorf("%w: norm count: %v", ErrSegmentCorrupted, d.err)
	}

	norms := make(map[uint64]uint32, normCount)
	for range normCount {
		docID := d.uvarint()
		fieldLen := d.uvarint()
		if d.err != nil {
			return nil, fmt.Errorf("%w: norms: %v", ErrSegmentCorrupted, d.err)
		}
		norms[docID] = uint32(fieldLen) //nolint:gosec // G115: encoded from uint32 originally.
	}

	return &Segment{
		ID:       segID,
		Terms:    terms,
		Norms:    norms,
		DocCount: uint32(docCount), //nolint:gosec // G115: encoded from uint32 originally.
		TotalLen: totalLen,
	}, nil
}

// --- helpers ---

func appendUint32LE(buf []byte, v uint32) []byte {
	return append(buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24)) //nolint:gosec // G115: deliberate LE encoding.
}

func appendString(buf []byte, s string) []byte {
	buf = enc.AppendUvarint(buf, uint64(len(s)))
	return append(buf, s...)
}

func sortedTermKeys(m map[string]*PostingList) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedNormKeys(m map[uint64]uint32) []uint64 {
	keys := make([]uint64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// decoder is a simple cursor over a byte slice for decoding.
type decoder struct {
	buf []byte
	off int
	err error
}

func (d *decoder) uint32LE() uint32 {
	if d.err != nil {
		return 0
	}
	if d.off+4 > len(d.buf) {
		d.err = ErrSegmentCorrupted
		return 0
	}
	v := uint32(d.buf[d.off]) |
		uint32(d.buf[d.off+1])<<8 |
		uint32(d.buf[d.off+2])<<16 |
		uint32(d.buf[d.off+3])<<24
	d.off += 4
	return v
}

func (d *decoder) byte() byte {
	if d.err != nil {
		return 0
	}
	if d.off >= len(d.buf) {
		d.err = ErrSegmentCorrupted
		return 0
	}
	v := d.buf[d.off]
	d.off++
	return v
}

func (d *decoder) uvarint() uint64 {
	if d.err != nil {
		return 0
	}
	v, n := enc.Uvarint(d.buf[d.off:])
	if n <= 0 {
		d.err = ErrSegmentCorrupted
		return 0
	}
	d.off += n
	return v
}

func (d *decoder) string() string {
	length := d.uvarint()
	if d.err != nil {
		return ""
	}
	end := d.off + int(length) //nolint:gosec // G115: length validated by buffer bounds check below.
	if end > len(d.buf) || end < d.off {
		d.err = ErrSegmentCorrupted
		return ""
	}
	s := string(d.buf[d.off:end])
	d.off = end
	return s
}

func (d *decoder) posting() Posting {
	docID := d.uvarint()
	freq := d.uvarint()
	posCount := d.uvarint()
	positions := make([]uint32, posCount)
	for i := range positions {
		positions[i] = uint32(d.uvarint()) //nolint:gosec // G115: encoded from uint32 originally.
	}
	return Posting{
		DocID:     docID,
		Freq:      uint32(freq), //nolint:gosec // G115: encoded from uint32 originally.
		Positions: positions,
	}
}

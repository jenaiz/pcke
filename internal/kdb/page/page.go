// Package page defines the on-disk page format for kdb.
//
// Every page is exactly 4096 bytes with a 24-byte header. The header layout:
//
//	Offset  Size  Field
//	------  ----  -----
//	 0       4    Magic    0x4B444250 ("KDBP")
//	 4       1    Type     PageType enum
//	 5       1    Flags    reserved (must be 0)
//	 6       4    Checksum CRC32C of [0..6) ++ [10..4096)
//	10       8    LSN      log sequence number (uint64 LE)
//	18       6    Reserved (must be 0)
//
// The checksum covers the entire page with the 4-byte checksum field zeroed.
// Usable data starts at offset 24 (HeaderSize) and is 4072 bytes.
//
// Phase 0 — Task T2.
package page

import (
	"errors"
	"fmt"

	"github.com/jenaiz/pcke/internal/kdb/encoding"
)

// ErrChecksumMismatch indicates a CRC32C verification failed on a page.
var ErrChecksumMismatch = errors.New("kdb: page checksum mismatch")

const (
	// Size is the fixed page size in bytes.
	Size = 4096

	// HeaderSize is the size of the page header in bytes.
	HeaderSize = 24

	// UsableSize is the number of bytes available for data after the header.
	UsableSize = Size - HeaderSize

	// Magic is the 4-byte magic number written at the start of every page.
	// ASCII "KDBP" = 0x4B444250.
	Magic uint32 = 0x4B444250
)

// Header field offsets within a page.
const (
	offMagic    = 0
	offType     = 4
	offFlags    = 5
	offChecksum = 6
	offLSN      = 10
	offReserved = 18
)

// Type identifies the kind of data stored in a page.
type Type uint8

const (
	// TypeMeta is a database metadata page (double-meta at offsets 0 and 4096).
	TypeMeta Type = iota + 1
	// TypeInternal is a B+tree internal (branch) node page.
	TypeInternal
	// TypeLeaf is a B+tree leaf node page.
	TypeLeaf
	// TypeOverflow stores large values that don't fit in a single leaf page.
	TypeOverflow
	// TypePostingSegment stores FTS posting list segments.
	TypePostingSegment
	// TypeFreelist stores freelist metadata (bootstrap linked-list format).
	TypeFreelist
	// TypeFree marks an unallocated page available for reuse.
	TypeFree
)

// String returns a human-readable name for the page type.
func (t Type) String() string {
	switch t {
	case TypeMeta:
		return "Meta"
	case TypeInternal:
		return "Internal"
	case TypeLeaf:
		return "Leaf"
	case TypeOverflow:
		return "Overflow"
	case TypePostingSegment:
		return "PostingSegment"
	case TypeFreelist:
		return "Freelist"
	case TypeFree:
		return "Free"
	default:
		return fmt.Sprintf("Unknown(%d)", t)
	}
}

// Valid reports whether t is a recognised page type.
func (t Type) Valid() bool {
	return t >= TypeMeta && t <= TypeFree
}

// Init initialises a zeroed 4096-byte page buffer with the given type and LSN.
// It writes the magic number, type, LSN, and computes the CRC32C checksum.
// The buffer must be exactly Size bytes; Init panics otherwise.
func Init(buf []byte, pt Type, lsn uint64) {
	if len(buf) != Size {
		panic(fmt.Sprintf("page.Init: buffer must be %d bytes, got %d", Size, len(buf)))
	}

	// Clear the buffer.
	clear(buf)

	// Magic.
	encoding.PutUint32(buf[offMagic:], Magic)

	// Type.
	buf[offType] = byte(pt)

	// Flags: 0 (already zeroed).

	// LSN.
	encoding.PutUint64(buf[offLSN:], lsn)

	// Checksum (computed with the checksum field zeroed, which it already is).
	cs := computeChecksum(buf)
	encoding.PutUint32(buf[offChecksum:], cs)
}

// SetChecksum recomputes and stores the CRC32C checksum for the page.
// Use this after modifying the data region of an already-initialised page.
func SetChecksum(buf []byte) {
	if len(buf) != Size {
		panic(fmt.Sprintf("page.SetChecksum: buffer must be %d bytes, got %d", Size, len(buf)))
	}
	// Zero the checksum field before computing.
	encoding.PutUint32(buf[offChecksum:], 0)
	cs := computeChecksum(buf)
	encoding.PutUint32(buf[offChecksum:], cs)
}

// Verify checks the magic number and CRC32C checksum of a page.
// Returns nil on success or ErrChecksumMismatch on failure.
func Verify(buf []byte) error {
	if len(buf) != Size {
		return fmt.Errorf("page: buffer must be %d bytes, got %d", Size, len(buf))
	}

	// Check magic.
	if m := encoding.Uint32(buf[offMagic:]); m != Magic {
		return fmt.Errorf("page: bad magic 0x%08X, want 0x%08X: %w", m, Magic, ErrChecksumMismatch)
	}

	// Verify checksum.
	stored := encoding.Uint32(buf[offChecksum:])
	computed := computeChecksumWithZeroedField(buf)

	if stored != computed {
		return fmt.Errorf(
			"page: checksum mismatch: stored 0x%08X, computed 0x%08X: %w",
			stored, computed, ErrChecksumMismatch,
		)
	}

	return nil
}

// GetType reads the page type from the header.
func GetType(buf []byte) Type {
	return Type(buf[offType])
}

// GetFlags reads the flags byte from the header.
func GetFlags(buf []byte) uint8 {
	return buf[offFlags]
}

// GetChecksum reads the stored CRC32C checksum from the header.
func GetChecksum(buf []byte) uint32 {
	return encoding.Uint32(buf[offChecksum:])
}

// GetLSN reads the log sequence number from the header.
func GetLSN(buf []byte) uint64 {
	return encoding.Uint64(buf[offLSN:])
}

// SetLSN writes the log sequence number into the header.
// The caller must call SetChecksum afterwards to update the CRC.
func SetLSN(buf []byte, lsn uint64) {
	encoding.PutUint64(buf[offLSN:], lsn)
}

// Data returns the usable data region of the page (bytes [24..4096)).
func Data(buf []byte) []byte {
	return buf[HeaderSize:]
}

// checksumSize is the width of the checksum field in the header.
const checksumSize = 4

// computeChecksum computes CRC32C over the entire page with the 4-byte
// checksum field treated as zeros. Assumes the field is already zeroed.
func computeChecksum(buf []byte) uint32 {
	return encoding.CRC32C(buf)
}

// computeChecksumWithZeroedField computes the checksum without modifying the
// buffer. It hashes [0..offChecksum) ++ [0,0,0,0] ++ [offChecksum+4..Size).
func computeChecksumWithZeroedField(buf []byte) uint32 {
	crc := encoding.CRC32C(buf[:offChecksum])
	var zeroed [checksumSize]byte
	crc = encoding.UpdateCRC32C(crc, zeroed[:])
	crc = encoding.UpdateCRC32C(crc, buf[offChecksum+checksumSize:])
	return crc
}

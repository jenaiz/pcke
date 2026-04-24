// Package encoding provides the on-disk encoding primitives for kdb.
//
// It implements the record format defined in PRD §4.14 (schema v1):
// little-endian integers, LEB128 varints for lengths, CRC32C checksums,
// and a tagged field encoder/decoder with forward compatibility.
//
// Phase 0 — Task T0.
package encoding

import "errors"

// WireType identifies the on-disk encoding of a field value.
type WireType uint8

const (
	// WireFixed64 encodes uint64, int64, float64, and timestamp as 8 bytes LE.
	WireFixed64 WireType = iota
	// WireBytes encodes string and []byte as varint-length + raw bytes.
	WireBytes
	// WireFixed8 encodes bool as a single byte (0 or 1).
	WireFixed8
	// WireList encodes list<string> as varint-count + repeated (varint-length + raw).
	WireList
)

const (
	// maxWireType is the highest valid wire type value.
	maxWireType = WireList
	// wireTypeBits is the number of bits used for wire type in the tag byte.
	wireTypeBits = 3
	// wireTypeMask isolates the wire type bits.
	wireTypeMask = (1 << wireTypeBits) - 1
	// MaxFieldID is the maximum field ID that fits in a tag byte (5 bits).
	MaxFieldID = 31
)

// Sentinel errors for encoding/decoding.
var (
	// ErrBufferTooSmall indicates the buffer does not have enough space.
	ErrBufferTooSmall = errors.New("encoding: buffer too small")
	// ErrVarintOverflow indicates a varint exceeds 10 bytes.
	ErrVarintOverflow = errors.New("encoding: varint overflow")
	// ErrInvalidWireType indicates an unrecognised wire type in a tag.
	ErrInvalidWireType = errors.New("encoding: invalid wire type")
	// ErrTooManyFields indicates a record has more than 255 fields.
	ErrTooManyFields = errors.New("encoding: too many fields (max 255)")
	// ErrUnexpectedEOF indicates the data ended before a field was fully read.
	ErrUnexpectedEOF = errors.New("encoding: unexpected end of data")
	// ErrNoFieldsRemaining indicates Next was called with no fields left.
	ErrNoFieldsRemaining = errors.New("encoding: no fields remaining")
)

// MakeTag encodes a field ID and wire type into a single tag byte.
// The field ID occupies bits 3–7 and the wire type occupies bits 0–2.
func MakeTag(fieldID uint8, wt WireType) uint8 {
	return (fieldID << wireTypeBits) | uint8(wt)
}

// ParseTag decodes a tag byte into a field ID and wire type.
func ParseTag(tag uint8) (fieldID uint8, wt WireType) {
	return tag >> wireTypeBits, WireType(tag & wireTypeMask)
}

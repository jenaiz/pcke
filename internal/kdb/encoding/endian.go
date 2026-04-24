package encoding

import (
	"encoding/binary"
	"math"
)

// All multi-byte integers on disk are little-endian (PRD §4.3).

// PutUint16 writes v as 2 bytes LE into b.
func PutUint16(b []byte, v uint16) {
	binary.LittleEndian.PutUint16(b, v)
}

// Uint16 reads a uint16 from the first 2 bytes of b in LE order.
func Uint16(b []byte) uint16 {
	return binary.LittleEndian.Uint16(b)
}

// PutUint32 writes v as 4 bytes LE into b.
func PutUint32(b []byte, v uint32) {
	binary.LittleEndian.PutUint32(b, v)
}

// Uint32 reads a uint32 from the first 4 bytes of b in LE order.
func Uint32(b []byte) uint32 {
	return binary.LittleEndian.Uint32(b)
}

// PutUint64 writes v as 8 bytes LE into b.
func PutUint64(b []byte, v uint64) {
	binary.LittleEndian.PutUint64(b, v)
}

// Uint64 reads a uint64 from the first 8 bytes of b in LE order.
func Uint64(b []byte) uint64 {
	return binary.LittleEndian.Uint64(b)
}

// PutInt64 writes v as 8 bytes LE into b.
func PutInt64(b []byte, v int64) {
	binary.LittleEndian.PutUint64(b, uint64(v)) //nolint:gosec // G115: bit reinterpretation, not arithmetic.
}

// Int64 reads an int64 from the first 8 bytes of b in LE order.
func Int64(b []byte) int64 {
	return int64(binary.LittleEndian.Uint64(b)) //nolint:gosec // G115: bit reinterpretation, not arithmetic.
}

// PutFloat64 writes v as 8 bytes LE (IEEE 754) into b.
func PutFloat64(b []byte, v float64) {
	binary.LittleEndian.PutUint64(b, math.Float64bits(v))
}

// Float64 reads a float64 from the first 8 bytes of b in LE order.
func Float64(b []byte) float64 {
	return math.Float64frombits(binary.LittleEndian.Uint64(b))
}

package encoding

import "encoding/binary"

// AppendUvarint appends the unsigned varint encoding of v to b.
func AppendUvarint(b []byte, v uint64) []byte {
	return binary.AppendUvarint(b, v)
}

// PutUvarint encodes v into b as an unsigned varint.
// It returns the number of bytes written.
// b must be large enough; the maximum encoding is 10 bytes.
func PutUvarint(b []byte, v uint64) int {
	return binary.PutUvarint(b, v)
}

// Uvarint decodes an unsigned varint from b.
// It returns the value and the number of bytes consumed.
// If n == 0, b was too small; if n < 0, the value overflows a uint64.
func Uvarint(b []byte) (uint64, int) {
	return binary.Uvarint(b)
}

// UvarintSize returns the number of bytes needed to encode v as an unsigned varint.
func UvarintSize(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

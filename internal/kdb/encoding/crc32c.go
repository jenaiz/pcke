package encoding

import "hash/crc32"

// castagnoliTable is the CRC32C (Castagnoli) polynomial table.
var castagnoliTable = crc32.MakeTable(crc32.Castagnoli)

// CRC32C computes the CRC32C (Castagnoli) checksum of data.
func CRC32C(data []byte) uint32 {
	return crc32.Checksum(data, castagnoliTable)
}

// UpdateCRC32C updates a running CRC32C checksum with additional data.
func UpdateCRC32C(crc uint32, data []byte) uint32 {
	return crc32.Update(crc, castagnoliTable, data)
}

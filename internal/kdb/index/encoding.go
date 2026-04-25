package index // encoding.go — composite key encoding for secondary indexes.

import "bytes"

// EncodeCompositeKey creates a composite key: indexKey + \x00 + primaryKey.
// The indexKey must not contain null bytes.
func EncodeCompositeKey(indexKey, primaryKey []byte) []byte {
	buf := make([]byte, len(indexKey)+1+len(primaryKey))
	copy(buf, indexKey)
	buf[len(indexKey)] = 0
	copy(buf[len(indexKey)+1:], primaryKey)
	return buf
}

// DecodeCompositeKey splits a composite key at the first null byte into
// its indexKey and primaryKey components.
func DecodeCompositeKey(composite []byte) (indexKey, primaryKey []byte) {
	idx := bytes.IndexByte(composite, 0)
	if idx < 0 {
		return composite, nil
	}
	return composite[:idx], composite[idx+1:]
}

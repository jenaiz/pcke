package encoding

import (
	"testing"
)

func TestCRC32CKnownValues(t *testing.T) {
	// "123456789" → CRC32C = 0xE3069283 (well-known test vector).
	data := []byte("123456789")
	want := uint32(0xE3069283)
	if got := CRC32C(data); got != want {
		t.Fatalf("CRC32C(%q) = 0x%08X, want 0x%08X", data, got, want)
	}
}

func TestCRC32CEmpty(t *testing.T) {
	if got := CRC32C(nil); got != 0 {
		t.Fatalf("CRC32C(nil) = 0x%08X, want 0", got)
	}
}

func TestCRC32CUpdateIncremental(t *testing.T) {
	data := []byte("Hello, World!")
	whole := CRC32C(data)
	partial := UpdateCRC32C(0, data[:5])
	partial = UpdateCRC32C(partial, data[5:])
	if partial != whole {
		t.Fatalf("incremental CRC32C = 0x%08X, whole = 0x%08X", partial, whole)
	}
}

func TestCRC32CDetectsCorruption(t *testing.T) {
	data := []byte("some payload data for CRC testing")
	original := CRC32C(data)

	// Flip each byte and verify CRC changes.
	for i := range data {
		corrupted := make([]byte, len(data))
		copy(corrupted, data)
		corrupted[i] ^= 0x01
		if CRC32C(corrupted) == original {
			t.Fatalf("CRC32C did not detect 1-bit flip at byte %d", i)
		}
	}
}

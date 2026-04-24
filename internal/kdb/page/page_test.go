package page_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/encoding"
	"github.com/jenaiz/pcke/internal/kdb/page"
)

func TestInitAndVerify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pt   page.Type
		lsn  uint64
	}{
		{"Meta/LSN0", page.TypeMeta, 0},
		{"Leaf/LSN42", page.TypeLeaf, 42},
		{"Internal/MaxLSN", page.TypeInternal, ^uint64(0)},
		{"Overflow/LSN1000", page.TypeOverflow, 1000},
		{"PostingSegment/LSN1", page.TypePostingSegment, 1},
		{"Freelist/LSN99", page.TypeFreelist, 99},
		{"Free/LSN0", page.TypeFree, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf [page.Size]byte
			page.Init(buf[:], tc.pt, tc.lsn)

			if err := page.Verify(buf[:]); err != nil {
				t.Fatalf("Verify failed on freshly Init'd page: %v", err)
			}

			if got := page.GetType(buf[:]); got != tc.pt {
				t.Errorf("GetType = %v, want %v", got, tc.pt)
			}

			if got := page.GetLSN(buf[:]); got != tc.lsn {
				t.Errorf("GetLSN = %d, want %d", got, tc.lsn)
			}

			if got := page.GetFlags(buf[:]); got != 0 {
				t.Errorf("GetFlags = %d, want 0", got)
			}

			// Magic in first 4 bytes.
			magic := encoding.Uint32(buf[:4])
			if magic != page.Magic {
				t.Errorf("magic = 0x%08X, want 0x%08X", magic, page.Magic)
			}
		})
	}
}

func TestDataRegion(t *testing.T) {
	t.Parallel()

	var buf [page.Size]byte
	page.Init(buf[:], page.TypeLeaf, 1)

	data := page.Data(buf[:])
	if len(data) != page.UsableSize {
		t.Fatalf("Data len = %d, want %d", len(data), page.UsableSize)
	}

	// Write to data region, recompute checksum, verify.
	data[0] = 0xFF
	data[page.UsableSize-1] = 0xAB
	page.SetChecksum(buf[:])

	if err := page.Verify(buf[:]); err != nil {
		t.Fatalf("Verify failed after writing to data region: %v", err)
	}
}

func TestSetLSNAndRecheck(t *testing.T) {
	t.Parallel()

	var buf [page.Size]byte
	page.Init(buf[:], page.TypeMeta, 100)

	page.SetLSN(buf[:], 200)
	page.SetChecksum(buf[:])

	if err := page.Verify(buf[:]); err != nil {
		t.Fatalf("Verify failed after SetLSN: %v", err)
	}

	if got := page.GetLSN(buf[:]); got != 200 {
		t.Errorf("GetLSN = %d, want 200", got)
	}
}

func TestVerifyBadMagic(t *testing.T) {
	t.Parallel()

	var buf [page.Size]byte
	page.Init(buf[:], page.TypeLeaf, 0)

	// Corrupt magic.
	buf[0] = 0x00

	err := page.Verify(buf[:])
	if err == nil {
		t.Fatal("expected error for bad magic, got nil")
	}
	if !errors.Is(err, kdb.ErrChecksumMismatch) {
		t.Errorf("expected ErrChecksumMismatch, got %v", err)
	}
}

func TestVerifyBadSize(t *testing.T) {
	t.Parallel()

	buf := make([]byte, 100)
	err := page.Verify(buf)
	if err == nil {
		t.Fatal("expected error for short buffer, got nil")
	}
}

// TestBitFlipDetection is the property test from the T2 DoD:
// CRC detects corruption at every byte position in the page.
func TestBitFlipDetection(t *testing.T) {
	t.Parallel()

	var buf [page.Size]byte
	page.Init(buf[:], page.TypeLeaf, 42)

	// Write some non-zero data to make the test more meaningful.
	data := page.Data(buf[:])
	for i := range data {
		data[i] = byte(i) //nolint:gosec // G115: test-only truncation.
	}
	page.SetChecksum(buf[:])

	// Sanity: original verifies.
	if err := page.Verify(buf[:]); err != nil {
		t.Fatalf("original page fails Verify: %v", err)
	}

	// Flip each byte (XOR with 0xFF) and check detection.
	var corrupt [page.Size]byte

	for pos := 0; pos < page.Size; pos++ {
		copy(corrupt[:], buf[:])
		corrupt[pos] ^= 0xFF

		err := page.Verify(corrupt[:])
		if err == nil {
			t.Errorf("byte-flip at position %d was not detected", pos)
		}
	}
}

// TestSingleBitFlipDetection tests every single-bit flip across the page.
func TestSingleBitFlipDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping exhaustive single-bit flip test in short mode")
	}
	t.Parallel()

	var buf [page.Size]byte
	page.Init(buf[:], page.TypeInternal, 12345)

	// Fill data region with a pattern.
	data := page.Data(buf[:])
	for i := range data {
		data[i] = byte(i*7 + 13) //nolint:gosec // G115: test-only truncation.
	}
	page.SetChecksum(buf[:])

	var corrupt [page.Size]byte

	for pos := 0; pos < page.Size; pos++ {
		for bit := 0; bit < 8; bit++ {
			copy(corrupt[:], buf[:])
			corrupt[pos] ^= 1 << bit //nolint:gosec // G115: test-only bit shift.

			err := page.Verify(corrupt[:])
			if err == nil {
				t.Errorf("single-bit flip at byte %d, bit %d was not detected", pos, bit)
			}
		}
	}
}

func TestPageTypeString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pt   page.Type
		want string
	}{
		{page.TypeMeta, "Meta"},
		{page.TypeInternal, "Internal"},
		{page.TypeLeaf, "Leaf"},
		{page.TypeOverflow, "Overflow"},
		{page.TypePostingSegment, "PostingSegment"},
		{page.TypeFreelist, "Freelist"},
		{page.TypeFree, "Free"},
		{page.Type(0), "Unknown(0)"},
		{page.Type(99), "Unknown(99)"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := tc.pt.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPageTypeValid(t *testing.T) {
	t.Parallel()

	for pt := page.TypeMeta; pt <= page.TypeFree; pt++ {
		if !pt.Valid() {
			t.Errorf("Type(%d).Valid() = false, want true", pt)
		}
	}

	// Invalid types.
	if page.Type(0).Valid() {
		t.Error("Type(0).Valid() should be false")
	}
	if page.Type(255).Valid() {
		t.Error("Type(255).Valid() should be false")
	}
}

func TestConstants(t *testing.T) {
	t.Parallel()

	if page.Size != 4096 {
		t.Errorf("Size = %d, want 4096", page.Size)
	}
	if page.HeaderSize != 24 {
		t.Errorf("HeaderSize = %d, want 24", page.HeaderSize)
	}
	if page.UsableSize != 4072 {
		t.Errorf("UsableSize = %d, want 4072", page.UsableSize)
	}
	if page.Magic != 0x4B444250 {
		t.Errorf("Magic = 0x%08X, want 0x4B444250", page.Magic)
	}
}

func TestInitPanicsOnWrongSize(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for wrong buffer size")
		}
	}()

	buf := make([]byte, 100)
	page.Init(buf, page.TypeLeaf, 0)
}

func TestSetChecksumPanicsOnWrongSize(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for wrong buffer size")
		}
	}()

	buf := make([]byte, 100)
	page.SetChecksum(buf)
}

// Benchmark checksum computation.
func BenchmarkChecksum(b *testing.B) {
	var buf [page.Size]byte
	page.Init(buf[:], page.TypeLeaf, 1)

	b.ResetTimer()
	for range b.N {
		page.SetChecksum(buf[:])
	}
}

func BenchmarkVerify(b *testing.B) {
	var buf [page.Size]byte
	page.Init(buf[:], page.TypeLeaf, 1)

	b.ResetTimer()
	for range b.N {
		_ = page.Verify(buf[:])
	}
}

func ExampleInit() {
	var buf [page.Size]byte
	page.Init(buf[:], page.TypeLeaf, 42)

	fmt.Println("type:", page.GetType(buf[:]))
	fmt.Println("lsn:", page.GetLSN(buf[:]))
	fmt.Println("verify:", page.Verify(buf[:]))

	// Output:
	// type: Leaf
	// lsn: 42
	// verify: <nil>
}

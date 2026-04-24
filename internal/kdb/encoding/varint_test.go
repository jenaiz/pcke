package encoding

import (
	"testing"
)

func TestUvarintRoundTrip(t *testing.T) {
	cases := []uint64{
		0, 1, 127, 128, 255, 256,
		0xFFFF, 0xFFFFFFFF, 0x7FFFFFFFFFFFFFFF, 0xFFFFFFFFFFFFFFFF,
	}
	var buf [10]byte
	for _, want := range cases {
		n := PutUvarint(buf[:], want)
		got, m := Uvarint(buf[:n])
		if m != n {
			t.Fatalf("Uvarint consumed %d bytes, PutUvarint wrote %d (val=%d)", m, n, want)
		}
		if got != want {
			t.Fatalf("round-trip: got %d, want %d", got, want)
		}
	}
}

func TestAppendUvarint(t *testing.T) {
	b := AppendUvarint(nil, 300)
	v, n := Uvarint(b)
	if v != 300 || n != len(b) {
		t.Fatalf("AppendUvarint(300): got v=%d n=%d len=%d", v, n, len(b))
	}
}

func TestUvarintSize(t *testing.T) {
	cases := []struct {
		val  uint64
		size int
	}{
		{0, 1},
		{127, 1},
		{128, 2},
		{16383, 2},
		{16384, 3},
		{0xFFFFFFFFFFFFFFFF, 10},
	}
	for _, tc := range cases {
		if got := UvarintSize(tc.val); got != tc.size {
			t.Errorf("UvarintSize(%d) = %d, want %d", tc.val, got, tc.size)
		}
	}
}

func TestUvarintEmptyBuffer(t *testing.T) {
	_, n := Uvarint(nil)
	if n != 0 {
		t.Fatalf("Uvarint(nil) n = %d, want 0", n)
	}
}

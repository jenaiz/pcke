package encoding

import (
	"math"
	"testing"
)

func TestEndianUint16(t *testing.T) {
	cases := []uint16{0, 1, 0x00FF, 0xFF00, 0xFFFF}
	var buf [2]byte
	for _, want := range cases {
		PutUint16(buf[:], want)
		if got := Uint16(buf[:]); got != want {
			t.Fatalf("Uint16 round-trip: got %d, want %d", got, want)
		}
	}
}

func TestEndianUint32(t *testing.T) {
	cases := []uint32{0, 1, 0xDEADBEEF, math.MaxUint32}
	var buf [4]byte
	for _, want := range cases {
		PutUint32(buf[:], want)
		if got := Uint32(buf[:]); got != want {
			t.Fatalf("Uint32 round-trip: got 0x%X, want 0x%X", got, want)
		}
	}
}

func TestEndianUint64(t *testing.T) {
	cases := []uint64{0, 1, 0xDEADBEEFCAFEBABE, math.MaxUint64}
	var buf [8]byte
	for _, want := range cases {
		PutUint64(buf[:], want)
		if got := Uint64(buf[:]); got != want {
			t.Fatalf("Uint64 round-trip: got %d, want %d", got, want)
		}
	}
}

func TestEndianInt64(t *testing.T) {
	cases := []int64{0, -1, math.MinInt64, math.MaxInt64, 42}
	var buf [8]byte
	for _, want := range cases {
		PutInt64(buf[:], want)
		if got := Int64(buf[:]); got != want {
			t.Fatalf("Int64 round-trip: got %d, want %d", got, want)
		}
	}
}

func TestEndianFloat64(t *testing.T) {
	cases := []float64{0, -1.5, math.Pi, math.Inf(1), math.Inf(-1), math.SmallestNonzeroFloat64}
	var buf [8]byte
	for _, want := range cases {
		PutFloat64(buf[:], want)
		if got := Float64(buf[:]); got != want {
			t.Fatalf("Float64 round-trip: got %v, want %v", got, want)
		}
	}
}

func TestEndianFloat64NaN(t *testing.T) {
	var buf [8]byte
	PutFloat64(buf[:], math.NaN())
	got := Float64(buf[:])
	if !math.IsNaN(got) {
		t.Fatalf("Float64 NaN round-trip: got %v, want NaN", got)
	}
}

func TestEndianLittleEndianLayout(t *testing.T) {
	// Verify actual byte order: 0x0102030405060708 should be [08 07 06 05 04 03 02 01].
	var buf [8]byte
	PutUint64(buf[:], 0x0102030405060708)
	want := [8]byte{0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01}
	if buf != want {
		t.Fatalf("byte order: got %X, want %X", buf, want)
	}
}

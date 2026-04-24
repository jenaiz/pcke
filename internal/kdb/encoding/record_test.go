package encoding

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func TestRecordAllTypesRoundTrip(t *testing.T) {
	const version = 1

	enc := NewEncoder(version)
	enc.PutUint64(0, 42)
	enc.PutInt64(1, -99)
	enc.PutFloat64(2, math.Pi)
	enc.PutString(3, "hello")
	enc.PutBytes(4, []byte{0xDE, 0xAD})
	enc.PutBool(5, true)
	enc.PutTimestamp(6, 1714000000000000000)
	enc.PutStringList(7, []string{"alpha", "beta", "gamma"})

	data := enc.Bytes()

	dec, err := NewDecoder(data)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Version() != version {
		t.Fatalf("version: got %d, want %d", dec.Version(), version)
	}
	if dec.FieldCount() != 8 {
		t.Fatalf("field count: got %d, want 8", dec.FieldCount())
	}

	// Field 0: uint64
	readTag(t, dec, 0, WireFixed64)
	u, err := dec.Uint64()
	requireNoError(t, err)
	if u != 42 {
		t.Fatalf("uint64: got %d, want 42", u)
	}

	// Field 1: int64
	readTag(t, dec, 1, WireFixed64)
	i, err := dec.Int64()
	requireNoError(t, err)
	if i != -99 {
		t.Fatalf("int64: got %d, want -99", i)
	}

	// Field 2: float64
	readTag(t, dec, 2, WireFixed64)
	f, err := dec.Float64()
	requireNoError(t, err)
	if f != math.Pi {
		t.Fatalf("float64: got %v, want %v", f, math.Pi)
	}

	// Field 3: string
	readTag(t, dec, 3, WireBytes)
	s, err := dec.String()
	requireNoError(t, err)
	if s != "hello" {
		t.Fatalf("string: got %q, want %q", s, "hello")
	}

	// Field 4: bytes
	readTag(t, dec, 4, WireBytes)
	b, err := dec.Bytes()
	requireNoError(t, err)
	if !bytes.Equal(b, []byte{0xDE, 0xAD}) {
		t.Fatalf("bytes: got %X, want DEAD", b)
	}

	// Field 5: bool
	readTag(t, dec, 5, WireFixed8)
	bo, err := dec.Bool()
	requireNoError(t, err)
	if !bo {
		t.Fatal("bool: got false, want true")
	}

	// Field 6: timestamp
	readTag(t, dec, 6, WireFixed64)
	ts, err := dec.Timestamp()
	requireNoError(t, err)
	if ts != 1714000000000000000 {
		t.Fatalf("timestamp: got %d, want 1714000000000000000", ts)
	}

	// Field 7: string list
	readTag(t, dec, 7, WireList)
	sl, err := dec.StringList()
	requireNoError(t, err)
	want := []string{"alpha", "beta", "gamma"}
	if len(sl) != len(want) {
		t.Fatalf("string list len: got %d, want %d", len(sl), len(want))
	}
	for idx := range want {
		if sl[idx] != want[idx] {
			t.Fatalf("string list[%d]: got %q, want %q", idx, sl[idx], want[idx])
		}
	}

	if !dec.Done() {
		t.Fatal("decoder should be done")
	}
}

func TestRecordUint64EdgeCases(t *testing.T) {
	cases := []uint64{0, 1, math.MaxUint32, math.MaxUint64}
	for _, want := range cases {
		enc := NewEncoder(1)
		enc.PutUint64(0, want)
		dec, err := NewDecoder(enc.Bytes())
		requireNoError(t, err)
		readTag(t, dec, 0, WireFixed64)
		got, err := dec.Uint64()
		requireNoError(t, err)
		if got != want {
			t.Fatalf("uint64 round-trip: got %d, want %d", got, want)
		}
	}
}

func TestRecordInt64EdgeCases(t *testing.T) {
	cases := []int64{0, -1, 1, math.MinInt64, math.MaxInt64}
	for _, want := range cases {
		enc := NewEncoder(1)
		enc.PutInt64(0, want)
		dec, err := NewDecoder(enc.Bytes())
		requireNoError(t, err)
		readTag(t, dec, 0, WireFixed64)
		got, err := dec.Int64()
		requireNoError(t, err)
		if got != want {
			t.Fatalf("int64 round-trip: got %d, want %d", got, want)
		}
	}
}

func TestRecordFloat64EdgeCases(t *testing.T) {
	cases := []float64{0, -0, math.Inf(1), math.Inf(-1), math.SmallestNonzeroFloat64, math.MaxFloat64}
	for _, want := range cases {
		enc := NewEncoder(1)
		enc.PutFloat64(0, want)
		dec, err := NewDecoder(enc.Bytes())
		requireNoError(t, err)
		readTag(t, dec, 0, WireFixed64)
		got, err := dec.Float64()
		requireNoError(t, err)
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("float64 round-trip: got %v (bits %x), want %v (bits %x)",
				got, math.Float64bits(got), want, math.Float64bits(want))
		}
	}
}

func TestRecordFloat64NaN(t *testing.T) {
	enc := NewEncoder(1)
	enc.PutFloat64(0, math.NaN())
	dec, err := NewDecoder(enc.Bytes())
	requireNoError(t, err)
	readTag(t, dec, 0, WireFixed64)
	got, err := dec.Float64()
	requireNoError(t, err)
	if !math.IsNaN(got) {
		t.Fatalf("NaN round-trip: got %v, want NaN", got)
	}
}

func TestRecordStringEdgeCases(t *testing.T) {
	cases := []string{"", "a", "hello world", "\x00\xff binary"}
	for _, want := range cases {
		enc := NewEncoder(1)
		enc.PutString(0, want)
		dec, err := NewDecoder(enc.Bytes())
		requireNoError(t, err)
		readTag(t, dec, 0, WireBytes)
		got, err := dec.String()
		requireNoError(t, err)
		if got != want {
			t.Fatalf("string round-trip: got %q, want %q", got, want)
		}
	}
}

func TestRecordBytesNilAndEmpty(t *testing.T) {
	// nil bytes encodes as length 0.
	enc := NewEncoder(1)
	enc.PutBytes(0, nil)
	enc.PutBytes(1, []byte{})
	dec, err := NewDecoder(enc.Bytes())
	requireNoError(t, err)

	readTag(t, dec, 0, WireBytes)
	b, err := dec.Bytes()
	requireNoError(t, err)
	if len(b) != 0 {
		t.Fatalf("nil bytes: got len %d, want 0", len(b))
	}

	readTag(t, dec, 1, WireBytes)
	b, err = dec.Bytes()
	requireNoError(t, err)
	if len(b) != 0 {
		t.Fatalf("empty bytes: got len %d, want 0", len(b))
	}
}

func TestRecordBoolRoundTrip(t *testing.T) {
	enc := NewEncoder(1)
	enc.PutBool(0, true)
	enc.PutBool(1, false)
	dec, err := NewDecoder(enc.Bytes())
	requireNoError(t, err)

	readTag(t, dec, 0, WireFixed8)
	v, err := dec.Bool()
	requireNoError(t, err)
	if !v {
		t.Fatal("bool: got false, want true")
	}

	readTag(t, dec, 1, WireFixed8)
	v, err = dec.Bool()
	requireNoError(t, err)
	if v {
		t.Fatal("bool: got true, want false")
	}
}

func TestRecordStringListEdgeCases(t *testing.T) {
	cases := [][]string{
		nil,
		{},
		{"one"},
		{"", ""},
		{"alpha", "beta", "gamma", "delta"},
	}
	for _, want := range cases {
		enc := NewEncoder(1)
		enc.PutStringList(0, want)
		dec, err := NewDecoder(enc.Bytes())
		requireNoError(t, err)
		readTag(t, dec, 0, WireList)
		got, err := dec.StringList()
		requireNoError(t, err)
		if len(got) != len(want) {
			t.Fatalf("string list len: got %d, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("string list[%d]: got %q, want %q", i, got[i], want[i])
			}
		}
	}
}

func TestRecordSkipForwardCompat(t *testing.T) {
	// Encode 3 fields, skip the middle one.
	enc := NewEncoder(1)
	enc.PutUint64(0, 100)
	enc.PutString(1, "skip me")
	enc.PutBool(2, true)

	dec, err := NewDecoder(enc.Bytes())
	requireNoError(t, err)

	// Read field 0.
	readTag(t, dec, 0, WireFixed64)
	u, err := dec.Uint64()
	requireNoError(t, err)
	if u != 100 {
		t.Fatalf("field 0: got %d, want 100", u)
	}

	// Skip field 1 (unknown to this "schema version").
	fid, wt, err := dec.Next()
	requireNoError(t, err)
	if fid != 1 || wt != WireBytes {
		t.Fatalf("field 1: got id=%d wt=%d", fid, wt)
	}
	requireNoError(t, dec.Skip(wt))

	// Read field 2.
	readTag(t, dec, 2, WireFixed8)
	bo, err := dec.Bool()
	requireNoError(t, err)
	if !bo {
		t.Fatal("field 2: got false, want true")
	}
	if !dec.Done() {
		t.Fatal("decoder should be done")
	}
}

func TestRecordSkipAllTypes(t *testing.T) {
	enc := NewEncoder(1)
	enc.PutUint64(0, 42)
	enc.PutString(1, "test")
	enc.PutBool(2, true)
	enc.PutStringList(3, []string{"a", "b"})

	dec, err := NewDecoder(enc.Bytes())
	requireNoError(t, err)

	for !dec.Done() {
		_, wt, err := dec.Next()
		requireNoError(t, err)
		requireNoError(t, dec.Skip(wt))
	}
}

func TestRecordEncoderReset(t *testing.T) {
	enc := NewEncoder(1)
	enc.PutUint64(0, 42)
	data1 := append([]byte(nil), enc.Bytes()...)

	enc.Reset(2)
	enc.PutString(0, "hello")
	data2 := enc.Bytes()

	// data2 should be a different record with version 2.
	dec, err := NewDecoder(data2)
	requireNoError(t, err)
	if dec.Version() != 2 {
		t.Fatalf("version after reset: got %d, want 2", dec.Version())
	}
	if dec.FieldCount() != 1 {
		t.Fatalf("field count after reset: got %d, want 1", dec.FieldCount())
	}

	// Original data should still be version 1.
	dec1, err := NewDecoder(data1)
	requireNoError(t, err)
	if dec1.Version() != 1 {
		t.Fatalf("original version: got %d, want 1", dec1.Version())
	}
}

func TestDecoderErrors(t *testing.T) {
	t.Run("empty data", func(t *testing.T) {
		_, err := NewDecoder(nil)
		if !errors.Is(err, ErrUnexpectedEOF) {
			t.Fatalf("got %v, want ErrUnexpectedEOF", err)
		}
	})
	t.Run("one byte", func(t *testing.T) {
		_, err := NewDecoder([]byte{1})
		if !errors.Is(err, ErrUnexpectedEOF) {
			t.Fatalf("got %v, want ErrUnexpectedEOF", err)
		}
	})
	t.Run("no fields remaining", func(t *testing.T) {
		dec, err := NewDecoder([]byte{1, 0})
		requireNoError(t, err)
		_, _, err = dec.Next()
		if !errors.Is(err, ErrNoFieldsRemaining) {
			t.Fatalf("got %v, want ErrNoFieldsRemaining", err)
		}
	})
	t.Run("truncated fixed64", func(t *testing.T) {
		// Header says 1 field, tag is WireFixed64, but only 3 bytes of data.
		data := []byte{1, 1, MakeTag(0, WireFixed64), 0x01, 0x02, 0x03}
		dec, err := NewDecoder(data)
		requireNoError(t, err)
		_, _, err = dec.Next()
		requireNoError(t, err)
		_, err = dec.Uint64()
		if !errors.Is(err, ErrUnexpectedEOF) {
			t.Fatalf("got %v, want ErrUnexpectedEOF", err)
		}
	})
	t.Run("truncated bytes length", func(t *testing.T) {
		// Tag says WireBytes but no length varint follows.
		data := []byte{1, 1, MakeTag(0, WireBytes)}
		dec, err := NewDecoder(data)
		requireNoError(t, err)
		_, _, err = dec.Next()
		requireNoError(t, err)
		_, err = dec.Bytes()
		if !errors.Is(err, ErrUnexpectedEOF) {
			t.Fatalf("got %v, want ErrUnexpectedEOF", err)
		}
	})
	t.Run("truncated bytes payload", func(t *testing.T) {
		// Length says 10, but only 2 bytes follow.
		data := []byte{1, 1, MakeTag(0, WireBytes), 10, 0x01, 0x02}
		dec, err := NewDecoder(data)
		requireNoError(t, err)
		_, _, err = dec.Next()
		requireNoError(t, err)
		_, err = dec.Bytes()
		if !errors.Is(err, ErrUnexpectedEOF) {
			t.Fatalf("got %v, want ErrUnexpectedEOF", err)
		}
	})
	t.Run("truncated bool", func(t *testing.T) {
		data := []byte{1, 1, MakeTag(0, WireFixed8)}
		dec, err := NewDecoder(data)
		requireNoError(t, err)
		_, _, err = dec.Next()
		requireNoError(t, err)
		_, err = dec.Bool()
		if !errors.Is(err, ErrUnexpectedEOF) {
			t.Fatalf("got %v, want ErrUnexpectedEOF", err)
		}
	})
	t.Run("field count mismatch", func(t *testing.T) {
		// Header says 5 fields, but only 1 is present → Next fails on 2nd.
		enc := NewEncoder(1)
		enc.PutBool(0, true)
		data := enc.Bytes()
		data[1] = 5 // lie about field count

		dec, err := NewDecoder(data)
		requireNoError(t, err)
		_, _, err = dec.Next()
		requireNoError(t, err)
		_, err = dec.Bool()
		requireNoError(t, err)
		_, _, err = dec.Next()
		if !errors.Is(err, ErrUnexpectedEOF) {
			t.Fatalf("got %v, want ErrUnexpectedEOF", err)
		}
	})
}

func TestRecordMaxFieldID(t *testing.T) {
	enc := NewEncoder(1)
	enc.PutUint64(MaxFieldID, 999)
	dec, err := NewDecoder(enc.Bytes())
	requireNoError(t, err)
	readTag(t, dec, MaxFieldID, WireFixed64)
	v, err := dec.Uint64()
	requireNoError(t, err)
	if v != 999 {
		t.Fatalf("max field ID: got %d, want 999", v)
	}
}

// readTag is a test helper that reads the next field tag and validates it.
func readTag(t *testing.T, dec *Decoder, wantID uint8, wantWT WireType) {
	t.Helper()
	fid, wt, err := dec.Next()
	if err != nil {
		t.Fatalf("Next(): %v", err)
	}
	if fid != wantID {
		t.Fatalf("field ID: got %d, want %d", fid, wantID)
	}
	if wt != wantWT {
		t.Fatalf("wire type: got %d, want %d", wt, wantWT)
	}
}

// requireNoError is a test helper that fails immediately on error.
func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

package encoding

import (
	"math"
	"testing"
)

// FuzzRecordRoundTrip encodes a record with all supported field types using
// fuzz-generated values, decodes it, and verifies exact round-trip equality.
func FuzzRecordRoundTrip(f *testing.F) {
	f.Add(
		uint8(1),
		uint64(42),
		int64(-1),
		float64(3.14),
		"hello",
		[]byte{0xDE, 0xAD},
		true,
		int64(1714000000),
		"alpha",
		"beta",
	)
	f.Add(
		uint8(0),
		uint64(0),
		int64(0),
		float64(0),
		"",
		[]byte{},
		false,
		int64(0),
		"",
		"",
	)
	f.Add(
		uint8(255),
		uint64(math.MaxUint64),
		int64(math.MinInt64),
		float64(math.Inf(1)),
		"\x00\xff",
		[]byte{0, 1, 2, 3, 4, 5, 6, 7},
		true,
		int64(math.MaxInt64),
		"a longer string with spaces",
		"unicode: 日本語テスト",
	)

	f.Fuzz(func(t *testing.T, version uint8, u uint64, i int64, fl float64,
		s string, byt []byte, bo bool, ts int64, s1, s2 string,
	) {
		data := fuzzEncode(version, u, i, fl, s, byt, bo, ts, s1, s2)
		fuzzDecode(t, data, version, u, i, fl, s, byt, bo, ts, s1, s2)
	})
}

func fuzzEncode(version uint8, u uint64, i int64, fl float64,
	s string, byt []byte, bo bool, ts int64, s1, s2 string,
) []byte {
	enc := NewEncoder(version)
	enc.PutUint64(0, u)
	enc.PutInt64(1, i)
	enc.PutFloat64(2, fl)
	enc.PutString(3, s)
	enc.PutBytes(4, byt)
	enc.PutBool(5, bo)
	enc.PutTimestamp(6, ts)
	enc.PutStringList(7, []string{s1, s2})
	return append([]byte(nil), enc.Bytes()...)
}

func fuzzDecode(t *testing.T, data []byte, version uint8, u uint64, i int64, fl float64,
	s string, byt []byte, bo bool, ts int64, s1, s2 string,
) {
	t.Helper()
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

	fuzzDecodeFixed64(t, dec, u, i, fl)
	fuzzDecodeVarFields(t, dec, s, byt, bo, ts, s1, s2)

	if !dec.Done() {
		t.Fatal("decoder should be done")
	}
}

func fuzzDecodeFixed64(t *testing.T, dec *Decoder, u uint64, i int64, fl float64) {
	t.Helper()

	if _, _, err := dec.Next(); err != nil {
		t.Fatal(err)
	}
	gotU, err := dec.Uint64()
	if err != nil {
		t.Fatal(err)
	}
	if gotU != u {
		t.Fatalf("uint64: got %d, want %d", gotU, u)
	}

	if _, _, err := dec.Next(); err != nil {
		t.Fatal(err)
	}
	gotI, err := dec.Int64()
	if err != nil {
		t.Fatal(err)
	}
	if gotI != i {
		t.Fatalf("int64: got %d, want %d", gotI, i)
	}

	if _, _, err := dec.Next(); err != nil {
		t.Fatal(err)
	}
	gotF, err := dec.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if math.Float64bits(gotF) != math.Float64bits(fl) {
		t.Fatalf("float64: got %v, want %v", gotF, fl)
	}
}

func fuzzDecodeVarFields(t *testing.T, dec *Decoder,
	s string, byt []byte, bo bool, ts int64, s1, s2 string,
) {
	t.Helper()

	fuzzDecodeStringAndBytes(t, dec, s, byt)
	fuzzDecodeScalarsAndList(t, dec, bo, ts, s1, s2)
}

func fuzzDecodeStringAndBytes(t *testing.T, dec *Decoder, s string, byt []byte) {
	t.Helper()

	if _, _, err := dec.Next(); err != nil {
		t.Fatal(err)
	}
	gotS, err := dec.String()
	if err != nil {
		t.Fatal(err)
	}
	if gotS != s {
		t.Fatalf("string: got %q, want %q", gotS, s)
	}

	if _, _, err := dec.Next(); err != nil {
		t.Fatal(err)
	}
	gotB, err := dec.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(gotB) != len(byt) {
		t.Fatalf("bytes len: got %d, want %d", len(gotB), len(byt))
	}
	for idx := range byt {
		if gotB[idx] != byt[idx] {
			t.Fatalf("bytes[%d]: got %d, want %d", idx, gotB[idx], byt[idx])
		}
	}
}

func fuzzDecodeScalarsAndList(t *testing.T, dec *Decoder, bo bool, ts int64, s1, s2 string) {
	t.Helper()

	if _, _, err := dec.Next(); err != nil {
		t.Fatal(err)
	}
	gotBo, err := dec.Bool()
	if err != nil {
		t.Fatal(err)
	}
	if gotBo != bo {
		t.Fatalf("bool: got %v, want %v", gotBo, bo)
	}

	if _, _, err := dec.Next(); err != nil {
		t.Fatal(err)
	}
	gotTS, err := dec.Timestamp()
	if err != nil {
		t.Fatal(err)
	}
	if gotTS != ts {
		t.Fatalf("timestamp: got %d, want %d", gotTS, ts)
	}

	if _, _, err := dec.Next(); err != nil {
		t.Fatal(err)
	}
	gotSL, err := dec.StringList()
	if err != nil {
		t.Fatal(err)
	}
	wantSL := []string{s1, s2}
	if len(gotSL) != len(wantSL) {
		t.Fatalf("string list len: got %d, want %d", len(gotSL), len(wantSL))
	}
	for idx := range wantSL {
		if gotSL[idx] != wantSL[idx] {
			t.Fatalf("string list[%d]: got %q, want %q", idx, gotSL[idx], wantSL[idx])
		}
	}
}

// FuzzDecoderNoPanic feeds arbitrary bytes to the decoder and verifies it
// never panics, regardless of input.
func FuzzDecoderNoPanic(f *testing.F) {
	f.Add([]byte{1, 0})
	f.Add([]byte{1, 1, 0x00, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{1, 3, 0x01, 5, 'h', 'e', 'l', 'l', 'o', 0x02, 1, 0x03, 1, 2, 'a', 'b'})
	f.Add([]byte{0xFF, 0xFF})

	f.Fuzz(func(_ *testing.T, data []byte) {
		dec, err := NewDecoder(data)
		if err != nil {
			return
		}
		for !dec.Done() {
			_, wt, err := dec.Next()
			if err != nil {
				return
			}
			switch wt {
			case WireFixed64:
				if _, err := dec.Uint64(); err != nil {
					return
				}
			case WireBytes:
				if _, err := dec.Bytes(); err != nil {
					return
				}
			case WireFixed8:
				if _, err := dec.Bool(); err != nil {
					return
				}
			case WireList:
				if _, err := dec.StringList(); err != nil {
					return
				}
			default:
				return
			}
		}
	})
}

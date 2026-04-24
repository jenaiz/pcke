package encoding

import (
	"testing"
)

func TestMakeTagParseTagRoundTrip(t *testing.T) {
	for fieldID := uint8(0); fieldID <= MaxFieldID; fieldID++ {
		for wt := WireFixed64; wt <= maxWireType; wt++ {
			tag := MakeTag(fieldID, wt)
			gotID, gotWT := ParseTag(tag)
			if gotID != fieldID || gotWT != wt {
				t.Fatalf("MakeTag(%d, %d)=%d → ParseTag → (%d, %d)",
					fieldID, wt, tag, gotID, gotWT)
			}
		}
	}
}

func TestTagBitLayout(t *testing.T) {
	// Field 0, wire type 0 → tag 0x00
	if tag := MakeTag(0, WireFixed64); tag != 0x00 {
		t.Fatalf("got 0x%02x, want 0x00", tag)
	}
	// Field 0, wire type 1 → tag 0x01
	if tag := MakeTag(0, WireBytes); tag != 0x01 {
		t.Fatalf("got 0x%02x, want 0x01", tag)
	}
	// Field 1, wire type 0 → tag 0x08 (1<<3)
	if tag := MakeTag(1, WireFixed64); tag != 0x08 {
		t.Fatalf("got 0x%02x, want 0x08", tag)
	}
	// Field 31, wire type 3 → tag 0xFB (31<<3|3)
	if tag := MakeTag(31, WireList); tag != 0xFB {
		t.Fatalf("got 0x%02x, want 0xFB", tag)
	}
}

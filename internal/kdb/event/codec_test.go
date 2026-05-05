package event

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

// fixedTime is used so round-trip equality holds despite time.Time's
// unexported monotonic-clock and location fields.
var fixedTime = time.Unix(0, 1_715_000_000_000_000_000).UTC()

func newHeader(version uint64, lifecycle Lifecycle) Header {
	return Header{
		Version:    version,
		CreatedAt:  fixedTime,
		Lifecycle:  lifecycle,
		Supersedes: nil,
	}
}

func TestEncodeDecode_Entity(t *testing.T) {
	t.Parallel()
	in := &Entity{
		Hdr:  newHeader(1, LifecycleActive),
		EID:  "internal/kdb/db.go",
		Type: "file",
		Path: "internal/kdb/db.go",
		Name: "",
	}
	value, err := Encode(in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(value, in.EID)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	out, ok := got.(*Entity)
	if !ok {
		t.Fatalf("Decode returned %T, want *Entity", got)
	}
	if out.EID != in.EID || out.Type != in.Type || out.Path != in.Path || out.Name != in.Name {
		t.Errorf("entity payload mismatch:\n got  %+v\n want %+v", out, in)
	}
	if !out.Hdr.CreatedAt.Equal(in.Hdr.CreatedAt) ||
		out.Hdr.Version != in.Hdr.Version ||
		out.Hdr.Lifecycle != in.Hdr.Lifecycle {
		t.Errorf("header mismatch:\n got  %+v\n want %+v", out.Hdr, in.Hdr)
	}
}

func TestEncodeDecode_Decision(t *testing.T) {
	t.Parallel()
	in := &Decision{
		Hdr:      newHeader(3, LifecycleActive),
		DID:      "adr-0008",
		Title:    "Context Graph Pivot",
		Body:     "Pivot to a graph-shaped memory.",
		Severity: SeverityMust,
		Scope:    ScopeGlobal,
		Source:   "adr",
	}
	value, err := Encode(in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(value, in.DID)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	out, ok := got.(*Decision)
	if !ok {
		t.Fatalf("Decode returned %T, want *Decision", got)
	}
	if out.DID != in.DID || out.Title != in.Title || out.Body != in.Body ||
		out.Severity != in.Severity || out.Scope != in.Scope || out.Source != in.Source {
		t.Errorf("decision payload mismatch:\n got  %+v\n want %+v", out, in)
	}
}

func TestEncodeDecode_Link(t *testing.T) {
	t.Parallel()
	in := &Link{
		Hdr:      newHeader(1, LifecycleActive),
		SrcRef:   "e:internal/kdb/db.go",
		EdgeType: "imports",
		DstRef:   "e:internal/kdb/btree",
	}
	value, err := Encode(in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(value, in.ID())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	out, ok := got.(*Link)
	if !ok {
		t.Fatalf("Decode returned %T, want *Link", got)
	}
	if out.SrcRef != in.SrcRef || out.EdgeType != in.EdgeType || out.DstRef != in.DstRef {
		t.Errorf("link payload mismatch:\n got  %+v\n want %+v", out, in)
	}
}

func TestEncodeDecode_Observation(t *testing.T) {
	t.Parallel()
	in := &Observation{
		Hdr:     newHeader(1, LifecycleActive),
		OID:     "session-1/call-7",
		Action:  "tool_call",
		Subject: "e:internal/kdb/db.go",
	}
	value, err := Encode(in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(value, in.OID)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	out, ok := got.(*Observation)
	if !ok {
		t.Fatalf("Decode returned %T, want *Observation", got)
	}
	if out.Action != in.Action || out.Subject != in.Subject {
		t.Errorf("observation payload mismatch:\n got  %+v\n want %+v", out, in)
	}
}

func TestEncodeDecode_Outcome(t *testing.T) {
	t.Parallel()
	in := &Outcome{
		Hdr:     newHeader(1, LifecycleActive),
		XID:     "respect-1",
		Type:    "respect",
		Subject: "d:adr-0008",
	}
	value, err := Encode(in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(value, in.XID)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	out, ok := got.(*Outcome)
	if !ok {
		t.Fatalf("Decode returned %T, want *Outcome", got)
	}
	if out.Type != in.Type || out.Subject != in.Subject {
		t.Errorf("outcome payload mismatch:\n got  %+v\n want %+v", out, in)
	}
}

func TestEncodeDecode_SupersedesPointer(t *testing.T) {
	t.Parallel()
	priorKey, err := BuildKey(KindEntity, "internal/kdb/db.go", 1)
	if err != nil {
		t.Fatalf("BuildKey: %v", err)
	}
	in := &Entity{
		Hdr: Header{
			Version:    2,
			CreatedAt:  fixedTime,
			Lifecycle:  LifecycleActive,
			Supersedes: priorKey,
		},
		EID:  "internal/kdb/db.go",
		Type: "file",
	}
	value, err := Encode(in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(value, in.EID)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(got.Header().Supersedes, priorKey) {
		t.Errorf("supersedes mismatch: got %q, want %q", got.Header().Supersedes, priorKey)
	}
}

func TestEncode_Errors(t *testing.T) {
	t.Parallel()
	if _, err := Encode(nil); !errors.Is(err, ErrCorrupt) {
		t.Errorf("nil event: got %v, want wrap of ErrCorrupt", err)
	}
	if _, err := Encode(&Entity{Hdr: newHeader(1, LifecycleActive), EID: ""}); !errors.Is(err, ErrEmptyID) {
		t.Errorf("empty id: got %v, want ErrEmptyID", err)
	}
	bad := &Entity{Hdr: Header{Version: 1, Lifecycle: Lifecycle(99)}, EID: "x"}
	if _, err := Encode(bad); !errors.Is(err, ErrInvalidLifecycle) {
		t.Errorf("invalid lifecycle: got %v, want ErrInvalidLifecycle", err)
	}
}

func TestDecode_Errors(t *testing.T) {
	t.Parallel()

	t.Run("empty input", func(t *testing.T) {
		t.Parallel()
		if _, err := Decode(nil, "x"); !errors.Is(err, ErrCorrupt) {
			t.Errorf("got %v, want wrap of ErrCorrupt", err)
		}
	})

	t.Run("schema version mismatch", func(t *testing.T) {
		t.Parallel()
		// A valid record with version 99 (unsupported).
		buf := []byte{99, 0}
		if _, err := Decode(buf, "x"); !errors.Is(err, ErrSchemaVersion) {
			t.Errorf("got %v, want wrap of ErrSchemaVersion", err)
		}
	})

	t.Run("missing event_kind field", func(t *testing.T) {
		t.Parallel()
		// Valid record with version=1 and 0 fields.
		buf := []byte{1, 0}
		if _, err := Decode(buf, "x"); !errors.Is(err, ErrInvalidKind) {
			t.Errorf("got %v, want wrap of ErrInvalidKind", err)
		}
	})

	t.Run("invalid event_kind value", func(t *testing.T) {
		t.Parallel()
		in := &Entity{Hdr: newHeader(1, LifecycleActive), EID: "x", Type: "file"}
		value, err := Encode(in)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		// Hand-corrupt the event_kind byte. Find the WireFixed8 byte for
		// fieldKind: tag is (4 << 3) | WireFixed8 == (4<<3)|2 == 34.
		idx := bytes.IndexByte(value, byte(fieldKind<<3|2))
		if idx < 0 || idx+1 >= len(value) {
			t.Fatalf("could not locate event_kind tag in encoded value")
		}
		value[idx+1] = 99 // unknown kind
		if _, err := Decode(value, "x"); !errors.Is(err, ErrInvalidKind) {
			t.Errorf("got %v, want wrap of ErrInvalidKind", err)
		}
	})

	t.Run("empty id supplied", func(t *testing.T) {
		t.Parallel()
		in := &Entity{Hdr: newHeader(1, LifecycleActive), EID: "x", Type: "file"}
		value, err := Encode(in)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		if _, err := Decode(value, ""); !errors.Is(err, ErrEmptyID) {
			t.Errorf("got %v, want ErrEmptyID", err)
		}
	})
}

func TestDecode_ForwardCompat_UnknownPayloadField(t *testing.T) {
	t.Parallel()
	// Encode a normal Entity record but append an extra field with an
	// unused fieldID and a string value, mimicking a future writer adding
	// a field that this build doesn't know about. Bump the field count
	// in the header to match.
	in := &Entity{Hdr: newHeader(1, LifecycleActive), EID: "x", Type: "file"}
	value, err := Encode(in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// Field count is byte 1 in the record. Add one extra string field.
	value[1]++
	const futureFieldID uint8 = 30
	tag := byte(futureFieldID<<3) | 1 // WireBytes == 1
	value = append(value, tag)
	value = append(value, 4) // varint length
	value = append(value, []byte("data")...)

	got, err := Decode(value, in.EID)
	if err != nil {
		t.Fatalf("forward-compat decode: %v", err)
	}
	out, ok := got.(*Entity)
	if !ok || out.EID != in.EID || out.Type != in.Type {
		t.Errorf("forward-compat decode lost fields: got %+v", got)
	}
}

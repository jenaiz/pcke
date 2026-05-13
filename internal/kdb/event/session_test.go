package event_test

import (
	"strings"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/event"
)

func TestSessionOID_PrefixAndExtract(t *testing.T) {
	t.Parallel()

	cases := []struct {
		uuid string
	}{
		{"abc-123"},
		{"00000000-0000-0000-0000-000000000000"},
		{"weird:uuid"}, // colons in uuid pass through; storage key escapes them
		{""},           // empty uuid: bare prefix
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.uuid, func(t *testing.T) {
			t.Parallel()
			oid := event.SessionOID(tc.uuid)
			if got, want := oid, event.SessionOIDPrefix+tc.uuid; got != want {
				t.Errorf("SessionOID(%q) = %q, want %q", tc.uuid, got, want)
			}
			if !event.IsSessionOID(oid) {
				t.Errorf("IsSessionOID(%q) = false, want true", oid)
			}
			if event.IsCallOID(oid) {
				t.Errorf("IsCallOID(%q) = true, want false", oid)
			}
			gotUUID, ok := event.SessionUUID(oid)
			if !ok || gotUUID != tc.uuid {
				t.Errorf("SessionUUID(%q) = (%q,%v), want (%q,true)",
					oid, gotUUID, ok, tc.uuid)
			}
		})
	}
}

func TestCallOID_PrefixAndExtract(t *testing.T) {
	t.Parallel()

	oid := event.CallOID("xyz-987")
	if got, want := oid, "call:xyz-987"; got != want {
		t.Errorf("CallOID = %q, want %q", got, want)
	}
	if !event.IsCallOID(oid) {
		t.Errorf("IsCallOID(%q) = false, want true", oid)
	}
	if event.IsSessionOID(oid) {
		t.Errorf("IsSessionOID(%q) = true, want false", oid)
	}
	gotUUID, ok := event.CallUUID(oid)
	if !ok || gotUUID != "xyz-987" {
		t.Errorf("CallUUID = (%q,%v), want (\"xyz-987\",true)", gotUUID, ok)
	}
}

func TestSessionRef_AndCallRef_AreObservationPrefixed(t *testing.T) {
	t.Parallel()

	if got := event.SessionRef("u1"); !strings.HasPrefix(got, event.KindObservation.Prefix()) {
		t.Errorf("SessionRef = %q, missing observation prefix", got)
	}
	if got := event.CallRef("u2"); !strings.HasPrefix(got, event.KindObservation.Prefix()) {
		t.Errorf("CallRef = %q, missing observation prefix", got)
	}
	if got, want := event.SessionRef("u1"), "o:session:u1"; got != want {
		t.Errorf("SessionRef = %q, want %q", got, want)
	}
	if got, want := event.CallRef("u2"), "o:call:u2"; got != want {
		t.Errorf("CallRef = %q, want %q", got, want)
	}
}

func TestSessionOID_RoundTripsThroughBuildAndParseKey(t *testing.T) {
	t.Parallel()

	oid := event.SessionOID("s1")
	key, err := event.BuildKey(event.KindObservation, oid, 1)
	if err != nil {
		t.Fatalf("BuildKey: %v", err)
	}
	// The structural colon in oid must be escaped in the raw key so the
	// version suffix is unambiguous. We don't pin the exact escape form
	// here — round-trip equality is what callers depend on.
	if !strings.HasPrefix(string(key), event.KindObservation.Prefix()) {
		t.Errorf("key = %q, missing %q prefix", key, event.KindObservation.Prefix())
	}
	parsed, err := event.ParseKey(key)
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}
	if parsed.Kind != event.KindObservation {
		t.Errorf("Kind = %v, want %v", parsed.Kind, event.KindObservation)
	}
	if parsed.ID != oid {
		t.Errorf("ID = %q, want %q", parsed.ID, oid)
	}
	if parsed.Version != 1 {
		t.Errorf("Version = %d, want 1", parsed.Version)
	}
}

func TestCallOID_RoundTripsThroughBuildAndParseKey(t *testing.T) {
	t.Parallel()

	oid := event.CallOID("c1")
	key, err := event.BuildKey(event.KindObservation, oid, 7)
	if err != nil {
		t.Fatalf("BuildKey: %v", err)
	}
	parsed, err := event.ParseKey(key)
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}
	if parsed.ID != oid {
		t.Errorf("ID = %q, want %q", parsed.ID, oid)
	}
	if parsed.Version != 7 {
		t.Errorf("Version = %d, want 7", parsed.Version)
	}
}

func TestSessionUUID_OnNonSessionOIDReturnsFalse(t *testing.T) {
	t.Parallel()

	if _, ok := event.SessionUUID("call:abc"); ok {
		t.Error("SessionUUID(call:abc) ok = true, want false")
	}
	if _, ok := event.CallUUID("session:abc"); ok {
		t.Error("CallUUID(session:abc) ok = true, want false")
	}
}

func TestActionAndEdgeConstants_AreStable(t *testing.T) {
	t.Parallel()

	// These literals are wire-stable: bumping the schema means writing a
	// new migration, not editing the constant. Pin the surface values so
	// an accidental rename is caught at test time.
	pairs := []struct{ got, want string }{
		{event.ActionSession, "session"},
		{event.ActionCall, "call"},
		{event.EdgeContains, "contains"},
		{event.EdgeServed, "served"},
		{event.EdgeBelongsTo, "belongs_to"},
		{event.SessionOIDPrefix, "session:"},
		{event.CallOIDPrefix, "call:"},
	}
	for _, p := range pairs {
		if p.got != p.want {
			t.Errorf("constant changed: got %q, want %q", p.got, p.want)
		}
	}
}

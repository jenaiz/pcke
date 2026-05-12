package retrieval

import (
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/event"
)

func TestSplitRef_AllKinds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		wantKnd event.Kind
		wantID  string
		wantOK  bool
	}{
		{"e:internal/kdb/db.go", event.KindEntity, "internal/kdb/db.go", true},
		{"d:adr-0008", event.KindDecision, "adr-0008", true},
		{"l:imports:abc", event.KindLink, "imports:abc", true},
		{"o:scan-001", event.KindObservation, "scan-001", true},
		{"x:summary-001", event.KindOutcome, "summary-001", true},
		{"z:unknown", 0, "", false},
		{"", 0, "", false},
		{"e", 0, "", false},
	}
	for _, c := range cases {
		gotKnd, gotID, gotOK := splitRef(c.in)
		if gotKnd != c.wantKnd || gotID != c.wantID || gotOK != c.wantOK {
			t.Errorf("splitRef(%q) = (%v, %q, %v); want (%v, %q, %v)",
				c.in, gotKnd, gotID, gotOK, c.wantKnd, c.wantID, c.wantOK)
		}
	}
}

func TestTitleForEvent_AllKinds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		evt  event.Event
		want string
	}{
		{"entity with path", &event.Entity{EID: "x", Path: "internal/kdb/db.go"}, "internal/kdb/db.go"},
		{"entity no path", &event.Entity{EID: "fallback"}, "fallback"},
		{"decision with title", &event.Decision{DID: "d1", Title: "Validate input"}, "Validate input"},
		{"decision no title", &event.Decision{DID: "d2"}, "d2"},
		{"link", &event.Link{SrcRef: "e:a", EdgeType: "imports", DstRef: "e:b"}, "e:a --imports--> e:b"},
		{"observation falls back to ref", &event.Observation{OID: "obs1"}, "o:obs1"},
		{"outcome falls back to ref", &event.Outcome{XID: "out1"}, "x:out1"},
	}
	for _, c := range cases {
		if got := titleForEvent(c.evt); got != c.want {
			t.Errorf("%s: titleForEvent = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestBodyForEvent_AllKinds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		evt  event.Event
		want string
	}{
		{"entity full", &event.Entity{Path: "p.go", Type: "file", Name: "P"}, "p.go: file P"},
		{"entity path-only", &event.Entity{Path: "p.go"}, "p.go"},
		{"decision body", &event.Decision{Title: "T", Body: "B"}, "B"},
		{"decision title fallback", &event.Decision{Title: "T"}, "T"},
		{"link", &event.Link{SrcRef: "e:a", EdgeType: "imports", DstRef: "e:b"}, "e:a --imports--> e:b"},
		{"observation default empty", &event.Observation{OID: "o1"}, ""},
		{"outcome default empty", &event.Outcome{XID: "x1"}, ""},
	}
	for _, c := range cases {
		if got := bodyForEvent(c.evt); got != c.want {
			t.Errorf("%s: bodyForEvent = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRefForEvent_AllKinds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		evt  event.Event
		want string
	}{
		{&event.Entity{EID: "internal/kdb/db.go"}, "e:internal/kdb/db.go"},
		{&event.Decision{DID: "adr-0008"}, "d:adr-0008"},
		{&event.Link{SrcRef: "e:a", EdgeType: "imports", DstRef: "e:b"}, "l:e\\ca:imports:e\\cb"},
		{&event.Observation{OID: "obs1"}, "o:obs1"},
		{&event.Outcome{XID: "out1"}, "x:out1"},
	}
	for _, c := range cases {
		if got := refForEvent(c.evt); got != c.want {
			t.Errorf("refForEvent(%T) = %q, want %q", c.evt, got, c.want)
		}
	}
}

func TestPathForEvent_DecisionAnchor(t *testing.T) {
	t.Parallel()
	d := &event.Decision{Body: "[file: internal/kdb/db.go:42]\n\nUse prepared statements."}
	if got := pathForEvent(d); got != "internal/kdb/db.go" {
		t.Errorf("pathForEvent(decision anchored) = %q, want internal/kdb/db.go", got)
	}
	if got := pathForEvent(&event.Decision{DID: "no-anchor"}); got != "" {
		t.Errorf("pathForEvent(decision no anchor) = %q, want empty", got)
	}
	if got := pathForEvent(&event.Observation{OID: "obs1"}); got != "" {
		t.Errorf("pathForEvent(observation) = %q, want empty", got)
	}
}

func TestIsAllDigits(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"42":     true,
		"0":      true,
		"":       false,
		"4a2":    false,
		"-1":     false,
		" 1":     false,
		"999999": true,
	}
	for in, want := range cases {
		if got := isAllDigits(in); got != want {
			t.Errorf("isAllDigits(%q) = %v, want %v", in, got, want)
		}
	}
}

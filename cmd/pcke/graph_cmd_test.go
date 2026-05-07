package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb/event"
)

func TestParseAsOfFlag(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"2026-04-01":               true,
		"2026-04-01T12:30:00Z":     true,
		"2026-04-01T12:30:00.001Z": true,
		"":                         false,
		"not a date":               false,
		"2026/04/01":               false,
	}
	for in, ok := range cases {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			_, err := parseAsOfFlag(in)
			if ok && err != nil {
				t.Errorf("parseAsOfFlag(%q) unexpected error: %v", in, err)
			}
			if !ok && err == nil {
				t.Errorf("parseAsOfFlag(%q): want error, got nil", in)
			}
		})
	}
}

func TestParseAsOfFlag_ReturnsUTC(t *testing.T) {
	t.Parallel()
	got, err := parseAsOfFlag("2026-04-01T12:30:00+02:00")
	if err != nil {
		t.Fatalf("parseAsOfFlag: %v", err)
	}
	if got.Location() != time.UTC {
		t.Errorf("Location = %v, want UTC", got.Location())
	}
}

func TestParseSeverityFilter(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		want    event.Severity
		wantErr bool
	}{
		"":          {0, false},
		"must":      {event.SeverityMust, false},
		"should":    {event.SeverityShould, false},
		"may":       {event.SeverityMay, false},
		"MUST":      {event.SeverityMust, false},
		"  Should ": {event.SeverityShould, false},
		"unknown":   {0, true},
		"high":      {0, true},
	}
	for in, tc := range cases {
		t.Run(strings.TrimSpace(in), func(t *testing.T) {
			t.Parallel()
			got, err := parseSeverityFilter(in)
			if tc.wantErr && err == nil {
				t.Errorf("parseSeverityFilter(%q): want error", in)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("parseSeverityFilter(%q): unexpected error: %v", in, err)
			}
			if got != tc.want {
				t.Errorf("parseSeverityFilter(%q) = %d, want %d", in, got, tc.want)
			}
		})
	}
}

func TestParseTypedRef(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		kind    event.Kind
		id      string
		wantErr bool
	}{
		{"e:foo", event.KindEntity, "foo", false},
		{"d:adr-0008", event.KindDecision, "adr-0008", false},
		{"o:session-1", event.KindObservation, "session-1", false},
		{"x:respect-1", event.KindOutcome, "respect-1", false},
		{"l:linkid", event.KindLink, "linkid", false},
		// Trailing :v<digits> stripped.
		{"e:foo:v0000000000000003", event.KindEntity, "foo", false},
		{"d:bar:v42", event.KindDecision, "bar", false},
		// Ambiguity: "e:v1234" — ":v1234" looks like a version suffix
		// of a four-digit number, but parseTypedRef strips it.
		// That's accepted for now; users with literal ids ending in
		// :v<digits> need to rename them (vanishingly unlikely).
		// Failures.
		{"", 0, "", true},
		{"e:", 0, "", true},
		{":foo", 0, "", true},
		{"foo", 0, "", true},
		{"z:foo", 0, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			gotKind, gotID, err := parseTypedRef(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseTypedRef(%q): want error", tc.in)
				}
				return
			}
			if err != nil {
				t.Errorf("parseTypedRef(%q): unexpected error %v", tc.in, err)
				return
			}
			if gotKind != tc.kind {
				t.Errorf("kind = %v, want %v", gotKind, tc.kind)
			}
			if gotID != tc.id {
				t.Errorf("id = %q, want %q", gotID, tc.id)
			}
		})
	}
}

func TestExcerptForEvent(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		evt  event.Event
		want string
	}{
		"entity": {
			&event.Entity{EID: "x", Type: "file", Name: "main.go", Path: "cmd/pcke/main.go"},
			"file main.go (cmd/pcke/main.go)",
		},
		"decision": {
			&event.Decision{DID: "d", Title: "Pivot to event log"},
			"Pivot to event log",
		},
		"link": {
			&event.Link{SrcRef: "e:a", EdgeType: "imports", DstRef: "e:b"},
			"e:a --imports--> e:b",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := excerptForEvent(tc.evt)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExcerptForEvent_TruncatesAndStripsNewlines(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", 300)
	got := excerptForEvent(&event.Decision{DID: "d", Title: long + "\nmore text"})
	if r := []rune(got); len(r) != 80 {
		t.Errorf("length = %d, want 80", len(r))
	}
	if strings.Contains(got, "\n") {
		t.Errorf("excerpt contains newline: %q", got)
	}
}

// errors helper just to keep the import live.
var _ = errors.New

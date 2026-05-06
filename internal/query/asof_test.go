package query

import (
	"errors"
	"testing"
	"time"
)

func TestParse_AsOf_RFC3339(t *testing.T) {
	t.Parallel()
	q, err := Parse("nodes as of '2026-04-01T12:30:00Z'")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.AsOf == nil {
		t.Fatal("AsOf is nil")
	}
	want := time.Date(2026, 4, 1, 12, 30, 0, 0, time.UTC)
	if !q.AsOf.Equal(want) {
		t.Errorf("AsOf = %v, want %v", q.AsOf, want)
	}
}

func TestParse_AsOf_DateOnly(t *testing.T) {
	t.Parallel()
	q, err := Parse("nodes as of '2026-04-01'")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.AsOf == nil {
		t.Fatal("AsOf is nil")
	}
	want := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	if !q.AsOf.Equal(want) {
		t.Errorf("AsOf = %v, want %v", q.AsOf, want)
	}
}

func TestParse_AsOf_RFC3339Nano(t *testing.T) {
	t.Parallel()
	q, err := Parse("nodes as of '2026-04-01T12:30:00.123456789Z'")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.AsOf == nil {
		t.Fatal("AsOf is nil")
	}
}

func TestParse_AsOf_AlongsideOtherClauses(t *testing.T) {
	t.Parallel()
	q, err := Parse("nodes where type = 'module' as of '2026-04-01' order by stability desc limit 10")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.AsOf == nil {
		t.Fatal("AsOf is nil")
	}
	if q.Where == nil || len(q.Where.Conditions) != 1 {
		t.Errorf("Where lost: %+v", q.Where)
	}
	if q.OrderBy == nil || q.OrderBy.Field != "stability" {
		t.Errorf("OrderBy lost: %+v", q.OrderBy)
	}
	if q.Limit != 10 {
		t.Errorf("Limit = %d, want 10", q.Limit)
	}
}

func TestParse_AsOf_CaseInsensitive(t *testing.T) {
	t.Parallel()
	cases := []string{
		"nodes AS OF '2026-04-01'",
		"nodes As Of '2026-04-01'",
		"nodes as OF '2026-04-01'",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			q, err := Parse(in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", in, err)
			}
			if q.AsOf == nil {
				t.Errorf("AsOf nil for %q", in)
			}
		})
	}
}

func TestParse_AsOf_MissingOf(t *testing.T) {
	t.Parallel()
	_, err := Parse("nodes as '2026-04-01'")
	if !errors.Is(err, ErrSyntax) {
		t.Errorf("got %v, want wrap of ErrSyntax", err)
	}
}

func TestParse_AsOf_MissingTimestamp(t *testing.T) {
	t.Parallel()
	_, err := Parse("nodes as of")
	if !errors.Is(err, ErrSyntax) {
		t.Errorf("got %v, want wrap of ErrSyntax", err)
	}
}

func TestParse_AsOf_NonStringTimestamp(t *testing.T) {
	t.Parallel()
	_, err := Parse("nodes as of 12345")
	if !errors.Is(err, ErrSyntax) {
		t.Errorf("got %v, want wrap of ErrSyntax", err)
	}
}

func TestParse_AsOf_InvalidTimestampFormat(t *testing.T) {
	t.Parallel()
	cases := []string{
		"nodes as of 'not a date'",
		"nodes as of '2026/04/01'",
		"nodes as of '01-04-2026'",
		"nodes as of ''",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			_, err := Parse(in)
			if !errors.Is(err, ErrSyntax) {
				t.Errorf("Parse(%q): got %v, want wrap of ErrSyntax", in, err)
			}
		})
	}
}

func TestParse_AsOf_DuplicateRejected(t *testing.T) {
	t.Parallel()
	_, err := Parse("nodes as of '2026-04-01' as of '2026-05-01'")
	if !errors.Is(err, ErrSyntax) {
		t.Errorf("got %v, want wrap of ErrSyntax", err)
	}
}

func TestParse_NoAsOf_NilField(t *testing.T) {
	t.Parallel()
	q, err := Parse("nodes where type = 'module'")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.AsOf != nil {
		t.Errorf("AsOf = %v, want nil for query without AS OF", q.AsOf)
	}
}

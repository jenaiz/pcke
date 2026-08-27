package query

import (
	"errors"
	"testing"
)

func TestParse_Traverse_Minimal(t *testing.T) {
	t.Parallel()
	q, err := Parse("nodes where traverse(edges) from 'e:foo'")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.Where == nil || q.Where.Traverse == nil {
		t.Fatalf("Where.Traverse is nil; got %+v", q.Where)
	}
	tx := q.Where.Traverse
	if tx.EdgeName != "edges" {
		t.Errorf("EdgeName = %q, want %q", tx.EdgeName, "edges")
	}
	if tx.StartKey != "e:foo" {
		t.Errorf("StartKey = %q, want %q", tx.StartKey, "e:foo")
	}
	if tx.Direction != "forward" {
		t.Errorf("Direction = %q, want forward (default)", tx.Direction)
	}
	if tx.Depth != 0 {
		t.Errorf("Depth = %d, want 0 (executor maps zero to default)", tx.Depth)
	}
	if tx.EdgeType != "" {
		t.Errorf("EdgeType = %q, want empty", tx.EdgeType)
	}
}

func TestParse_Traverse_FullArgs(t *testing.T) {
	t.Parallel()
	q, err := Parse("nodes where traverse(edges, depth=3, edge='imports', direction=reverse) from 'e:internal/kdb/btree'")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	tx := q.Where.Traverse
	if tx == nil {
		t.Fatal("Traverse is nil")
		return
	}
	if tx.Depth != 3 {
		t.Errorf("Depth = %d, want 3", tx.Depth)
	}
	if tx.EdgeType != "imports" {
		t.Errorf("EdgeType = %q, want imports", tx.EdgeType)
	}
	if tx.Direction != "reverse" {
		t.Errorf("Direction = %q, want reverse", tx.Direction)
	}
	if tx.StartKey != "e:internal/kdb/btree" {
		t.Errorf("StartKey = %q, want e:internal/kdb/btree", tx.StartKey)
	}
}

func TestParse_Traverse_TypeAliasForEdge(t *testing.T) {
	t.Parallel()
	// "type='imports'" should be accepted as an alias for edge='imports'
	// because the PRD uses "type" in some examples.
	q, err := Parse("nodes where traverse(edges, type='imports') from 'e:foo'")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.Where.Traverse.EdgeType != "imports" {
		t.Errorf("EdgeType = %q, want imports", q.Where.Traverse.EdgeType)
	}
}

func TestParse_Traverse_DirectionVariants(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"nodes where traverse(edges, direction=forward) from 'k'":   "forward",
		"nodes where traverse(edges, direction=reverse) from 'k'":   "reverse",
		"nodes where traverse(edges, direction=both) from 'k'":      "both",
		"nodes where traverse(edges, direction='reverse') from 'k'": "reverse", // string form OK
	}
	for in, want := range cases {
		in, want := in, want
		t.Run(want, func(t *testing.T) {
			t.Parallel()
			q, err := Parse(in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", in, err)
			}
			if q.Where.Traverse.Direction != want {
				t.Errorf("Direction = %q, want %q", q.Where.Traverse.Direction, want)
			}
		})
	}
}

func TestParse_Traverse_AlongsideOtherClauses(t *testing.T) {
	t.Parallel()
	q, err := Parse("nodes where traverse(edges, depth=2) from 'e:a' as of '2026-04-01' order by recency desc limit 20")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.Where == nil || q.Where.Traverse == nil {
		t.Fatal("Where.Traverse missing")
	}
	if q.AsOf == nil {
		t.Error("AsOf missing")
	}
	if q.OrderBy == nil || q.OrderBy.Field != "recency" {
		t.Errorf("OrderBy = %+v, want recency desc", q.OrderBy)
	}
	if q.Limit != 20 {
		t.Errorf("Limit = %d, want 20", q.Limit)
	}
}

func TestParse_Traverse_MissingOpenParen(t *testing.T) {
	t.Parallel()
	_, err := Parse("nodes where traverse edges) from 'e:foo'")
	if !errors.Is(err, ErrSyntax) {
		t.Errorf("got %v, want wrap of ErrSyntax", err)
	}
}

func TestParse_Traverse_MissingCloseParen(t *testing.T) {
	t.Parallel()
	_, err := Parse("nodes where traverse(edges, depth=2 from 'e:foo'")
	if !errors.Is(err, ErrSyntax) {
		t.Errorf("got %v, want wrap of ErrSyntax", err)
	}
}

func TestParse_Traverse_MissingFrom(t *testing.T) {
	t.Parallel()
	_, err := Parse("nodes where traverse(edges) 'e:foo'")
	if !errors.Is(err, ErrSyntax) {
		t.Errorf("got %v, want wrap of ErrSyntax", err)
	}
}

func TestParse_Traverse_MissingStartKey(t *testing.T) {
	t.Parallel()
	_, err := Parse("nodes where traverse(edges) from")
	if !errors.Is(err, ErrSyntax) {
		t.Errorf("got %v, want wrap of ErrSyntax", err)
	}
}

func TestParse_Traverse_EmptyStartKey(t *testing.T) {
	t.Parallel()
	_, err := Parse("nodes where traverse(edges) from ''")
	if !errors.Is(err, ErrSyntax) {
		t.Errorf("got %v, want wrap of ErrSyntax (empty start key)", err)
	}
}

func TestParse_Traverse_UnknownArg(t *testing.T) {
	t.Parallel()
	_, err := Parse("nodes where traverse(edges, weight=2) from 'k'")
	if !errors.Is(err, ErrSyntax) {
		t.Errorf("got %v, want wrap of ErrSyntax (unknown arg)", err)
	}
}

func TestParse_Traverse_InvalidDirection(t *testing.T) {
	t.Parallel()
	_, err := Parse("nodes where traverse(edges, direction=sideways) from 'k'")
	if !errors.Is(err, ErrSyntax) {
		t.Errorf("got %v, want wrap of ErrSyntax (invalid direction)", err)
	}
}

func TestParse_Traverse_DepthMustBeNumber(t *testing.T) {
	t.Parallel()
	_, err := Parse("nodes where traverse(edges, depth='two') from 'k'")
	if !errors.Is(err, ErrSyntax) {
		t.Errorf("got %v, want wrap of ErrSyntax", err)
	}
}

func TestParse_Traverse_DepthNonNegative(t *testing.T) {
	t.Parallel()
	// Negative depths are nonsensical; the lexer treats '-' as
	// non-letter/non-digit and bails before we even see the parser.
	// This test pins the user-visible behaviour: malformed input ->
	// ErrSyntax (anywhere in the pipeline).
	_, err := Parse("nodes where traverse(edges, depth=-2) from 'k'")
	if !errors.Is(err, ErrSyntax) {
		t.Errorf("got %v, want wrap of ErrSyntax", err)
	}
}

func TestParse_Traverse_EdgeTypeMustBeString(t *testing.T) {
	t.Parallel()
	_, err := Parse("nodes where traverse(edges, edge=imports) from 'k'")
	if !errors.Is(err, ErrSyntax) {
		t.Errorf("got %v, want wrap of ErrSyntax (edge= needs a quoted string)", err)
	}
}

func TestParse_Traverse_AndConditionsRejected(t *testing.T) {
	t.Parallel()
	// TRAVERSE plus regular conditions is intentionally out of scope
	// for v0.10.0; the parser must reject mixed forms loudly so users
	// don't silently get the wrong query shape.
	_, err := Parse("nodes where traverse(edges) from 'k' and stability > 0.7")
	if !errors.Is(err, ErrSyntax) {
		t.Errorf("got %v, want wrap of ErrSyntax (mixing TRAVERSE with conditions)", err)
	}
}

func TestParse_NoTraverse_NilField(t *testing.T) {
	t.Parallel()
	q, err := Parse("nodes where type = 'module'")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.Where == nil {
		t.Fatal("Where is nil")
	}
	if q.Where.Traverse != nil {
		t.Errorf("Traverse = %+v, want nil for normal where", q.Where.Traverse)
	}
}

func TestLex_NewPunctuation(t *testing.T) {
	t.Parallel()
	tokens, err := Tokenize("(a, b)")
	if err != nil {
		t.Fatalf("Tokenize: %v", err)
	}
	wantTypes := []TokenType{TokenLeftParen, TokenIdent, TokenComma, TokenIdent, TokenRightParen, TokenEOF}
	if len(tokens) != len(wantTypes) {
		t.Fatalf("got %d tokens, want %d: %v", len(tokens), len(wantTypes), tokens)
	}
	for i, want := range wantTypes {
		if tokens[i].Type != want {
			t.Errorf("token %d type = %v, want %v", i, tokens[i].Type, want)
		}
	}
}

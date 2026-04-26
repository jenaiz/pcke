package query

import (
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []Token
		wantErr bool
	}{
		{
			name:  "simple collection",
			input: "nodes",
			want: []Token{
				{Type: TokenIdent, Literal: "nodes", Pos: 0},
				{Type: TokenEOF, Pos: 5},
			},
		},
		{
			name:  "collection with where",
			input: "nodes where type = 'module'",
			want: []Token{
				{Type: TokenIdent, Literal: "nodes", Pos: 0},
				{Type: TokenIdent, Literal: "where", Pos: 6},
				{Type: TokenIdent, Literal: "type", Pos: 12},
				{Type: TokenEq, Literal: "=", Pos: 17},
				{Type: TokenString, Literal: "module", Pos: 19},
				{Type: TokenEOF, Pos: 27},
			},
		},
		{
			name:  "PRD example 1: nodes with AND",
			input: "nodes where type = 'module' and stability > 0.7",
			want: []Token{
				{Type: TokenIdent, Literal: "nodes", Pos: 0},
				{Type: TokenIdent, Literal: "where", Pos: 6},
				{Type: TokenIdent, Literal: "type", Pos: 12},
				{Type: TokenEq, Literal: "=", Pos: 17},
				{Type: TokenString, Literal: "module", Pos: 19},
				{Type: TokenIdent, Literal: "and", Pos: 28},
				{Type: TokenIdent, Literal: "stability", Pos: 32},
				{Type: TokenGt, Literal: ">", Pos: 42},
				{Type: TokenNumber, Literal: "0.7", Pos: 44},
				{Type: TokenEOF, Pos: 47},
			},
		},
		{
			name:  "PRD example 2: order by desc limit",
			input: "nodes where module = 'api' order by updated_at desc limit 10",
			want: []Token{
				{Type: TokenIdent, Literal: "nodes", Pos: 0},
				{Type: TokenIdent, Literal: "where", Pos: 6},
				{Type: TokenIdent, Literal: "module", Pos: 12},
				{Type: TokenEq, Literal: "=", Pos: 19},
				{Type: TokenString, Literal: "api", Pos: 21},
				{Type: TokenIdent, Literal: "order", Pos: 27},
				{Type: TokenIdent, Literal: "by", Pos: 33},
				{Type: TokenIdent, Literal: "updated_at", Pos: 36},
				{Type: TokenIdent, Literal: "desc", Pos: 47},
				{Type: TokenIdent, Literal: "limit", Pos: 52},
				{Type: TokenNumber, Literal: "10", Pos: 58},
				{Type: TokenEOF, Pos: 60},
			},
		},
		{
			name:  "PRD example 3: constraints",
			input: "constraints where scope = 'global' and severity = 'must'",
			want: []Token{
				{Type: TokenIdent, Literal: "constraints", Pos: 0},
				{Type: TokenIdent, Literal: "where", Pos: 12},
				{Type: TokenIdent, Literal: "scope", Pos: 18},
				{Type: TokenEq, Literal: "=", Pos: 24},
				{Type: TokenString, Literal: "global", Pos: 26},
				{Type: TokenIdent, Literal: "and", Pos: 35},
				{Type: TokenIdent, Literal: "severity", Pos: 39},
				{Type: TokenEq, Literal: "=", Pos: 48},
				{Type: TokenString, Literal: "must", Pos: 50},
				{Type: TokenEOF, Pos: 56},
			},
		},
		{
			name:  "PRD example 4: evolution",
			input: "evolution where author = 'jesus' and change_type = 'refactored'",
			want: []Token{
				{Type: TokenIdent, Literal: "evolution", Pos: 0},
				{Type: TokenIdent, Literal: "where", Pos: 10},
				{Type: TokenIdent, Literal: "author", Pos: 16},
				{Type: TokenEq, Literal: "=", Pos: 23},
				{Type: TokenString, Literal: "jesus", Pos: 25},
				{Type: TokenIdent, Literal: "and", Pos: 33},
				{Type: TokenIdent, Literal: "change_type", Pos: 37},
				{Type: TokenEq, Literal: "=", Pos: 49},
				{Type: TokenString, Literal: "refactored", Pos: 51},
				{Type: TokenEOF, Pos: 63},
			},
		},
		{
			name:  "PRD example 5: contains",
			input: "notes where tags contains 'decision'",
			want: []Token{
				{Type: TokenIdent, Literal: "notes", Pos: 0},
				{Type: TokenIdent, Literal: "where", Pos: 6},
				{Type: TokenIdent, Literal: "tags", Pos: 12},
				{Type: TokenIdent, Literal: "contains", Pos: 17},
				{Type: TokenString, Literal: "decision", Pos: 26},
				{Type: TokenEOF, Pos: 36},
			},
		},
		{
			name:  "all comparison operators",
			input: "a = 1 b != 2 c > 3 d < 4 e >= 5 f <= 6",
			want: []Token{
				{Type: TokenIdent, Literal: "a", Pos: 0},
				{Type: TokenEq, Literal: "=", Pos: 2},
				{Type: TokenNumber, Literal: "1", Pos: 4},
				{Type: TokenIdent, Literal: "b", Pos: 6},
				{Type: TokenNeq, Literal: "!=", Pos: 8},
				{Type: TokenNumber, Literal: "2", Pos: 11},
				{Type: TokenIdent, Literal: "c", Pos: 13},
				{Type: TokenGt, Literal: ">", Pos: 15},
				{Type: TokenNumber, Literal: "3", Pos: 17},
				{Type: TokenIdent, Literal: "d", Pos: 19},
				{Type: TokenLt, Literal: "<", Pos: 21},
				{Type: TokenNumber, Literal: "4", Pos: 23},
				{Type: TokenIdent, Literal: "e", Pos: 25},
				{Type: TokenGte, Literal: ">=", Pos: 27},
				{Type: TokenNumber, Literal: "5", Pos: 30},
				{Type: TokenIdent, Literal: "f", Pos: 32},
				{Type: TokenLte, Literal: "<=", Pos: 34},
				{Type: TokenNumber, Literal: "6", Pos: 37},
				{Type: TokenEOF, Pos: 38},
			},
		},
		{
			name:  "dotted field",
			input: "a.b.c",
			want: []Token{
				{Type: TokenIdent, Literal: "a", Pos: 0},
				{Type: TokenDot, Literal: ".", Pos: 1},
				{Type: TokenIdent, Literal: "b", Pos: 2},
				{Type: TokenDot, Literal: ".", Pos: 3},
				{Type: TokenIdent, Literal: "c", Pos: 4},
				{Type: TokenEOF, Pos: 5},
			},
		},
		{
			name:    "unterminated string",
			input:   "nodes where type = 'module",
			wantErr: true,
		},
		{
			name:    "unexpected character",
			input:   "nodes where type @ 'x'",
			wantErr: true,
		},
		{
			name:    "lone bang",
			input:   "nodes ! 1",
			wantErr: true,
		},
		{
			name:  "empty input",
			input: "",
			want: []Token{
				{Type: TokenEOF, Pos: 0},
			},
		},
		{
			name:  "whitespace only",
			input: "   \t\n  ",
			want: []Token{
				{Type: TokenEOF, Pos: 7},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Tokenize(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(got) != len(tt.want) {
				t.Fatalf("token count: got %d, want %d\ngot:  %v", len(got), len(tt.want), got)
			}

			for i := range tt.want {
				if got[i].Type != tt.want[i].Type {
					t.Errorf("token[%d].Type: got %s, want %s", i, got[i].Type, tt.want[i].Type)
				}
				if got[i].Literal != tt.want[i].Literal {
					t.Errorf("token[%d].Literal: got %q, want %q", i, got[i].Literal, tt.want[i].Literal)
				}
				if got[i].Pos != tt.want[i].Pos {
					t.Errorf("token[%d].Pos: got %d, want %d", i, got[i].Pos, tt.want[i].Pos)
				}
			}
		})
	}
}

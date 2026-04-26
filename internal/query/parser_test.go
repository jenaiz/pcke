package query

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Query
		wantErr error
	}{
		{
			name:  "bare collection",
			input: "nodes",
			want:  Query{Collection: "nodes"},
		},
		{
			name:  "PRD example 1",
			input: "nodes where type = 'module' and stability > 0.7",
			want: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "type", Operator: OpEq, Value: "module"},
						{Field: "stability", Operator: OpGt, Value: 0.7},
					},
					Operators: []LogicalOp{LogicalAnd},
				},
			},
		},
		{
			name:  "PRD example 2",
			input: "nodes where module = 'api' order by updated_at desc limit 10",
			want: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "module", Operator: OpEq, Value: "api"},
					},
				},
				OrderBy: &OrderClause{Field: "updated_at", Direction: SortDesc},
				Limit:   10,
			},
		},
		{
			name:  "PRD example 3",
			input: "constraints where scope = 'global' and severity = 'must'",
			want: Query{
				Collection: "constraints",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "scope", Operator: OpEq, Value: "global"},
						{Field: "severity", Operator: OpEq, Value: "must"},
					},
					Operators: []LogicalOp{LogicalAnd},
				},
			},
		},
		{
			name:  "PRD example 4",
			input: "evolution where author = 'jesus' and change_type = 'refactored'",
			want: Query{
				Collection: "evolution",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "author", Operator: OpEq, Value: "jesus"},
						{Field: "change_type", Operator: OpEq, Value: "refactored"},
					},
					Operators: []LogicalOp{LogicalAnd},
				},
			},
		},
		{
			name:  "PRD example 5",
			input: "notes where tags contains 'decision'",
			want: Query{
				Collection: "notes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "tags", Operator: OpContains, Value: "decision"},
					},
				},
			},
		},
		{
			name:  "OR condition",
			input: "nodes where type = 'module' or type = 'file'",
			want: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "type", Operator: OpEq, Value: "module"},
						{Field: "type", Operator: OpEq, Value: "file"},
					},
					Operators: []LogicalOp{LogicalOr},
				},
			},
		},
		{
			name:  "matches operator",
			input: "nodes where name matches '^api_.*'",
			want: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "name", Operator: OpMatches, Value: "^api_.*"},
					},
				},
			},
		},
		{
			name:  "boolean value",
			input: "relations where type = 'import'",
			want: Query{
				Collection: "relations",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "type", Operator: OpEq, Value: "import"},
					},
				},
			},
		},
		{
			name:  "order by asc default",
			input: "nodes order by name",
			want: Query{
				Collection: "nodes",
				OrderBy:    &OrderClause{Field: "name", Direction: SortAsc},
			},
		},
		{
			name:  "order by asc explicit",
			input: "nodes order by name asc",
			want: Query{
				Collection: "nodes",
				OrderBy:    &OrderClause{Field: "name", Direction: SortAsc},
			},
		},
		{
			name:  "limit only",
			input: "nodes limit 5",
			want: Query{
				Collection: "nodes",
				Limit:      5,
			},
		},
		{
			name:  "case insensitive keywords",
			input: "NODES WHERE type = 'module' ORDER BY name DESC LIMIT 5",
			want: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "type", Operator: OpEq, Value: "module"},
					},
				},
				OrderBy: &OrderClause{Field: "name", Direction: SortDesc},
				Limit:   5,
			},
		},
		{
			name:    "unknown collection",
			input:   "foobar where x = 1",
			wantErr: ErrUnknownCollection,
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: ErrSyntax,
		},
		{
			name:    "missing value",
			input:   "nodes where type =",
			wantErr: ErrSyntax,
		},
		{
			name:    "missing field",
			input:   "nodes where = 'x'",
			wantErr: ErrSyntax,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error wrapping %v, got nil", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error wrapping %v, got: %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			assertQueryEqual(t, tt.want, *got)
		})
	}
}

func assertQueryEqual(t *testing.T, want, got Query) {
	t.Helper()

	if got.Collection != want.Collection {
		t.Errorf("Collection: got %q, want %q", got.Collection, want.Collection)
	}

	if got.Limit != want.Limit {
		t.Errorf("Limit: got %d, want %d", got.Limit, want.Limit)
	}

	assertWhereEqual(t, want.Where, got.Where)
	assertOrderByEqual(t, want.OrderBy, got.OrderBy)
}

func assertWhereEqual(t *testing.T, want, got *WhereClause) {
	t.Helper()

	if (want == nil) != (got == nil) {
		t.Fatalf("Where: got nil=%v, want nil=%v", got == nil, want == nil)
	}
	if want == nil {
		return
	}

	if len(got.Conditions) != len(want.Conditions) {
		t.Fatalf("Where.Conditions count: got %d, want %d", len(got.Conditions), len(want.Conditions))
	}
	for i := range want.Conditions {
		wc := want.Conditions[i]
		gc := got.Conditions[i]
		if gc.Field != wc.Field {
			t.Errorf("Condition[%d].Field: got %q, want %q", i, gc.Field, wc.Field)
		}
		if gc.Operator != wc.Operator {
			t.Errorf("Condition[%d].Operator: got %s, want %s", i, gc.Operator, wc.Operator)
		}
		if gc.Value != wc.Value {
			t.Errorf("Condition[%d].Value: got %v (%T), want %v (%T)", i, gc.Value, gc.Value, wc.Value, wc.Value)
		}
	}
	if len(got.Operators) != len(want.Operators) {
		t.Fatalf("Where.Operators count: got %d, want %d", len(got.Operators), len(want.Operators))
	}
	for i := range want.Operators {
		if got.Operators[i] != want.Operators[i] {
			t.Errorf("Operator[%d]: got %s, want %s", i, got.Operators[i], want.Operators[i])
		}
	}
}

func assertOrderByEqual(t *testing.T, want, got *OrderClause) {
	t.Helper()

	if (want == nil) != (got == nil) {
		t.Fatalf("OrderBy: got nil=%v, want nil=%v", got == nil, want == nil)
	}
	if want == nil {
		return
	}

	if got.Field != want.Field {
		t.Errorf("OrderBy.Field: got %q, want %q", got.Field, want.Field)
	}
	if got.Direction != want.Direction {
		t.Errorf("OrderBy.Direction: got %s, want %s", got.Direction, want.Direction)
	}
}

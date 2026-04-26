package query

import (
	"errors"
	"testing"
)

func TestTypeCheck(t *testing.T) {
	tests := []struct {
		name    string
		query   Query
		wantErr error
	}{
		{
			name:  "valid: nodes bare",
			query: Query{Collection: "nodes"},
		},
		{
			name: "valid: nodes with equality",
			query: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "type", Operator: OpEq, Value: "module"},
					},
				},
			},
		},
		{
			name: "valid: number comparison",
			query: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "stability", Operator: OpGt, Value: 0.7},
					},
				},
			},
		},
		{
			name: "valid: contains on string_slice",
			query: Query{
				Collection: "notes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "tags", Operator: OpContains, Value: "decision"},
					},
				},
			},
		},
		{
			name: "valid: order by existing field",
			query: Query{
				Collection: "nodes",
				OrderBy:    &OrderClause{Field: "updated_at", Direction: SortDesc},
			},
		},
		{
			name:    "invalid: unknown collection",
			query:   Query{Collection: "foobar"},
			wantErr: ErrUnknownCollection,
		},
		{
			name: "invalid: unknown field",
			query: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "nonexistent", Operator: OpEq, Value: "x"},
					},
				},
			},
			wantErr: ErrUnknownField,
		},
		{
			name: "invalid: gt on string field",
			query: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "name", Operator: OpGt, Value: "x"},
					},
				},
			},
			wantErr: ErrIncompatibleOperator,
		},
		{
			name: "invalid: gt on bool field",
			query: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "status", Operator: OpGt, Value: "x"},
					},
				},
			},
			wantErr: ErrIncompatibleOperator,
		},
		{
			name: "invalid: contains on number",
			query: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "stability", Operator: OpContains, Value: "x"},
					},
				},
			},
			wantErr: ErrIncompatibleOperator,
		},
		{
			name: "invalid: matches on number",
			query: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "stability", Operator: OpMatches, Value: "x"},
					},
				},
			},
			wantErr: ErrIncompatibleOperator,
		},
		{
			name: "invalid: unknown order by field",
			query: Query{
				Collection: "nodes",
				OrderBy:    &OrderClause{Field: "nonexistent"},
			},
			wantErr: ErrUnknownField,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := TypeCheck(&tt.query)
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
		})
	}
}

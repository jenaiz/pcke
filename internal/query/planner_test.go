package query

import "testing"

func TestBuildPlan(t *testing.T) {
	tests := []struct {
		name     string
		query    Query
		wantStgy Strategy
		wantIdx  string
		wantKey  string
	}{
		{
			name:     "bare collection → full scan",
			query:    Query{Collection: "nodes"},
			wantStgy: FullScan,
		},
		{
			name: "equality on indexed field → index seek",
			query: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "module", Operator: OpEq, Value: "api"},
					},
				},
			},
			wantStgy: IndexSeek,
			wantIdx:  "by_module",
			wantKey:  "api",
		},
		{
			name: "equality on type → index seek",
			query: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "type", Operator: OpEq, Value: "module"},
					},
				},
			},
			wantStgy: IndexSeek,
			wantIdx:  "by_type",
			wantKey:  "module",
		},
		{
			name: "equality on file_path → index seek",
			query: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "file_path", Operator: OpEq, Value: "cmd/main.go"},
					},
				},
			},
			wantStgy: IndexSeek,
			wantIdx:  "by_file",
			wantKey:  "cmd/main.go",
		},
		{
			name: "equality on non-indexed field → full scan",
			query: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "name", Operator: OpEq, Value: "foo"},
					},
				},
			},
			wantStgy: FullScan,
		},
		{
			name: "range on indexed field → range scan",
			query: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "stability", Operator: OpGt, Value: 0.7},
					},
				},
			},
			wantStgy: FullScan, // stability is not indexed
		},
		{
			name: "OR conditions → full scan",
			query: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "module", Operator: OpEq, Value: "api"},
						{Field: "module", Operator: OpEq, Value: "web"},
					},
					Operators: []LogicalOp{LogicalOr},
				},
			},
			wantStgy: FullScan,
		},
		{
			name: "AND with indexed + non-indexed → index seek + filter",
			query: Query{
				Collection: "nodes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "type", Operator: OpEq, Value: "module"},
						{Field: "stability", Operator: OpGt, Value: 0.7},
					},
					Operators: []LogicalOp{LogicalAnd},
				},
			},
			wantStgy: IndexSeek,
			wantIdx:  "by_type",
			wantKey:  "module",
		},
		{
			name: "notes tags → index seek",
			query: Query{
				Collection: "notes",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "tags", Operator: OpEq, Value: "decision"},
					},
				},
			},
			wantStgy: IndexSeek,
			wantIdx:  "by_tag",
			wantKey:  "decision",
		},
		{
			name: "evolution has no indexes → full scan",
			query: Query{
				Collection: "evolution",
				Where: &WhereClause{
					Conditions: []Condition{
						{Field: "author", Operator: OpEq, Value: "jesus"},
					},
				},
			},
			wantStgy: FullScan,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := BuildPlan(&tt.query)

			if plan.Strategy != tt.wantStgy {
				t.Errorf("Strategy: got %s, want %s", plan.Strategy, tt.wantStgy)
			}
			if plan.IndexName != tt.wantIdx {
				t.Errorf("IndexName: got %q, want %q", plan.IndexName, tt.wantIdx)
			}
			if plan.IndexKey != tt.wantKey {
				t.Errorf("IndexKey: got %q, want %q", plan.IndexKey, tt.wantKey)
			}
			if plan.Collection != tt.query.Collection {
				t.Errorf("Collection: got %q, want %q", plan.Collection, tt.query.Collection)
			}
		})
	}
}

func TestBuildPlan_FiltersCount(t *testing.T) {
	// AND with indexed + non-indexed: the indexed condition is kept in
	// filters (executor does not yet implement native IndexSeek).
	q := Query{
		Collection: "nodes",
		Where: &WhereClause{
			Conditions: []Condition{
				{Field: "type", Operator: OpEq, Value: "module"},
				{Field: "stability", Operator: OpGt, Value: 0.7},
				{Field: "name", Operator: OpEq, Value: "foo"},
			},
			Operators: []LogicalOp{LogicalAnd, LogicalAnd},
		},
	}

	plan := BuildPlan(&q)
	if len(plan.Filters) != 3 {
		t.Errorf("Filters count: got %d, want 3", len(plan.Filters))
	}
}

func TestExplain(t *testing.T) {
	plan := &Plan{
		Collection: "nodes",
		Strategy:   IndexSeek,
		IndexName:  "by_type",
		IndexKey:   "module",
		Filters: []Condition{
			{Field: "stability", Operator: OpGt, Value: 0.7},
		},
		OrderBy: &OrderClause{Field: "updated_at", Direction: SortDesc},
		Limit:   10,
	}

	out := Explain(plan)
	if out == "" {
		t.Fatal("Explain returned empty string")
	}

	// Verify key components are present.
	for _, want := range []string{
		"Collection: nodes",
		"Strategy:   index_seek",
		"Index:      by_type",
		"Seek key:   module",
		"stability > 0.7",
		"Order by:   updated_at desc",
		"Limit:      10",
	} {
		if !containsStr(out, want) {
			t.Errorf("Explain output missing %q\ngot:\n%s", want, out)
		}
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && searchStr(s, sub)
}

func searchStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

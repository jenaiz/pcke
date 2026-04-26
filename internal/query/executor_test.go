package query

import "testing"

func TestMatchesFilters(t *testing.T) {
	tests := []struct {
		name       string
		row        Row
		conditions []Condition
		operators  []LogicalOp
		want       bool
	}{
		{
			name: "no conditions",
			row:  Row{"type": "module"},
			want: true,
		},
		{
			name: "single eq match",
			row:  Row{"type": "module"},
			conditions: []Condition{
				{Field: "type", Operator: OpEq, Value: "module"},
			},
			want: true,
		},
		{
			name: "single eq no match",
			row:  Row{"type": "file"},
			conditions: []Condition{
				{Field: "type", Operator: OpEq, Value: "module"},
			},
			want: false,
		},
		{
			name: "AND both match",
			row:  Row{"type": "module", "stability": 0.8},
			conditions: []Condition{
				{Field: "type", Operator: OpEq, Value: "module"},
				{Field: "stability", Operator: OpGt, Value: 0.7},
			},
			operators: []LogicalOp{LogicalAnd},
			want:      true,
		},
		{
			name: "AND one fails",
			row:  Row{"type": "module", "stability": 0.5},
			conditions: []Condition{
				{Field: "type", Operator: OpEq, Value: "module"},
				{Field: "stability", Operator: OpGt, Value: 0.7},
			},
			operators: []LogicalOp{LogicalAnd},
			want:      false,
		},
		{
			name: "OR first matches",
			row:  Row{"type": "module"},
			conditions: []Condition{
				{Field: "type", Operator: OpEq, Value: "module"},
				{Field: "type", Operator: OpEq, Value: "file"},
			},
			operators: []LogicalOp{LogicalOr},
			want:      true,
		},
		{
			name: "OR second matches",
			row:  Row{"type": "file"},
			conditions: []Condition{
				{Field: "type", Operator: OpEq, Value: "module"},
				{Field: "type", Operator: OpEq, Value: "file"},
			},
			operators: []LogicalOp{LogicalOr},
			want:      true,
		},
		{
			name: "OR none matches",
			row:  Row{"type": "dir"},
			conditions: []Condition{
				{Field: "type", Operator: OpEq, Value: "module"},
				{Field: "type", Operator: OpEq, Value: "file"},
			},
			operators: []LogicalOp{LogicalOr},
			want:      false,
		},
		{
			name: "AND/OR precedence: a OR b AND c",
			row:  Row{"type": "module", "name": "foo", "status": "active"},
			conditions: []Condition{
				{Field: "type", Operator: OpEq, Value: "module"}, // true
				{Field: "name", Operator: OpEq, Value: "bar"},    // false
				{Field: "status", Operator: OpEq, Value: "dead"}, // false
			},
			operators: []LogicalOp{LogicalOr, LogicalAnd},
			want:      true, // module matches first OR group
		},
		{
			name: "neq",
			row:  Row{"type": "file"},
			conditions: []Condition{
				{Field: "type", Operator: OpNeq, Value: "module"},
			},
			want: true,
		},
		{
			name: "gte",
			row:  Row{"stability": 0.7},
			conditions: []Condition{
				{Field: "stability", Operator: OpGte, Value: 0.7},
			},
			want: true,
		},
		{
			name: "lt",
			row:  Row{"stability": 0.5},
			conditions: []Condition{
				{Field: "stability", Operator: OpLt, Value: 0.7},
			},
			want: true,
		},
		{
			name: "lte",
			row:  Row{"stability": 0.7},
			conditions: []Condition{
				{Field: "stability", Operator: OpLte, Value: 0.7},
			},
			want: true,
		},
		{
			name: "contains on string",
			row:  Row{"name": "my_api_service"},
			conditions: []Condition{
				{Field: "name", Operator: OpContains, Value: "api"},
			},
			want: true,
		},
		{
			name: "contains on slice",
			row:  Row{"tags": []any{"decision", "architecture"}},
			conditions: []Condition{
				{Field: "tags", Operator: OpContains, Value: "decision"},
			},
			want: true,
		},
		{
			name: "contains on slice no match",
			row:  Row{"tags": []any{"architecture"}},
			conditions: []Condition{
				{Field: "tags", Operator: OpContains, Value: "decision"},
			},
			want: false,
		},
		{
			name: "matches regex",
			row:  Row{"name": "api_gateway"},
			conditions: []Condition{
				{Field: "name", Operator: OpMatches, Value: "^api_.*"},
			},
			want: true,
		},
		{
			name: "matches regex no match",
			row:  Row{"name": "web_server"},
			conditions: []Condition{
				{Field: "name", Operator: OpMatches, Value: "^api_.*"},
			},
			want: false,
		},
		{
			name: "missing field",
			row:  Row{"type": "module"},
			conditions: []Condition{
				{Field: "nonexistent", Operator: OpEq, Value: "x"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesFilters(tt.row, tt.conditions, tt.operators)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSortRows(t *testing.T) {
	rows := []Row{
		{"name": "charlie", "stability": 0.5},
		{"name": "alice", "stability": 0.9},
		{"name": "bob", "stability": 0.7},
	}

	t.Run("asc by name", func(t *testing.T) {
		r := make([]Row, len(rows))
		copy(r, rows)
		sortRows(r, &OrderClause{Field: "name", Direction: SortAsc})
		names := []string{r[0]["name"].(string), r[1]["name"].(string), r[2]["name"].(string)}
		want := []string{"alice", "bob", "charlie"}
		for i := range want {
			if names[i] != want[i] {
				t.Errorf("pos %d: got %q, want %q", i, names[i], want[i])
			}
		}
	})

	t.Run("desc by stability", func(t *testing.T) {
		r := make([]Row, len(rows))
		copy(r, rows)
		sortRows(r, &OrderClause{Field: "stability", Direction: SortDesc})
		vals := []float64{r[0]["stability"].(float64), r[1]["stability"].(float64), r[2]["stability"].(float64)}
		want := []float64{0.9, 0.7, 0.5}
		for i := range want {
			if vals[i] != want[i] {
				t.Errorf("pos %d: got %v, want %v", i, vals[i], want[i])
			}
		}
	})
}

func TestDeserializeRow(t *testing.T) {
	data := []byte(`{"id":"kn:1","type":"module","stability":0.8}`)
	row, err := deserializeRow(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row["id"] != "kn:1" {
		t.Errorf("id: got %v, want kn:1", row["id"])
	}
	if row["type"] != "module" {
		t.Errorf("type: got %v, want module", row["type"])
	}
	if row["stability"] != 0.8 {
		t.Errorf("stability: got %v, want 0.8", row["stability"])
	}
}

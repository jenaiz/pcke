package query

import (
	"fmt"
	"testing"
)

// BenchmarkCriticalParse benchmarks the full parse pipeline (lex + parse)
// for representative queries.
func BenchmarkCriticalParse(b *testing.B) {
	queries := []string{
		"nodes",
		"nodes where type = 'module' and stability > 0.7",
		"nodes where module = 'api' order by updated_at desc limit 10",
		"constraints where scope = 'global' and severity = 'must'",
		"evolution where author = 'jesus' and change_type = 'refactored'",
		"notes where tags contains 'decision'",
	}

	for _, q := range queries {
		b.Run(q, func(b *testing.B) {
			for range b.N {
				parsed, err := Parse(q)
				if err != nil {
					b.Fatal(err)
				}
				_ = parsed
			}
		})
	}
}

// BenchmarkCriticalTypeCheck benchmarks type checking on pre-parsed queries.
func BenchmarkCriticalTypeCheck(b *testing.B) {
	queries := []struct {
		name string
		q    *Query
	}{
		{
			name: "simple",
			q:    mustParse("nodes where type = 'module'"),
		},
		{
			name: "complex",
			q:    mustParse("nodes where type = 'module' and stability > 0.7 order by updated_at desc limit 10"),
		},
	}

	for _, tc := range queries {
		b.Run(tc.name, func(b *testing.B) {
			for range b.N {
				if err := TypeCheck(tc.q); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkCriticalBuildPlan benchmarks plan construction.
func BenchmarkCriticalBuildPlan(b *testing.B) {
	queries := []struct {
		name string
		q    *Query
	}{
		{
			name: "full_scan",
			q:    mustParse("nodes where name = 'foo'"),
		},
		{
			name: "index_seek",
			q:    mustParse("nodes where module = 'api'"),
		},
		{
			name: "complex",
			q:    mustParse("nodes where type = 'module' and stability > 0.7 order by updated_at desc limit 10"),
		},
	}

	for _, tc := range queries {
		b.Run(tc.name, func(b *testing.B) {
			for range b.N {
				plan := BuildPlan(tc.q)
				_ = plan
			}
		})
	}
}

// BenchmarkCriticalMatchFilters benchmarks filter evaluation at different scales.
func BenchmarkCriticalMatchFilters(b *testing.B) {
	sizes := []int{1000, 10000, 100000}

	for _, n := range sizes {
		rows := generateRows(n)
		conditions := []Condition{
			{Field: "type", Operator: OpEq, Value: "module"},
			{Field: "stability", Operator: OpGt, Value: 0.7},
		}
		operators := []LogicalOp{LogicalAnd}

		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for range b.N {
				count := 0
				for _, row := range rows {
					if matchesFilters(row, conditions, operators) {
						count++
					}
				}
				_ = count
			}
		})
	}
}

// BenchmarkCriticalSort benchmarks sorting at different scales.
func BenchmarkCriticalSort(b *testing.B) {
	sizes := []int{1000, 10000, 100000}
	ob := &OrderClause{Field: "stability", Direction: SortDesc}

	for _, n := range sizes {
		rows := generateRows(n)

		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for range b.N {
				r := make([]Row, len(rows))
				copy(r, rows)
				sortRows(r, ob)
			}
		})
	}
}

func generateRows(n int) []Row {
	types := []string{"module", "file", "function", "class", "interface"}
	rows := make([]Row, n)
	for i := range n {
		rows[i] = Row{
			"id":        fmt.Sprintf("kn:%d", i),
			"type":      types[i%len(types)],
			"name":      fmt.Sprintf("node_%d", i),
			"module":    fmt.Sprintf("mod_%d", i%10),
			"stability": float64(i%100) / 100.0,
			"status":    "active",
		}
	}
	return rows
}

func mustParse(input string) *Query {
	q, err := Parse(input)
	if err != nil {
		panic(err)
	}
	return q
}

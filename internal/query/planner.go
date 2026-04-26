package query

import (
	"fmt"
	"strings"
)

// Strategy describes the access method the planner selects for a query.
type Strategy int

const (
	// FullScan reads all records in the collection, applying filters post-read.
	FullScan Strategy = iota

	// IndexSeek uses a secondary index to look up records matching an exact
	// value, then applies remaining filters.
	IndexSeek

	// RangeScan uses an ordered index for inequality conditions on sortable
	// fields. Falls back to FullScan with filtering in the current executor.
	RangeScan
)

// String returns a human-readable name for the strategy.
func (s Strategy) String() string {
	switch s {
	case FullScan:
		return "full_scan"
	case IndexSeek:
		return "index_seek"
	case RangeScan:
		return "range_scan"
	default:
		return fmt.Sprintf("Strategy(%d)", int(s))
	}
}

// Plan represents a compiled query execution plan. It captures the planner's
// decisions (which index to use, scan direction, filters) so the executor can
// run the query without re-analyzing the AST. Plans are immutable once created.
type Plan struct {
	Collection string
	Strategy   Strategy
	IndexName  string      // secondary index name (empty for FullScan)
	IndexKey   string      // seek key for IndexSeek
	Filters    []Condition // conditions applied as post-scan filters
	Operators  []LogicalOp // logical ops between filters
	OrderBy    *OrderClause
	Limit      int
}

// indexedFields maps (collection, field) to the secondary index name.
// Only equality conditions on these fields can use IndexSeek.
var indexedFields = map[string]map[string]string{
	"nodes": {
		"module":    "by_module",
		"file_path": "by_file",
		"type":      "by_type",
	},
	"notes": {
		"tags": "by_tag",
	},
}

// BuildPlan creates an execution plan for a validated query. The query must
// have passed TypeCheck before calling BuildPlan.
//
// The planner prefers specificity: IndexSeek > RangeScan > FullScan.
// For AND conditions, it picks the most selective indexed field and pushes
// remaining conditions into post-scan filters. For OR conditions, it falls
// back to FullScan since any branch could produce results.
func BuildPlan(q *Query) *Plan {
	plan := &Plan{
		Collection: q.Collection,
		Strategy:   FullScan,
		OrderBy:    q.OrderBy,
		Limit:      q.Limit,
	}

	if q.Where == nil {
		return plan
	}

	// Check if all operators are AND (no OR). OR conditions require FullScan
	// because each OR branch could independently match records.
	allAnd := true
	for _, op := range q.Where.Operators {
		if op == LogicalOr {
			allAnd = false
			break
		}
	}

	if !allAnd {
		plan.Filters = q.Where.Conditions
		plan.Operators = q.Where.Operators
		return plan
	}

	// All conditions are AND-connected. Find the best indexed condition.
	idxMap := indexedFields[q.Collection]
	bestIdx := -1

	for i, cond := range q.Where.Conditions {
		if cond.Operator == OpEq {
			if idxName, ok := idxMap[cond.Field]; ok {
				bestIdx = i
				plan.Strategy = IndexSeek
				plan.IndexName = idxName
				plan.IndexKey = fmt.Sprintf("%v", cond.Value)
				break
			}
		}
	}

	// Check for range conditions on indexed fields (future: use RangeScan).
	if bestIdx == -1 {
		for i, cond := range q.Where.Conditions {
			if isRangeOp(cond.Operator) {
				if _, ok := idxMap[cond.Field]; ok {
					bestIdx = i
					plan.Strategy = RangeScan
					break
				}
			}
		}
	}

	// Build filter list: all conditions except the indexed one.
	for i, cond := range q.Where.Conditions {
		if i == bestIdx {
			continue
		}
		plan.Filters = append(plan.Filters, cond)
	}

	// Rebuild operators list for remaining filters.
	if len(plan.Filters) > 1 {
		plan.Operators = make([]LogicalOp, len(plan.Filters)-1)
		// All remaining are AND (we checked above).
	}

	return plan
}

// Explain returns a human-readable description of the execution plan.
func Explain(plan *Plan) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Collection: %s\n", plan.Collection)
	fmt.Fprintf(&b, "Strategy:   %s\n", plan.Strategy)

	if plan.IndexName != "" {
		fmt.Fprintf(&b, "Index:      %s\n", plan.IndexName)
	}
	if plan.IndexKey != "" {
		fmt.Fprintf(&b, "Seek key:   %s\n", plan.IndexKey)
	}

	if len(plan.Filters) > 0 {
		fmt.Fprintf(&b, "Filters:    ")
		for i, f := range plan.Filters {
			if i > 0 && i-1 < len(plan.Operators) {
				fmt.Fprintf(&b, " %s ", plan.Operators[i-1])
			}
			fmt.Fprintf(&b, "%s %s %v", f.Field, f.Operator, f.Value)
		}
		fmt.Fprintln(&b)
	}

	if plan.OrderBy != nil {
		fmt.Fprintf(&b, "Order by:   %s %s\n", plan.OrderBy.Field, plan.OrderBy.Direction)
	}
	if plan.Limit > 0 {
		fmt.Fprintf(&b, "Limit:      %d\n", plan.Limit)
	}

	return b.String()
}

func isRangeOp(op Operator) bool {
	return op == OpGt || op == OpLt || op == OpGte || op == OpLte
}

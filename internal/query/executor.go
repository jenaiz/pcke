package query

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// Row represents a single query result as a generic key-value map.
// Keys are field names from the collection schema.
type Row map[string]any

// ResultSet holds the output of a query execution.
type ResultSet struct {
	Collection string
	Rows       []Row
}

// collectionPrefixes maps collection names to their kdb key prefixes.
var collectionPrefixes = map[string]string{
	"nodes":       "kn:",
	"evolution":   "el:",
	"constraints": "ct:",
	"notes":       "nt:",
	"relations":   "rel:",
}

// Execute runs a query plan against the database and returns matching rows.
// The plan must have been produced by BuildPlan on a type-checked query.
//
// The executor uses cursor-based prefix scanning within a snapshot-isolated
// View transaction, applying filters, sorting, and LIMIT post-scan.
func Execute(ctx context.Context, db *kdb.DB, plan *Plan) (*ResultSet, error) {
	prefix, ok := collectionPrefixes[plan.Collection]
	if !ok {
		return nil, fmt.Errorf("query: execute: unknown collection %q", plan.Collection)
	}

	var rows []Row

	if err := db.View(ctx, func(rtx *tx.ReadTx) error {
		var err error
		rows, err = scanCollection(rtx, []byte(prefix), plan)
		return err
	}); err != nil {
		return nil, fmt.Errorf("query: execute: %w", err)
	}

	// Sort if needed. Sorting happens outside the transaction to minimize
	// the time spent holding the snapshot.
	if plan.OrderBy != nil {
		sortRows(rows, plan.OrderBy)
	}

	// Apply limit after sorting.
	if plan.Limit > 0 && len(rows) > plan.Limit {
		rows = rows[:plan.Limit]
	}

	return &ResultSet{Collection: plan.Collection, Rows: rows}, nil
}

// scanCollection iterates over all records with the given prefix, deserializes
// them, and applies the plan's filter conditions.
func scanCollection(rtx *tx.ReadTx, prefix []byte, plan *Plan) ([]Row, error) {
	var rows []Row

	c := rtx.Cursor()
	if !c.Seek(prefix) {
		return nil, nil
	}

	for c.Valid() {
		key := c.Key()
		if !bytes.HasPrefix(key, prefix) {
			break
		}

		val := c.Value()
		row, err := deserializeRow(val)
		if err != nil {
			// Skip unparseable records rather than failing the entire query.
			if !c.Next() {
				break
			}
			continue
		}

		if matchesFilters(row, plan.Filters, plan.Operators) {
			rows = append(rows, row)
		}

		if !c.Next() {
			break
		}
	}

	return rows, nil
}

// deserializeRow unmarshals a JSON-encoded kdb value into a Row map.
func deserializeRow(data []byte) (Row, error) {
	var row Row
	if err := json.Unmarshal(data, &row); err != nil {
		return nil, err
	}
	return row, nil
}

// matchesFilters evaluates the filter conditions against a row.
// Conditions are connected by logical operators (AND/OR).
func matchesFilters(row Row, conditions []Condition, operators []LogicalOp) bool {
	if len(conditions) == 0 {
		return true
	}

	// Evaluate left-to-right, respecting AND/OR precedence.
	// AND binds tighter than OR: a OR b AND c = a OR (b AND c).
	// We implement this by grouping AND-connected conditions and
	// OR-ing the groups.
	type group struct {
		conditions []Condition
	}

	var groups []group
	current := group{conditions: []Condition{conditions[0]}}

	for i, op := range operators {
		if op == LogicalOr {
			groups = append(groups, current)
			current = group{conditions: []Condition{conditions[i+1]}}
		} else {
			current.conditions = append(current.conditions, conditions[i+1])
		}
	}
	groups = append(groups, current)

	// OR: any group matching is enough.
	for _, g := range groups {
		allMatch := true
		for _, cond := range g.conditions {
			if !evalCondition(row, cond) {
				allMatch = false
				break
			}
		}
		if allMatch {
			return true
		}
	}

	return false
}

// evalCondition evaluates a single condition against a row.
func evalCondition(row Row, cond Condition) bool {
	val, ok := row[cond.Field]
	if !ok {
		return false
	}

	switch cond.Operator {
	case OpEq:
		return compareValues(val, cond.Value) == 0
	case OpNeq:
		return compareValues(val, cond.Value) != 0
	case OpGt:
		return compareValues(val, cond.Value) > 0
	case OpLt:
		return compareValues(val, cond.Value) < 0
	case OpGte:
		return compareValues(val, cond.Value) >= 0
	case OpLte:
		return compareValues(val, cond.Value) <= 0
	case OpContains:
		return evalContains(val, cond.Value)
	case OpMatches:
		return evalMatches(val, cond.Value)
	}

	return false
}

// compareValues compares two values. Returns -1, 0, or 1.
// JSON numbers are float64, strings are compared lexicographically.
func compareValues(a, b any) int {
	// Coerce both to the same type for comparison.
	af, aOk := toFloat64(a)
	bf, bOk := toFloat64(b)
	if aOk && bOk {
		switch {
		case af < bf:
			return -1
		case af > bf:
			return 1
		default:
			return 0
		}
	}

	as := fmt.Sprintf("%v", a)
	bs := fmt.Sprintf("%v", b)
	return strings.Compare(as, bs)
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

// evalContains checks string containment or slice membership.
func evalContains(fieldVal, queryVal any) bool {
	qStr := fmt.Sprintf("%v", queryVal)

	// Check if field is a slice (tags, etc.).
	if slice, ok := fieldVal.([]any); ok {
		for _, elem := range slice {
			if fmt.Sprintf("%v", elem) == qStr {
				return true
			}
		}
		return false
	}

	// String containment.
	return strings.Contains(fmt.Sprintf("%v", fieldVal), qStr)
}

// evalMatches checks regex match.
func evalMatches(fieldVal, queryVal any) bool {
	pattern := fmt.Sprintf("%v", queryVal)
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(fmt.Sprintf("%v", fieldVal))
}

// sortRows sorts rows by the ORDER BY clause.
func sortRows(rows []Row, ob *OrderClause) {
	sort.SliceStable(rows, func(i, j int) bool {
		a := rows[i][ob.Field]
		b := rows[j][ob.Field]
		cmp := compareValues(a, b)
		if ob.Direction == SortDesc {
			return cmp > 0
		}
		return cmp < 0
	})
}

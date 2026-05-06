package query

import "time"

// Query represents a parsed DSL query. It is the root AST node produced by
// the parser and consumed by the type checker and planner.
type Query struct {
	Collection string       // "nodes", "evolution", "constraints", "notes", "relations"
	Where      *WhereClause // nil if no WHERE clause
	OrderBy    *OrderClause // nil if no ORDER BY clause
	Limit      int          // 0 means no limit

	// AsOf, if non-nil, pins the query to a specific point in time.
	// Reads return the version of every record that was active at AsOf;
	// records created after AsOf are excluded. Surface-level support
	// only in this commit — the executor wires AsOf to event.Store.AsOf
	// in F12.T4 commit 3. See PRD v5.2 §3.5.
	AsOf *time.Time
}

// WhereClause represents the WHERE part of a query.
//
// Two mutually-exclusive forms:
//
//   - Conditions + Operators: classic field-op-value predicates joined
//     by AND/OR. Operators[i] joins Conditions[i] and Conditions[i+1].
//   - Traverse: a graph-traversal restriction on the candidate set.
//     When Traverse != nil, Conditions is empty.
//
// Combining the two (TRAVERSE plus filter conditions) is intentionally
// out of scope for v0.10.0; the parser rejects mixed forms with
// ErrSyntax. Future work can lift the restriction by treating Traverse
// as one extra implicit condition AND'd with the others.
type WhereClause struct {
	Conditions []Condition
	Operators  []LogicalOp // len(Operators) == len(Conditions) - 1
	Traverse   *TraverseExpr
}

// TraverseExpr is the parsed form of WHERE TRAVERSE(...) FROM '<startkey>'.
//
// Surface-level support only in F12.T4.2 — the executor wires the
// expression to the graph package in F12.T4.3.
type TraverseExpr struct {
	// StartKey is the typed reference the traversal begins at, taken
	// from the FROM '<key>' literal (e.g. "e:internal/kdb/db.go").
	StartKey string

	// EdgeName is the positional first argument inside the parens. We
	// accept any identifier ("edges", "rel", "links", ...) for forward-
	// compatibility but treat it opaquely; the typed-event log already
	// stores edges in a single namespace.
	EdgeName string

	// Depth is the MaxDepth bound. Zero (the default) is mapped to
	// graph.DefaultMaxDepth by the executor.
	Depth int

	// EdgeType is the inclusion filter. Empty = all edge types.
	EdgeType string

	// Direction is "forward", "reverse", or "both". Empty = "forward"
	// by convention; the executor maps to graph.Direction.
	Direction string
}

// LogicalOp is a logical conjunction between conditions.
type LogicalOp int

// Logical operators.
const (
	LogicalAnd LogicalOp = iota
	LogicalOr
)

// String returns "and" or "or".
func (op LogicalOp) String() string {
	if op == LogicalOr {
		return "or"
	}
	return "and"
}

// Condition represents a single filter predicate: field op value.
type Condition struct {
	Field    string
	Operator Operator
	Value    any // string, float64, or bool
}

// Operator represents a comparison operator in a condition.
type Operator int

// Comparison operators.
const (
	OpEq       Operator = iota // =
	OpNeq                      // !=
	OpGt                       // >
	OpLt                       // <
	OpGte                      // >=
	OpLte                      // <=
	OpContains                 // contains
	OpMatches                  // matches
)

// String returns the operator symbol.
func (op Operator) String() string {
	switch op {
	case OpEq:
		return "="
	case OpNeq:
		return "!="
	case OpGt:
		return ">"
	case OpLt:
		return "<"
	case OpGte:
		return ">="
	case OpLte:
		return "<="
	case OpContains:
		return "contains"
	case OpMatches:
		return "matches"
	default:
		return "?"
	}
}

// OrderClause represents ORDER BY field [ASC|DESC].
type OrderClause struct {
	Field     string
	Direction SortDirection
}

// SortDirection is ascending or descending.
type SortDirection int

// Sort direction constants.
const (
	SortAsc SortDirection = iota
	SortDesc
)

// String returns "asc" or "desc".
func (d SortDirection) String() string {
	if d == SortDesc {
		return "desc"
	}
	return "asc"
}

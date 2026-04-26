package query

import "fmt"

// TypeCheck validates a parsed Query against the collection schema. It verifies:
//   - The collection exists
//   - All referenced fields exist in the collection schema
//   - Operators are compatible with field types
//   - ORDER BY field exists
//
// Returns nil if the query is valid, or a descriptive error wrapping one of
// the sentinel errors (ErrUnknownCollection, ErrUnknownField, ErrIncompatibleOperator).
func TypeCheck(q *Query) error {
	schema := CollectionSchema(q.Collection)
	if schema == nil {
		return fmt.Errorf("%w: %s", ErrUnknownCollection, q.Collection)
	}

	if q.Where != nil {
		for _, cond := range q.Where.Conditions {
			if err := checkCondition(schema, q.Collection, cond); err != nil {
				return err
			}
		}
	}

	if q.OrderBy != nil {
		if _, ok := schema[q.OrderBy.Field]; !ok {
			return fmt.Errorf("%w: %s in collection %s", ErrUnknownField, q.OrderBy.Field, q.Collection)
		}
	}

	return nil
}

func checkCondition(schema Schema, coll string, cond Condition) error {
	ft, ok := schema[cond.Field]
	if !ok {
		return fmt.Errorf("%w: %s in collection %s", ErrUnknownField, cond.Field, coll)
	}

	return checkOperatorCompat(cond.Field, ft, cond.Operator)
}

// checkOperatorCompat ensures the operator makes sense for the field type.
//
// Rules:
//   - All types support = and !=
//   - FieldNumber and FieldTime support >, <, >=, <=
//   - FieldString and FieldTime support contains and matches
//   - FieldStringSlice supports contains (element membership)
//   - FieldBool only supports = and !=
func checkOperatorCompat(field string, ft FieldType, op Operator) error {
	switch op {
	case OpEq, OpNeq:
		return nil // universal

	case OpGt, OpLt, OpGte, OpLte:
		if ft == FieldNumber || ft == FieldTime {
			return nil
		}
		return fmt.Errorf("%w: %s on %s field %q", ErrIncompatibleOperator, op, fieldTypeName(ft), field)

	case OpContains:
		if ft == FieldString || ft == FieldTime || ft == FieldStringSlice {
			return nil
		}
		return fmt.Errorf("%w: contains on %s field %q", ErrIncompatibleOperator, fieldTypeName(ft), field)

	case OpMatches:
		if ft == FieldString || ft == FieldTime {
			return nil
		}
		return fmt.Errorf("%w: matches on %s field %q", ErrIncompatibleOperator, fieldTypeName(ft), field)
	}

	return nil
}

func fieldTypeName(ft FieldType) string {
	switch ft {
	case FieldString:
		return "string"
	case FieldNumber:
		return "number"
	case FieldBool:
		return "bool"
	case FieldTime:
		return "time"
	case FieldStringSlice:
		return "string_slice"
	default:
		return "unknown"
	}
}

package query

import "errors"

// ErrUnknownCollection is returned when a query references a collection
// that does not exist. Callers should surface this to the user as a
// syntax error, not retry.
var ErrUnknownCollection = errors.New("query: unknown collection")

// ErrUnknownField is returned when a query references a field that does not
// exist in the target collection's schema. Callers should surface this to
// the user as a syntax/validation error, not retry.
var ErrUnknownField = errors.New("query: unknown field")

// ErrIncompatibleOperator is returned when an operator is applied to a field
// whose type does not support that operator (e.g., ">" on a string field).
var ErrIncompatibleOperator = errors.New("query: incompatible operator for field type")

// ErrSyntax is returned for lexer or parser syntax errors in the query string.
var ErrSyntax = errors.New("query: syntax error")

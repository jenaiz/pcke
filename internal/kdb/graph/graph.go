package graph

import (
	"errors"
	"time"
)

// Direction identifies which side of a Link to follow during traversal.
type Direction int

const (
	// Forward follows edges from SrcRef to DstRef.
	Forward Direction = iota
	// Reverse follows edges from DstRef to SrcRef ("what depends on this?").
	Reverse
	// Both traverses Forward and Reverse in parallel; the visited set
	// keys on (ref, direction) so the same ref reached from each side
	// is counted twice rather than blocking the second visit.
	Both
)

// String returns the lowercase name of the direction.
func (d Direction) String() string {
	switch d {
	case Forward:
		return "forward"
	case Reverse:
		return "reverse"
	case Both:
		return "both"
	default:
		return "unknown"
	}
}

// Defaults for TraversalOptions when caller leaves them unset.
const (
	DefaultMaxDepth   = 5
	DefaultMaxVisited = 10_000
)

// TraversalOptions controls a graph walk. Zero values use the defaults
// above, which match the values documented in the package overview.
type TraversalOptions struct {
	// EdgeTypes is an inclusion filter. Empty list = all edge types.
	EdgeTypes []string

	// MaxDepth caps the BFS hop count. Zero or negative -> DefaultMaxDepth.
	MaxDepth int

	// MaxVisited bounds memory by failing fast once the visited set hits
	// this size. Zero or negative -> DefaultMaxVisited.
	MaxVisited int

	// Direction selects forward, reverse, or both.
	Direction Direction

	// AsOf, if non-nil, pins the traversal to a point in time: each link
	// version active at AsOf is used and superseded edges are skipped.
	// Nil means "current" (latest version of every link, ignoring those
	// whose lifecycle is Superseded today).
	AsOf *time.Time
}

// Sentinel errors returned by traversal functions.
var (
	// ErrVisitedCapExceeded is returned when a traversal would grow the
	// visited set beyond TraversalOptions.MaxVisited. The result returned
	// alongside the error is the partial set accumulated so far.
	ErrVisitedCapExceeded = errors.New("graph: visited cap exceeded")

	// ErrInvalidStart is returned when the supplied start reference is
	// empty or otherwise malformed.
	ErrInvalidStart = errors.New("graph: invalid start reference")

	// ErrUnknownDirection is returned for a Direction value outside the
	// known set.
	ErrUnknownDirection = errors.New("graph: unknown direction")
)

// Ref is a typed, version-less reference to an event: e.g.
// "e:internal/kdb/db.go" or "d:adr-0008".
//
// Refs are the unit of traversal: they ignore the supersedes chain on
// the entity itself and instead read the latest (or AsOf-current) link
// records to discover edges.
type Ref string

// String makes Ref satisfy fmt.Stringer.
func (r Ref) String() string { return string(r) }

// resolved returns sane values for the bounds + direction, applying
// defaults and validating the direction.
func (o TraversalOptions) resolved() (resolvedOpts, error) {
	r := resolvedOpts{
		edgeTypes:  o.EdgeTypes,
		maxDepth:   o.MaxDepth,
		maxVisited: o.MaxVisited,
		direction:  o.Direction,
		asOf:       o.AsOf,
	}
	if r.maxDepth <= 0 {
		r.maxDepth = DefaultMaxDepth
	}
	if r.maxVisited <= 0 {
		r.maxVisited = DefaultMaxVisited
	}
	switch r.direction {
	case Forward, Reverse, Both:
	default:
		return resolvedOpts{}, ErrUnknownDirection
	}
	return r, nil
}

// resolvedOpts is the package-internal post-defaults form of
// TraversalOptions.
type resolvedOpts struct {
	edgeTypes  []string
	maxDepth   int
	maxVisited int
	direction  Direction
	asOf       *time.Time
}

// edgeAllowed reports whether the given edge type passes the inclusion
// filter. An empty filter accepts all edge types.
func (o resolvedOpts) edgeAllowed(edgeType string) bool {
	if len(o.edgeTypes) == 0 {
		return true
	}
	for _, allowed := range o.edgeTypes {
		if allowed == edgeType {
			return true
		}
	}
	return false
}

// validateStart fails for empty refs (the only structural invariant we
// can check without reading the database).
func validateStart(start Ref) error {
	if start == "" {
		return ErrInvalidStart
	}
	return nil
}

// Package event provides the typed-event log primitives for kdb.
//
// Events are immutable, versioned records of one of five kinds:
// Entity, Decision, Observation, Outcome, Link. Each event has a key
// of the form "<prefix>:<id>:v<N>" with monotonic version numbers,
// and a value carrying header metadata (version, supersedes pointer,
// created_at, lifecycle) plus kind-specific fields.
//
// Updates are append-only: a new version is written with a Supersedes
// pointer to the prior version's key. The "current" view of an entity
// is the highest-version record whose lifecycle is not superseded or
// historical.
//
// This file defines the kind enum and prefix mapping.
package event

// Kind identifies the type of an event record.
//
// Wire-stable: values are persisted in the event_kind field of every
// record. Do not renumber existing kinds.
type Kind uint8

const (
	// KindEntity is a code unit: file, function, type, module.
	KindEntity Kind = 1
	// KindDecision is a typed assertion about code with severity and scope.
	KindDecision Kind = 2
	// KindObservation is an agent or scanner action (Phase 14).
	KindObservation Kind = 3
	// KindOutcome is a derived event (Phase 14).
	KindOutcome Kind = 4
	// KindLink is a first-class edge between two typed references.
	KindLink Kind = 5
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	switch k {
	case KindEntity, KindDecision, KindObservation, KindOutcome, KindLink:
		return true
	default:
		return false
	}
}

// String returns the lowercase name of the kind.
func (k Kind) String() string {
	switch k {
	case KindEntity:
		return "entity"
	case KindDecision:
		return "decision"
	case KindObservation:
		return "observation"
	case KindOutcome:
		return "outcome"
	case KindLink:
		return "link"
	default:
		return "unknown"
	}
}

// Prefix returns the key prefix for the kind (e.g. "e:", "d:", "l:").
//
// Returns the empty string for an invalid kind.
func (k Kind) Prefix() string {
	switch k {
	case KindEntity:
		return "e:"
	case KindDecision:
		return "d:"
	case KindObservation:
		return "o:"
	case KindOutcome:
		return "x:"
	case KindLink:
		return "l:"
	default:
		return ""
	}
}

// ReverseLinkPrefix is the prefix for the paired reverse-edge index.
// Reverse-index records map dst -> src for fast reverse traversal of links.
const ReverseLinkPrefix = "lr:"

// kindFromPrefix returns the kind that owns the given key prefix.
// Returns (0, false) for unknown prefixes.
func kindFromPrefix(prefix string) (Kind, bool) {
	switch prefix {
	case "e:":
		return KindEntity, true
	case "d:":
		return KindDecision, true
	case "o:":
		return KindObservation, true
	case "x:":
		return KindOutcome, true
	case "l:":
		return KindLink, true
	default:
		return 0, false
	}
}

// recordSchemaVersion is the schema version stamped on every event record.
// Bump when adding fields that older readers should reject; do not bump for
// additive changes (forward-compat via Skip on unknown field IDs).
const recordSchemaVersion uint8 = 1

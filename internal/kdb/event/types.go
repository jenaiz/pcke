package event

import (
	"time"

	"github.com/jenaiz/pcke/internal/kdb/encoding"
)

// Lifecycle is the active/inactive state of an event.
//
// Wire-stable: values are persisted; do not renumber.
type Lifecycle uint8

const (
	// LifecycleActive marks the event as currently in force.
	LifecycleActive Lifecycle = 1
	// LifecycleSuperseded marks the event as replaced by a later version
	// in the same id chain.
	LifecycleSuperseded Lifecycle = 2
	// LifecycleHistorical marks the event as retained for archive only;
	// readers should skip it for "current" queries.
	LifecycleHistorical Lifecycle = 3
)

// Valid reports whether l is a known lifecycle.
func (l Lifecycle) Valid() bool {
	switch l {
	case LifecycleActive, LifecycleSuperseded, LifecycleHistorical:
		return true
	default:
		return false
	}
}

// Severity is the strength of a Decision (must / should / may).
//
// Wire-stable: values are persisted; do not renumber.
type Severity uint8

const (
	// SeverityMust marks decisions that must be honored.
	SeverityMust Severity = 1
	// SeverityShould marks decisions that should be honored absent good cause.
	SeverityShould Severity = 2
	// SeverityMay marks decisions that are permissive guidance.
	SeverityMay Severity = 3
)

// Valid reports whether s is a known severity.
func (s Severity) Valid() bool {
	switch s {
	case SeverityMust, SeverityShould, SeverityMay:
		return true
	default:
		return false
	}
}

// Scope is the breadth a Decision applies to (file / module / global).
//
// Wire-stable: values are persisted; do not renumber.
type Scope uint8

const (
	// ScopeFile applies a decision to a single file.
	ScopeFile Scope = 1
	// ScopeModule applies a decision to all files in a module.
	ScopeModule Scope = 2
	// ScopeGlobal applies a decision project-wide.
	ScopeGlobal Scope = 3
)

// Valid reports whether s is a known scope.
func (s Scope) Valid() bool {
	switch s {
	case ScopeFile, ScopeModule, ScopeGlobal:
		return true
	default:
		return false
	}
}

// Header carries the metadata fields common to every event.
//
// Supersedes is empty for the first version of an id; for later versions
// it is the full key bytes of the immediately-prior version in the same
// chain (e.g. "e:internal/kdb/db.go:v0000000000000003").
type Header struct {
	Version    uint64
	CreatedAt  time.Time
	Supersedes []byte
	Lifecycle  Lifecycle
}

// Event is the sum interface satisfied by every concrete event type.
//
// Concrete types live in this package; the encodePayload method is
// unexported so the set of kinds is closed.
type Event interface {
	// Kind returns the Kind discriminator.
	Kind() Kind
	// ID returns the logical, unescaped id of the event.
	ID() string
	// Header returns the event's header metadata.
	Header() Header
	// SetHeader replaces the event's header. Used by Store.Append to stamp
	// version, supersedes pointer, lifecycle and timestamp before encoding.
	SetHeader(Header)
	// encodePayload writes the kind-specific fields to the encoder.
	// Header fields are written by Encode before this is called.
	encodePayload(*encoding.Encoder)
}

// Field IDs.
//
// Header fields are 1–9; kind-specific fields start at 10.
// Stable wire IDs: do not renumber; only add to the trailing range.
const (
	// Header fields.
	fieldVersion        uint8 = 1
	fieldCreatedAtNanos uint8 = 2
	fieldSupersedesKey  uint8 = 3
	fieldKind           uint8 = 4
	fieldLifecycle      uint8 = 5

	// Entity-specific.
	fieldEntityType uint8 = 10
	fieldEntityPath uint8 = 11
	fieldEntityName uint8 = 12

	// Decision-specific.
	fieldDecisionTitle    uint8 = 15
	fieldDecisionBody     uint8 = 16
	fieldDecisionSeverity uint8 = 17
	fieldDecisionScope    uint8 = 18
	fieldDecisionSource   uint8 = 19

	// Link-specific.
	fieldLinkSrcRef   uint8 = 20
	fieldLinkEdgeType uint8 = 21
	fieldLinkDstRef   uint8 = 22

	// Observation-specific (Phase 14).
	fieldObservationAction  uint8 = 25
	fieldObservationSubject uint8 = 26

	// Outcome-specific (Phase 14).
	fieldOutcomeType    uint8 = 28
	fieldOutcomeSubject uint8 = 29
)

// Entity is a code unit: file, function, type, or module.
type Entity struct {
	Hdr  Header
	EID  string
	Type string // "file" | "function" | "type" | "module"
	Path string // file path (entities anchored to a file)
	Name string // symbol name (functions, types)
}

// Kind reports the event kind. See Kind.
func (e *Entity) Kind() Kind { return KindEntity }

// ID returns the logical, unescaped id of the entity.
func (e *Entity) ID() string { return e.EID }

// Header returns the entity's header metadata.
func (e *Entity) Header() Header { return e.Hdr }

// SetHeader replaces the entity's header metadata.
func (e *Entity) SetHeader(h Header) { e.Hdr = h }

func (e *Entity) encodePayload(enc *encoding.Encoder) {
	if e.Type != "" {
		enc.PutString(fieldEntityType, e.Type)
	}
	if e.Path != "" {
		enc.PutString(fieldEntityPath, e.Path)
	}
	if e.Name != "" {
		enc.PutString(fieldEntityName, e.Name)
	}
}

// Decision is a typed assertion about code with severity and scope.
//
// Source records where the decision came from: "@pcke-rule" annotation,
// "adr" file, "commit" message heuristic, "doc" heading, or "manual".
type Decision struct {
	Hdr      Header
	DID      string
	Title    string
	Body     string
	Severity Severity
	Scope    Scope
	Source   string
}

// Kind reports the event kind. See Kind.
func (d *Decision) Kind() Kind { return KindDecision }

// ID returns the logical, unescaped id of the decision.
func (d *Decision) ID() string { return d.DID }

// Header returns the decision's header metadata.
func (d *Decision) Header() Header { return d.Hdr }

// SetHeader replaces the decision's header metadata.
func (d *Decision) SetHeader(h Header) { d.Hdr = h }

func (d *Decision) encodePayload(enc *encoding.Encoder) {
	if d.Title != "" {
		enc.PutString(fieldDecisionTitle, d.Title)
	}
	if d.Body != "" {
		enc.PutString(fieldDecisionBody, d.Body)
	}
	if d.Severity != 0 {
		enc.PutUint8(fieldDecisionSeverity, uint8(d.Severity))
	}
	if d.Scope != 0 {
		enc.PutUint8(fieldDecisionScope, uint8(d.Scope))
	}
	if d.Source != "" {
		enc.PutString(fieldDecisionSource, d.Source)
	}
}

// Link is a first-class edge between two typed references.
//
// SrcRef and DstRef are typed references without a version (e.g.
// "e:internal/kdb/db.go"). The link refers to the entity logically;
// readers resolve to a specific version via Latest or AsOf.
type Link struct {
	Hdr      Header
	SrcRef   string
	EdgeType string
	DstRef   string
}

// Kind reports the event kind. See Kind.
func (l *Link) Kind() Kind { return KindLink }

// ID returns the deterministic composite id for the link:
// EscapeID(SrcRef) + ":" + EscapeID(EdgeType) + ":" + EscapeID(DstRef).
//
// The colons in the returned id are structural; the segments are
// independently escaped so a final BuildKey/ParseKey round-trip is
// unambiguous.
func (l *Link) ID() string {
	return EscapeID(l.SrcRef) + ":" + EscapeID(l.EdgeType) + ":" + EscapeID(l.DstRef)
}

// Header returns the link's header metadata.
func (l *Link) Header() Header { return l.Hdr }

// SetHeader replaces the link's header metadata.
func (l *Link) SetHeader(h Header) { l.Hdr = h }

func (l *Link) encodePayload(enc *encoding.Encoder) {
	if l.SrcRef != "" {
		enc.PutString(fieldLinkSrcRef, l.SrcRef)
	}
	if l.EdgeType != "" {
		enc.PutString(fieldLinkEdgeType, l.EdgeType)
	}
	if l.DstRef != "" {
		enc.PutString(fieldLinkDstRef, l.DstRef)
	}
}

// Observation is an agent or scanner action. Reserved for Phase 14;
// fields are minimal in v0.10.0.
type Observation struct {
	Hdr     Header
	OID     string
	Action  string
	Subject string
}

// Kind reports the event kind. See Kind.
func (o *Observation) Kind() Kind { return KindObservation }

// ID returns the logical, unescaped id of the observation.
func (o *Observation) ID() string { return o.OID }

// Header returns the observation's header metadata.
func (o *Observation) Header() Header { return o.Hdr }

// SetHeader replaces the observation's header metadata.
func (o *Observation) SetHeader(h Header) { o.Hdr = h }

func (o *Observation) encodePayload(enc *encoding.Encoder) {
	if o.Action != "" {
		enc.PutString(fieldObservationAction, o.Action)
	}
	if o.Subject != "" {
		enc.PutString(fieldObservationSubject, o.Subject)
	}
}

// Outcome is a derived event. Reserved for Phase 14; fields are minimal
// in v0.10.0.
type Outcome struct {
	Hdr     Header
	XID     string
	Type    string
	Subject string
}

// Kind reports the event kind. See Kind.
func (o *Outcome) Kind() Kind { return KindOutcome }

// ID returns the logical, unescaped id of the outcome.
func (o *Outcome) ID() string { return o.XID }

// Header returns the outcome's header metadata.
func (o *Outcome) Header() Header { return o.Hdr }

// SetHeader replaces the outcome's header metadata.
func (o *Outcome) SetHeader(h Header) { o.Hdr = h }

func (o *Outcome) encodePayload(enc *encoding.Encoder) {
	if o.Type != "" {
		enc.PutString(fieldOutcomeType, o.Type)
	}
	if o.Subject != "" {
		enc.PutString(fieldOutcomeSubject, o.Subject)
	}
}

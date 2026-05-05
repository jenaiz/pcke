package event

import "errors"

// Sentinel errors for the event package.
var (
	// ErrCorrupt indicates a stored event record cannot be decoded.
	ErrCorrupt = errors.New("event: corrupt record")

	// ErrInvalidKey indicates a key does not match the expected
	// "<prefix>:<id>:v<N>" shape.
	ErrInvalidKey = errors.New("event: invalid key")

	// ErrInvalidKind indicates an event_kind field value outside the
	// known set.
	ErrInvalidKind = errors.New("event: invalid kind")

	// ErrInvalidLifecycle indicates a lifecycle field value outside the
	// known set.
	ErrInvalidLifecycle = errors.New("event: invalid lifecycle")

	// ErrInvalidSeverity indicates a severity field value outside the
	// known set.
	ErrInvalidSeverity = errors.New("event: invalid severity")

	// ErrInvalidScope indicates a scope field value outside the known set.
	ErrInvalidScope = errors.New("event: invalid scope")

	// ErrEmptyID indicates an event was constructed or decoded with an
	// empty logical id.
	ErrEmptyID = errors.New("event: empty id")

	// ErrSchemaVersion indicates a record carries a schema version this
	// build does not understand.
	ErrSchemaVersion = errors.New("event: unsupported schema version")

	// ErrNotFound indicates no version exists for the requested (kind, id).
	ErrNotFound = errors.New("event: not found")

	// ErrSupersedesLoop indicates a supersedes-chain walk exceeded the
	// caller-supplied hop limit, or revisited a key (cycle).
	ErrSupersedesLoop = errors.New("event: supersedes loop or hop limit exceeded")

	// ErrSupersedesMissing indicates a supersedes pointer references a key
	// that is no longer present in the store.
	ErrSupersedesMissing = errors.New("event: supersedes target missing")
)

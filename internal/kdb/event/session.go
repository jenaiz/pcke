package event

import "strings"

// Observation Action values for Phase 14 sub-types of KindObservation.
//
// Both sub-types live under the existing "o:" prefix. The Action field
// distinguishes them; their logical OID begins with the matching prefix
// constant (SessionOIDPrefix / CallOIDPrefix) followed by a uuid.
//
// Wire-stable: persisted in event records; never rename existing values.
const (
	// ActionSession marks an Observation that anchors one agent session.
	ActionSession = "session"
	// ActionCall marks an Observation that records a single MCP tool call.
	ActionCall = "call"
)

// Logical OID prefixes for the two Observation sub-types.
//
// A Session observation's OID is SessionOIDPrefix + "<uuid>"; a ToolCall
// observation's OID is CallOIDPrefix + "<uuid>". The structural ':' in
// the OID is escaped by EscapeID when the storage key is built, so the
// raw key form is e.g. "o:session\c<uuid>:v0000000000000001". Conceptually
// (and in docs/CLI output) we render the key as "o:session:<uuid>".
const (
	// SessionOIDPrefix prefixes the logical OID of every Session observation.
	SessionOIDPrefix = "session:"
	// CallOIDPrefix prefixes the logical OID of every ToolCall observation.
	CallOIDPrefix = "call:"
)

// Edge types introduced by Phase 14 (PRD v5.2 §5.3).
//
// Wire-stable: persisted on Link records; never rename existing values.
const (
	// EdgeContains links a Session observation to each ToolCall it contains
	// (src=o:session:<uuid>, dst=o:call:<uuid>).
	EdgeContains = "contains"
	// EdgeServed links a ToolCall observation to every Entity/Decision it
	// surfaced in its response (src=o:call:<uuid>, dst=e:* | d:*).
	EdgeServed = "served"
	// EdgeBelongsTo links a ToolCall observation back to its parent Session
	// (src=o:call:<uuid>, dst=o:session:<uuid>). Inverse of EdgeContains;
	// kept as its own edge so reverse-index lookups stay constant-time.
	EdgeBelongsTo = "belongs_to"
)

// SessionOID returns the logical OID for a Session observation given its
// uuid: "session:<uuid>". Empty uuid is allowed and yields the bare
// prefix, which callers should treat as an error before persisting.
func SessionOID(uuid string) string {
	return SessionOIDPrefix + uuid
}

// CallOID returns the logical OID for a ToolCall observation given its
// uuid: "call:<uuid>".
func CallOID(uuid string) string {
	return CallOIDPrefix + uuid
}

// SessionRef returns the typed reference ("o:session:<uuid>") used as
// SrcRef/DstRef on Link records that touch a Session.
func SessionRef(uuid string) string {
	return KindObservation.Prefix() + SessionOID(uuid)
}

// CallRef returns the typed reference ("o:call:<uuid>") used as
// SrcRef/DstRef on Link records that touch a ToolCall.
func CallRef(uuid string) string {
	return KindObservation.Prefix() + CallOID(uuid)
}

// IsSessionOID reports whether oid is a Session-sub-type OID.
func IsSessionOID(oid string) bool {
	return strings.HasPrefix(oid, SessionOIDPrefix)
}

// IsCallOID reports whether oid is a ToolCall-sub-type OID.
func IsCallOID(oid string) bool {
	return strings.HasPrefix(oid, CallOIDPrefix)
}

// SessionUUID returns the uuid portion of a Session OID. Returns
// ("", false) if oid is not a Session OID.
func SessionUUID(oid string) (string, bool) {
	if !IsSessionOID(oid) {
		return "", false
	}
	return oid[len(SessionOIDPrefix):], true
}

// CallUUID returns the uuid portion of a ToolCall OID. Returns
// ("", false) if oid is not a ToolCall OID.
func CallUUID(oid string) (string, bool) {
	if !IsCallOID(oid) {
		return "", false
	}
	return oid[len(CallOIDPrefix):], true
}

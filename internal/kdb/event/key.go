package event

import (
	"fmt"
	"strconv"
	"strings"
)

// versionDigits is the fixed width of the decimal version number in keys.
// 16 digits is enough for ~10^16 versions per id; with this width the
// lexicographic order of keys equals the numeric order of versions, so a
// single B+tree cursor seek finds the latest version.
const versionDigits = 16

// versionSeparator is the literal separator between an escaped id and the
// version suffix in a key: "<prefix>:<id-escaped>:v<digits>".
const versionSeparator = ":v"

// EscapeID escapes a logical id for safe inclusion in a key.
//
// Two replacements only:
//   - '\' becomes '\\' (so unescaping is unambiguous)
//   - ':'  becomes '\c' (so structural ':' separators in the key cannot
//     collide with content from the id)
//
// Other characters — including '/', spaces, and unicode — pass through
// untouched. This keeps file paths and human-readable ids legible in
// raw key dumps.
func EscapeID(id string) string {
	if !strings.ContainsAny(id, `\:`) {
		return id
	}
	var b strings.Builder
	b.Grow(len(id) + 4)
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case ':':
			b.WriteString(`\c`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// UnescapeID reverses EscapeID. Returns ErrInvalidKey for any malformed
// escape sequence (a trailing '\' or '\' followed by an unknown letter).
func UnescapeID(escaped string) (string, error) {
	if !strings.Contains(escaped, `\`) {
		return escaped, nil
	}
	var b strings.Builder
	b.Grow(len(escaped))
	for i := 0; i < len(escaped); i++ {
		c := escaped[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(escaped) {
			return "", fmt.Errorf("%w: trailing backslash", ErrInvalidKey)
		}
		next := escaped[i+1]
		switch next {
		case '\\':
			b.WriteByte('\\')
		case 'c':
			b.WriteByte(':')
		default:
			return "", fmt.Errorf("%w: unknown escape \\%c", ErrInvalidKey, next)
		}
		i++ // consume the second char of the escape
	}
	return b.String(), nil
}

// BuildKey returns the storage key for a given kind, logical id, and
// version. The id is escaped with EscapeID; the version is rendered at
// fixed width so lexicographic order tracks numeric order.
//
// Returns ErrInvalidKind for an unknown kind, or ErrEmptyID for an empty id.
func BuildKey(kind Kind, id string, version uint64) ([]byte, error) {
	prefix := kind.Prefix()
	if prefix == "" {
		return nil, ErrInvalidKind
	}
	if id == "" {
		return nil, ErrEmptyID
	}
	escaped := EscapeID(id)
	verStr := padVersion(version)
	out := make([]byte, 0, len(prefix)+len(escaped)+len(versionSeparator)+versionDigits)
	out = append(out, prefix...)
	out = append(out, escaped...)
	out = append(out, versionSeparator...)
	out = append(out, verStr...)
	return out, nil
}

// BuildReverseLinkKey returns the storage key for the reverse-link index.
// dst, edgeType and src are the typed references and edge type from a
// Link.
//
// Reverse-link keys are NOT versioned: there is exactly one lr: record
// per (dst, edge, src) tuple. The value of the record is the full key of
// the latest forward link version. When a link is re-appended (a new
// version), AppendLink overwrites the lr: value to point at the new
// forward-key. This trades AS OF support on the reverse path for a
// constant-time lookup of the current edge state.
func BuildReverseLinkKey(dstRef, edgeType, srcRef string) ([]byte, error) {
	if dstRef == "" || edgeType == "" || srcRef == "" {
		return nil, ErrEmptyID
	}
	composite := EscapeID(dstRef) + ":" + EscapeID(edgeType) + ":" + EscapeID(srcRef)
	out := make([]byte, 0, len(ReverseLinkPrefix)+len(composite))
	out = append(out, ReverseLinkPrefix...)
	out = append(out, composite...)
	return out, nil
}

// ParsedKey is the decomposed form of an event key.
type ParsedKey struct {
	Kind    Kind
	ID      string // unescaped logical id
	Version uint64
}

// ParseKey decomposes a storage key into its components. Reverse-link
// keys ("lr:") are not supported here; they are decomposed by callers
// in the link-index path.
//
// Returns ErrInvalidKey for any structural malformation, ErrInvalidKind
// for an unknown prefix, or ErrInvalidKey wrapping the version-parse
// error if the version segment is malformed.
func ParseKey(key []byte) (ParsedKey, error) {
	s := string(key)

	// Reject reverse-link keys at this entry point — they have a different
	// shape (composite id with structural colons) and are decoded by a
	// dedicated helper.
	if strings.HasPrefix(s, ReverseLinkPrefix) {
		return ParsedKey{}, fmt.Errorf("%w: use ParseReverseLinkKey for lr: keys", ErrInvalidKey)
	}

	colonIdx := strings.IndexByte(s, ':')
	if colonIdx < 1 {
		return ParsedKey{}, fmt.Errorf("%w: missing prefix separator", ErrInvalidKey)
	}
	prefix := s[:colonIdx+1]
	kind, ok := kindFromPrefix(prefix)
	if !ok {
		return ParsedKey{}, fmt.Errorf("%w: unknown prefix %q", ErrInvalidKind, prefix)
	}

	verStart, version, err := splitVersionSuffix(s)
	if err != nil {
		return ParsedKey{}, err
	}
	escapedID := s[colonIdx+1 : verStart]
	if escapedID == "" {
		return ParsedKey{}, ErrEmptyID
	}
	id, err := UnescapeID(escapedID)
	if err != nil {
		return ParsedKey{}, err
	}
	return ParsedKey{Kind: kind, ID: id, Version: version}, nil
}

// ParsedReverseLinkKey is the decomposed form of a reverse-link key.
//
// All three reference strings are unescaped.
type ParsedReverseLinkKey struct {
	DstRef   string
	EdgeType string
	SrcRef   string
}

// ParseReverseLinkKey decomposes an "lr:<dst>:<edge>:<src>" key into
// its components.
func ParseReverseLinkKey(key []byte) (ParsedReverseLinkKey, error) {
	s := string(key)
	if !strings.HasPrefix(s, ReverseLinkPrefix) {
		return ParsedReverseLinkKey{}, fmt.Errorf("%w: not a reverse-link key", ErrInvalidKey)
	}
	body := s[len(ReverseLinkPrefix):]
	parts, err := splitEscapedColons(body, 3)
	if err != nil {
		return ParsedReverseLinkKey{}, err
	}
	dst, err := UnescapeID(parts[0])
	if err != nil {
		return ParsedReverseLinkKey{}, err
	}
	edge, err := UnescapeID(parts[1])
	if err != nil {
		return ParsedReverseLinkKey{}, err
	}
	src, err := UnescapeID(parts[2])
	if err != nil {
		return ParsedReverseLinkKey{}, err
	}
	if dst == "" || edge == "" || src == "" {
		return ParsedReverseLinkKey{}, ErrEmptyID
	}
	return ParsedReverseLinkKey{
		DstRef:   dst,
		EdgeType: edge,
		SrcRef:   src,
	}, nil
}

// reverseLinkPrefixForDst returns the byte prefix matching every reverse-
// link entry for a given dst+edgeType: "lr:<escaped-dst>:<escaped-edge>:".
// Used by Store.ReverseLinks for the cursor seek.
func reverseLinkPrefixForDst(dstRef, edgeType string) ([]byte, error) {
	if dstRef == "" || edgeType == "" {
		return nil, ErrEmptyID
	}
	out := make([]byte, 0, len(ReverseLinkPrefix)+len(dstRef)+len(edgeType)+4)
	out = append(out, ReverseLinkPrefix...)
	out = append(out, EscapeID(dstRef)...)
	out = append(out, ':')
	out = append(out, EscapeID(edgeType)...)
	out = append(out, ':')
	return out, nil
}

// padVersion renders v as a fixed-width zero-padded decimal string.
func padVersion(v uint64) string {
	return fmt.Sprintf("%0*d", versionDigits, v)
}

// chainPrefix returns the byte prefix shared by every version of a given
// (kind, id): "<prefix><escaped-id>:v". It is used as a cursor seek target
// when scanning a single id's version chain.
func chainPrefix(kind Kind, id string) ([]byte, error) {
	prefix := kind.Prefix()
	if prefix == "" {
		return nil, ErrInvalidKind
	}
	if id == "" {
		return nil, ErrEmptyID
	}
	escaped := EscapeID(id)
	out := make([]byte, 0, len(prefix)+len(escaped)+len(versionSeparator))
	out = append(out, prefix...)
	out = append(out, escaped...)
	out = append(out, versionSeparator...)
	return out, nil
}

// kindPrefix returns the byte prefix that delimits all events of a given
// kind: just "<prefix>". Used by IterateKind for the outer scan.
func kindPrefix(kind Kind) ([]byte, error) {
	prefix := kind.Prefix()
	if prefix == "" {
		return nil, ErrInvalidKind
	}
	return []byte(prefix), nil
}

// splitVersionSuffix locates the trailing ":v<digits>" and returns the
// byte offset where the suffix starts plus the parsed version number.
func splitVersionSuffix(s string) (verStart int, version uint64, err error) {
	expectedTail := len(versionSeparator) + versionDigits
	if len(s) < expectedTail+2 {
		return 0, 0, fmt.Errorf("%w: too short for version suffix", ErrInvalidKey)
	}
	verStart = len(s) - expectedTail
	if s[verStart:verStart+len(versionSeparator)] != versionSeparator {
		return 0, 0, fmt.Errorf("%w: missing %q separator before version", ErrInvalidKey, versionSeparator)
	}
	digits := s[verStart+len(versionSeparator):]
	parsed, parseErr := strconv.ParseUint(digits, 10, 64)
	if parseErr != nil {
		return 0, 0, fmt.Errorf("%w: bad version digits %q: %s", ErrInvalidKey, digits, parseErr)
	}
	return verStart, parsed, nil
}

// splitEscapedColons splits s on every unescaped ':' and verifies the
// resulting count matches want.
func splitEscapedColons(s string, want int) ([]string, error) {
	parts := make([]string, 0, want)
	var current strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			// Preserve the escape sequence so UnescapeID can decode it.
			current.WriteByte(c)
			current.WriteByte(s[i+1])
			i++
			continue
		}
		if c == ':' {
			parts = append(parts, current.String())
			current.Reset()
			continue
		}
		current.WriteByte(c)
	}
	parts = append(parts, current.String())
	if len(parts) != want {
		return nil, fmt.Errorf("%w: expected %d segments, got %d", ErrInvalidKey, want, len(parts))
	}
	return parts, nil
}

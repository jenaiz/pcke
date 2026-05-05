package graph

import (
	"bytes"
	"fmt"
	"time"

	"github.com/jenaiz/pcke/internal/kdb/event"
)

// forwardSrcPrefix returns the cursor-seek prefix matching every link
// record whose SrcRef equals start.
//
// Link keys are *doubly* escaped: Link.ID() escapes each segment
// (SrcRef, EdgeType, DstRef) individually and joins them with literal
// ':' separators; BuildKey then escapes the whole composite a second
// time, so the literal ':' separator becomes "\c" in the stored key
// while the original "\c" (from segment escaping) becomes "\\c".
//
// Concretely, for SrcRef "e:src":
//
//	onceEscaped  = `e\csrc`
//	twiceEscaped = `e\\csrc`
//	prefix       = `l:e\\csrc\c`     // <-- structural ':' was escaped
//
// The trailing `\c` ensures we don't bleed into a different src whose
// twice-escaped form starts with "e\\csrc..." (e.g. "e:src-2").
func forwardSrcPrefix(start Ref) []byte {
	onceEscaped := event.EscapeID(string(start))
	twiceEscaped := event.EscapeID(onceEscaped)
	out := make([]byte, 0, len("l:")+len(twiceEscaped)+2)
	out = append(out, "l:"...)
	out = append(out, twiceEscaped...)
	out = append(out, '\\', 'c') // the structural ':' separator after BuildKey's second escape
	return out
}

// reverseDstPrefix returns the cursor-seek prefix matching every
// reverse-index entry whose DstRef equals start: "lr:<escaped-dst>:".
func reverseDstPrefix(start Ref) []byte {
	out := make([]byte, 0, 8+len(start))
	out = append(out, event.ReverseLinkPrefix...)
	out = append(out, event.EscapeID(string(start))...)
	out = append(out, ':')
	return out
}

// splitChainTuple extracts the "<escaped-src>:<escaped-edge>:<escaped-dst>"
// body of a forward-link key — the bytes that uniquely identify a
// (src, edge, dst) tuple. The caller uses this body to detect tuple
// boundaries when scanning the version chain.
//
// Implementation: trim the "l:" prefix and the ":v<16 digits>" suffix.
func splitChainTuple(key []byte) ([]byte, error) {
	if !bytes.HasPrefix(key, []byte("l:")) {
		return nil, fmt.Errorf("graph: not a forward-link key: %q", key)
	}
	const versionTail = len(":v") + 16
	if len(key) < len("l:")+1+versionTail {
		return nil, fmt.Errorf("graph: forward-link key too short: %q", key)
	}
	body := key[len("l:") : len(key)-versionTail]
	return body, nil
}

// linkSnapshot is the subset of a forward-link record the graph walks
// care about. Folding all four fields into a single decode call avoids
// re-running event.Decode three times per record.
type linkSnapshot struct {
	dst       Ref
	edge      string
	lifecycle event.Lifecycle
	createdAt time.Time
}

// decodeLinkSnapshot decodes a forward-link value into the fields the
// traversal needs. Returns ok=false if the value is not a Link record.
//
// The id passed to event.Decode is a placeholder — it is required by
// the codec API but the snapshot does not need it (we only read the
// payload + header). A focused decoder that reads just these fields
// could replace this body for additional speedup; benchmarks in T8
// will tell us whether it is worth doing.
func decodeLinkSnapshot(value []byte) (linkSnapshot, bool) {
	const dummyID = "x:x:x"
	evt, err := event.Decode(value, dummyID)
	if err != nil {
		return linkSnapshot{}, false
	}
	link, ok := evt.(*event.Link)
	if !ok {
		return linkSnapshot{}, false
	}
	return linkSnapshot{
		dst:       Ref(link.DstRef),
		edge:      link.EdgeType,
		lifecycle: link.Header().Lifecycle,
		createdAt: link.Header().CreatedAt,
	}, true
}

// chainPrefixFromForwardKey returns the byte prefix that matches every
// version of the link identified by forwardKey. Used by the reverse
// walk under AsOf to scan the chain for the version active at the
// pinned timestamp.
//
// Implementation: drop the trailing ":v<16 digits>", append ":v" so the
// remaining prefix matches all version siblings.
func chainPrefixFromForwardKey(forwardKey []byte) ([]byte, error) {
	const versionTail = len(":v") + 16
	if len(forwardKey) < versionTail+len("l:") {
		return nil, fmt.Errorf("graph: forward key too short: %q", forwardKey)
	}
	body := forwardKey[:len(forwardKey)-versionTail]
	out := make([]byte, len(body)+len(":v"))
	copy(out, body)
	copy(out[len(body):], ":v")
	return out, nil
}

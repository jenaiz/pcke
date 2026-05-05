package graph

import (
	"bytes"
	"fmt"

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

// decodeLinkValue decodes a forward-link value enough to extract the
// DstRef and EdgeType. It returns false if the value is not parseable
// as a Link.
//
// We avoid a full event.Decode here because the forward-walk path reads
// many records per traversal and only needs two fields; a focused
// decoder is meaningfully cheaper. The benchmark in commit 4 of T3 will
// validate the win.
//
// For simplicity and correctness during T3 commit 1, this function
// delegates to event.Decode and asserts the result is a Link. The
// micro-optimised scanner can replace this body later.
func decodeLinkValue(value []byte) (dst Ref, edge string, ok bool) {
	const dummyID = "x:x:x" // any non-empty id; Link uses its own composite id
	evt, err := event.Decode(value, dummyID)
	if err != nil {
		return "", "", false
	}
	link, isLink := evt.(*event.Link)
	if !isLink {
		return "", "", false
	}
	return Ref(link.DstRef), link.EdgeType, true
}

// lifecycleIsSuperseded reports whether the encoded link record carries
// LifecycleSuperseded. We re-decode the value (small cost; T3 commit 4
// can collapse this into decodeLinkValue).
func lifecycleIsSuperseded(value []byte) bool {
	const dummyID = "x:x:x"
	evt, err := event.Decode(value, dummyID)
	if err != nil {
		return false
	}
	return evt.Header().Lifecycle == event.LifecycleSuperseded
}

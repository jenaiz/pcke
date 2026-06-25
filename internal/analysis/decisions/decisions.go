package decisions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// Source labels which artifact a Decision was derived from. The string
// values are persisted in the Decision.Source field of the event-log
// record; do not rename existing values.
type Source string

// Source constants. New sources can be appended; never renumber/rename.
const (
	SourceADR        Source = "adr"
	SourceAnnotation Source = "annotation"
	SourceCommit     Source = "commit"
	SourceDoc        Source = "doc"
)

// Result aggregates how many decisions each source produced during a
// single scan. Returned by the orchestrator (Backfill) so the caller
// can log/expose totals; per-source helpers return their own counts.
type Result struct {
	ADRs        int
	Annotations int
	Commits     int
	Docs        int
}

// Total returns the sum across all sources.
func (r Result) Total() int {
	return r.ADRs + r.Annotations + r.Commits + r.Docs
}

// writeDecision appends a single Decision event inside the supplied
// WriteTx. Idempotent: if e:<DID>:v1 already exists, the call is a
// no-op and the (no-op, nil) tuple is returned.
//
// store is required so AppendInTx can stamp the version chain.
func writeDecision(wtx *tx.WriteTx, store *event.Store, d *event.Decision) (wrote bool, err error) {
	if d == nil {
		return false, errors.New("decisions: nil decision")
	}
	if d.DID == "" {
		return false, errors.New("decisions: empty DID")
	}

	// Idempotency probe: skip if v1 already exists for this id.
	expectedKey, err := event.BuildKey(event.KindDecision, d.DID, 1)
	if err != nil {
		return false, fmt.Errorf("build key for %q: %w", d.DID, err)
	}
	if _, getErr := wtx.Get(expectedKey); getErr == nil {
		return false, nil
	} else if !errors.Is(getErr, btree.ErrKeyNotFound) {
		return false, fmt.Errorf("probe %q: %w", expectedKey, getErr)
	}

	if _, err := store.AppendInTx(wtx, d); err != nil {
		return false, fmt.Errorf("append d:%s: %w", d.DID, err)
	}
	return true, nil
}

// decisionLinkEdge is the edge type connecting a code entity to a
// decision that governs or originates from it. It is the edge the
// review workflow boosts, so retrieval surfaces the rules attached to
// the files an agent is touching.
const decisionLinkEdge = "decision_link"

// pagesPerDecision is the freelist headroom reserved per decision when
// pre-growing a backfill transaction. It covers the d: decision record
// plus its decision_link edge (l: forward + lr: reverse) and B+tree
// split / CoW churn.
const pagesPerDecision = 8

// writeDecisionLink appends a decision_link edge "e:<filePath>" ->
// "d:<did>" inside wtx, unless its v1 record already exists. A blank
// filePath or did is a no-op (the decision has no file anchor).
//
// filePath must be a repo-relative, slash-separated path (the entity id
// form written by the scanner), so the SrcRef resolves to the file's
// e: entity.
func writeDecisionLink(wtx *tx.WriteTx, store *event.Store, filePath, did string) (bool, error) {
	if filePath == "" || did == "" {
		return false, nil
	}
	link := &event.Link{
		Hdr: event.Header{
			CreatedAt: time.Now().UTC(),
			Lifecycle: event.LifecycleActive,
		},
		SrcRef:   "e:" + filePath,
		EdgeType: decisionLinkEdge,
		DstRef:   "d:" + did,
	}
	key, err := event.BuildKey(event.KindLink, link.ID(), 1)
	if err != nil {
		return false, fmt.Errorf("build key for decision_link %s: %w", link.ID(), err)
	}
	if _, getErr := wtx.Get(key); getErr == nil {
		return false, nil
	} else if !errors.Is(getErr, btree.ErrKeyNotFound) {
		return false, fmt.Errorf("probe decision_link %s: %w", link.ID(), getErr)
	}
	if _, err := store.AppendInTx(wtx, link); err != nil {
		return false, fmt.Errorf("append decision_link %s -> %s: %w", link.SrcRef, link.DstRef, err)
	}
	return true, nil
}

// ensureFreePages reserves at least n free pages when db supports
// growth (satisfied by *kdb.DB). kdb does not auto-grow inside a write
// transaction, so a backfill that out-sizes the current free pages must
// reserve headroom first. Test doubles that do not implement the
// capability are left untouched.
func ensureFreePages(db any, n int) error {
	if g, ok := db.(interface{ EnsureFreePages(int) error }); ok {
		return g.EnsureFreePages(n)
	}
	return nil
}

// firstNonEmptyLine returns the first non-blank line of s, trimmed and
// stripped of leading markdown heading markers ("#", "##", ...).
// Returns "(untitled)" if no non-empty line exists.
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Strip leading '#' markdown heading markers.
		for strings.HasPrefix(trimmed, "#") {
			trimmed = strings.TrimSpace(trimmed[1:])
		}
		if trimmed == "" {
			continue
		}
		return trimmed
	}
	return "(untitled)"
}

// truncateRunes returns at most maxRunes runes of s. Multibyte safe.
func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}

// _ keeps context.Context in the import set; per-source files use it.
var _ = context.Background

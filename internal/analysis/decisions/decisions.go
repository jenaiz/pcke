package decisions

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

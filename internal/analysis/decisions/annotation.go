package decisions

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jenaiz/pcke/internal/analysis/annotations"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// BackfillFromAnnotations writes one Decision event per @pcke-rule
// annotation supplied. Lesson annotations are intentionally skipped
// — those are documentation, not decisions.
//
// DID derivation: "rule:" + annotation.Name. Names should be unique
// across the repo by convention; if two annotations share a Name the
// idempotency probe ensures only the first one wins per scan (the
// second is a no-op write).
//
// Severity heuristic (from the annotation name prefix):
//
//	"must-X"   -> SeverityMust
//	"may-X"    -> SeverityMay
//	otherwise  -> SeverityShould (default for legacy/un-tagged names)
//
// Scope = ScopeFile (annotations are anchored to a source file).
//
// File anchor: Decision has no FilePath field today, so we prefix
// the Body with "[file: <path>:<line>]\n\n" to preserve the anchor.
// A future schema extension can migrate this to a structured field.
//
// Header.CreatedAt is set to time.Now() — annotations have no per-
// annotation timestamp available without re-stating the source file.
//
// Returns the number of Decisions newly written.
func BackfillFromAnnotations(ctx context.Context, db UpdateDB, anns []annotations.Annotation) (int, error) {
	if len(anns) == 0 {
		return 0, nil
	}

	store := event.New(db)
	written := 0
	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		for _, a := range anns {
			if a.Type != annotations.Rule {
				continue
			}
			if a.Name == "" {
				continue
			}
			d := decisionFromAnnotation(a)
			ok, err := writeDecision(wtx, store, d)
			if err != nil {
				return err
			}
			if ok {
				written++
			}
		}
		return nil
	}); err != nil {
		return written, fmt.Errorf("backfill annotations: %w", err)
	}
	return written, nil
}

// decisionFromAnnotation builds the Decision payload for one Rule
// annotation. Pulled out so unit tests can verify the mapping without
// touching kdb.
func decisionFromAnnotation(a annotations.Annotation) *event.Decision {
	severity := severityFromAnnotationName(a.Name)
	body := a.Description
	if a.File != "" {
		anchor := fmt.Sprintf("[file: %s", a.File)
		if a.Line > 0 {
			anchor = fmt.Sprintf("%s:%d", anchor, a.Line)
		}
		anchor += "]"
		if body == "" {
			body = anchor
		} else {
			body = anchor + "\n\n" + body
		}
	}
	title := a.Name
	if a.Description != "" {
		title = a.Name
	}

	return &event.Decision{
		Hdr: event.Header{
			CreatedAt: time.Now().UTC(),
			Lifecycle: event.LifecycleActive,
		},
		DID:      "rule:" + a.Name,
		Title:    title,
		Body:     body,
		Severity: severity,
		Scope:    event.ScopeFile,
		Source:   string(SourceAnnotation),
	}
}

// severityFromAnnotationName extracts a severity hint from a "must-",
// "should-", or "may-" name prefix. Default is SeverityShould.
func severityFromAnnotationName(name string) event.Severity {
	lower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(lower, "must-"):
		return event.SeverityMust
	case strings.HasPrefix(lower, "may-"):
		return event.SeverityMay
	default:
		return event.SeverityShould
	}
}

// _ keeps context import live; the orchestrator (T5.4) will reuse it.
var _ = context.Background

package retrieval

import (
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/jenaiz/pcke/internal/kdb/event"
)

// recencyHalfLifeDays is the half-life used by ScoreRecency. An event
// created today scores 1.0; one half-life ago scores 0.5; n half-lives
// ago scores 1/2^n. 30 days matches the conventional "stale code"
// rule of thumb without being so short that yesterday's commit looks
// stale.
const recencyHalfLifeDays = 30

// ScoreRecency returns a value in [0, 1] based on how recently the
// event was created. now is the reference timestamp (use time.Now()
// in production; tests inject a fixed value).
//
// Formula: 2^(-age_days / 30). An event with no CreatedAt timestamp
// (zero Time) is treated as maximally stale (returns 0).
func ScoreRecency(now time.Time, evt event.Event) float64 {
	if evt == nil {
		return 0
	}
	created := evt.Header().CreatedAt
	if created.IsZero() {
		return 0
	}
	ageDays := now.Sub(created).Hours() / 24
	if ageDays < 0 {
		// Future-dated event (clock skew or test fixture) — treat as
		// brand new.
		return 1
	}
	return math.Exp2(-ageDays / float64(recencyHalfLifeDays))
}

// ScoreSeverity returns a value in [0, 1] from the event's severity.
//
//   - Decisions:    must=1.0, should=0.6, may=0.3, unset=0.5
//   - Other kinds:  0.5 (neutral; severity is a Decision-only concept)
//
// PRD v5.2 §4.3 lists the must/should/may anchor values.
func ScoreSeverity(evt event.Event) float64 {
	if evt == nil {
		return 0
	}
	d, ok := evt.(*event.Decision)
	if !ok {
		return 0.5
	}
	switch d.Severity {
	case event.SeverityMust:
		return 1.0
	case event.SeverityShould:
		return 0.6
	case event.SeverityMay:
		return 0.3
	default:
		return 0.5
	}
}

// ScoreProximity returns a value in [0, 1] based on how close the
// event is to the request's focus files.
//
//   - 1.0 — the event's own path equals one of the request's files
//     (or for Decisions, the file mentioned in the body anchor)
//   - 0.5 — same module/directory as one of the request's files
//   - 0.2 — global / no proximity signal
//
// For Decisions with ScopeGlobal (e.g. ADRs), the floor is 0.2 since
// they apply repo-wide regardless of which file is open.
func ScoreProximity(req Request, evt event.Event) float64 {
	if evt == nil {
		return 0
	}
	files := requestFiles(req)
	if len(files) == 0 {
		return 0.2
	}
	eventPath := pathForEvent(evt)
	if eventPath == "" {
		// Global scope (no path) — give it the floor.
		if d, ok := evt.(*event.Decision); ok && d.Scope == event.ScopeGlobal {
			return 0.2
		}
		return 0.2
	}
	eventDir := filepath.Dir(eventPath)
	for _, f := range files {
		if f == "" {
			continue
		}
		if eventPath == f {
			return 1.0
		}
		if eventDir == filepath.Dir(f) && eventDir != "." {
			return 0.5
		}
	}
	return 0.2
}

// ScoreNovelty returns 1.0 if evt's ref is not in req.AlreadyServed,
// 0.0 if it is. Conceptually: "would the agent learn something new
// from this section?".
func ScoreNovelty(req Request, evt event.Event) float64 {
	if evt == nil {
		return 0
	}
	ref := refForEvent(evt)
	for _, served := range req.AlreadyServed {
		if served == ref {
			return 0
		}
	}
	return 1.0
}

// Score returns the weighted combination of all four components.
// Result is in [0, 1] when w.Sum() == 1.0.
func Score(now time.Time, req Request, evt event.Event, w Weights) float64 {
	if evt == nil {
		return 0
	}
	r := ScoreRecency(now, evt)
	s := ScoreSeverity(evt)
	p := ScoreProximity(req, evt)
	n := ScoreNovelty(req, evt)
	return w.Recency*r + w.Severity*s + w.Proximity*p + w.Novelty*n
}

// requestFiles returns FilePath plus ChangedFiles as a single slice,
// dropping empties.
func requestFiles(req Request) []string {
	out := make([]string, 0, 1+len(req.ChangedFiles))
	if req.FilePath != "" {
		out = append(out, req.FilePath)
	}
	for _, f := range req.ChangedFiles {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// pathForEvent extracts the most useful "path" string for proximity
// scoring. For Entities the Path field; for Decisions a heuristic
// scan of the Body for an anchor like "[file: <path>:<line>]" written
// by the annotation backfill in F12.T5.2.
func pathForEvent(evt event.Event) string {
	switch v := evt.(type) {
	case *event.Entity:
		if v.Path != "" {
			return v.Path
		}
		return v.EID
	case *event.Decision:
		if anchor := extractFileAnchor(v.Body); anchor != "" {
			return anchor
		}
		return ""
	default:
		return ""
	}
}

// extractFileAnchor pulls the first "[file: <path>:<line>]" anchor
// from a Decision body. Returns the path portion only (drops the
// optional ":<line>" suffix). If no anchor is present, returns "".
func extractFileAnchor(body string) string {
	const marker = "[file: "
	start := strings.Index(body, marker)
	if start < 0 {
		return ""
	}
	rest := body[start+len(marker):]
	end := strings.Index(rest, "]")
	if end < 0 {
		return ""
	}
	anchor := rest[:end]
	if i := strings.LastIndex(anchor, ":"); i > 0 {
		// Drop the ":<line>" suffix only if it parses as digits.
		tail := anchor[i+1:]
		if isAllDigits(tail) {
			anchor = anchor[:i]
		}
	}
	return anchor
}

// refForEvent reconstructs the typed-reference string for an event,
// matching the format used by graph.Ref and the CLI.
func refForEvent(evt event.Event) string {
	if evt == nil {
		return ""
	}
	prefix := evt.Kind().Prefix()
	if prefix == "" {
		return ""
	}
	return prefix + evt.ID()
}

// isAllDigits reports whether s is non-empty and contains only ASCII
// digits.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

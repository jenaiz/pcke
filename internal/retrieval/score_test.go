package retrieval

import (
	"math"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb/event"
)

func nearlyEqual(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func TestScoreRecency_TodayIsOne(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	got := ScoreRecency(now, &event.Entity{
		Hdr: event.Header{CreatedAt: now},
	})
	if !nearlyEqual(got, 1.0, 0.001) {
		t.Errorf("got %f, want 1.0 for now=created", got)
	}
}

func TestScoreRecency_HalfLifeIsHalf(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	thirtyDaysAgo := now.AddDate(0, 0, -30)
	got := ScoreRecency(now, &event.Entity{
		Hdr: event.Header{CreatedAt: thirtyDaysAgo},
	})
	if !nearlyEqual(got, 0.5, 0.001) {
		t.Errorf("got %f, want 0.5 at half-life (30 days)", got)
	}
}

func TestScoreRecency_FarPastApproachesZero(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	yearAgo := now.AddDate(0, 0, -365)
	got := ScoreRecency(now, &event.Entity{
		Hdr: event.Header{CreatedAt: yearAgo},
	})
	if got > 0.01 {
		t.Errorf("got %f, want < 0.01 for 365-day-old event", got)
	}
}

func TestScoreRecency_FutureClampedToOne(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	tomorrow := now.AddDate(0, 0, 1)
	got := ScoreRecency(now, &event.Entity{
		Hdr: event.Header{CreatedAt: tomorrow},
	})
	if got != 1.0 {
		t.Errorf("got %f, want 1.0 for future-dated event", got)
	}
}

func TestScoreRecency_ZeroTimeIsZero(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	got := ScoreRecency(now, &event.Entity{Hdr: event.Header{}})
	if got != 0 {
		t.Errorf("got %f, want 0 for zero CreatedAt", got)
	}
}

func TestScoreRecency_NilSafe(t *testing.T) {
	t.Parallel()
	if ScoreRecency(time.Now(), nil) != 0 {
		t.Error("nil event must return 0")
	}
}

func TestScoreSeverity_Decisions(t *testing.T) {
	t.Parallel()
	cases := map[event.Severity]float64{
		event.SeverityMust:   1.0,
		event.SeverityShould: 0.6,
		event.SeverityMay:    0.3,
		0:                    0.5, // unset
	}
	for sev, want := range cases {
		got := ScoreSeverity(&event.Decision{Severity: sev})
		if got != want {
			t.Errorf("severity=%d: got %f, want %f", sev, got, want)
		}
	}
}

func TestScoreSeverity_NonDecisionIsNeutral(t *testing.T) {
	t.Parallel()
	got := ScoreSeverity(&event.Entity{})
	if got != 0.5 {
		t.Errorf("Entity got %f, want 0.5 (neutral)", got)
	}
}

func TestScoreSeverity_NilSafe(t *testing.T) {
	t.Parallel()
	if ScoreSeverity(nil) != 0 {
		t.Error("nil event must return 0")
	}
}

func TestScoreProximity_ExactPathMatch(t *testing.T) {
	t.Parallel()
	req := Request{FilePath: "internal/kdb/db.go"}
	evt := &event.Entity{EID: "internal/kdb/db.go", Path: "internal/kdb/db.go"}
	if got := ScoreProximity(req, evt); got != 1.0 {
		t.Errorf("got %f, want 1.0 for exact match", got)
	}
}

func TestScoreProximity_SameModuleHalf(t *testing.T) {
	t.Parallel()
	req := Request{FilePath: "internal/kdb/db.go"}
	evt := &event.Entity{EID: "internal/kdb/btree.go", Path: "internal/kdb/btree.go"}
	if got := ScoreProximity(req, evt); got != 0.5 {
		t.Errorf("got %f, want 0.5 for same dir", got)
	}
}

func TestScoreProximity_DifferentModule(t *testing.T) {
	t.Parallel()
	req := Request{FilePath: "internal/kdb/db.go"}
	evt := &event.Entity{EID: "cmd/pcke/main.go", Path: "cmd/pcke/main.go"}
	if got := ScoreProximity(req, evt); got != 0.2 {
		t.Errorf("got %f, want 0.2 for different module", got)
	}
}

func TestScoreProximity_RequestWithoutFilesIsFloor(t *testing.T) {
	t.Parallel()
	req := Request{}
	evt := &event.Entity{EID: "x", Path: "x"}
	if got := ScoreProximity(req, evt); got != 0.2 {
		t.Errorf("got %f, want 0.2 (floor when request has no files)", got)
	}
}

func TestScoreProximity_DiffMode(t *testing.T) {
	t.Parallel()
	req := Request{ChangedFiles: []string{"a.go", "internal/kdb/db.go"}}
	evt := &event.Entity{EID: "internal/kdb/db.go", Path: "internal/kdb/db.go"}
	if got := ScoreProximity(req, evt); got != 1.0 {
		t.Errorf("ChangedFiles match: got %f, want 1.0", got)
	}
}

func TestScoreProximity_DecisionWithFileAnchor(t *testing.T) {
	t.Parallel()
	req := Request{FilePath: "internal/kdb/db.go"}
	evt := &event.Decision{
		DID:  "rule:no-raw-sql",
		Body: "[file: internal/kdb/db.go:42]\n\nUse prepared statements.",
	}
	if got := ScoreProximity(req, evt); got != 1.0 {
		t.Errorf("decision with anchor matching FilePath: got %f, want 1.0", got)
	}
}

func TestScoreProximity_GlobalDecisionFloor(t *testing.T) {
	t.Parallel()
	req := Request{FilePath: "internal/kdb/db.go"}
	evt := &event.Decision{DID: "adr:0008", Body: "no anchor", Scope: event.ScopeGlobal}
	if got := ScoreProximity(req, evt); got != 0.2 {
		t.Errorf("global decision: got %f, want 0.2 (floor)", got)
	}
}

func TestScoreNovelty_NotServedIsOne(t *testing.T) {
	t.Parallel()
	req := Request{}
	evt := &event.Entity{EID: "x"}
	if got := ScoreNovelty(req, evt); got != 1.0 {
		t.Errorf("got %f, want 1.0 (not in AlreadyServed)", got)
	}
}

func TestScoreNovelty_ServedIsZero(t *testing.T) {
	t.Parallel()
	req := Request{AlreadyServed: []string{"e:x", "e:y"}}
	evt := &event.Entity{EID: "x"}
	if got := ScoreNovelty(req, evt); got != 0 {
		t.Errorf("got %f, want 0 (in AlreadyServed)", got)
	}
}

func TestScore_WeightsApplied(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	req := Request{FilePath: "internal/kdb/db.go"}
	// must-severity Decision anchored to the request's file → all
	// four components hit their max:
	//   recency  = 1.0 (created today)
	//   severity = 1.0 (must)
	//   proximity= 1.0 (exact path match via file anchor)
	//   novelty  = 1.0 (not served)
	// Score should be 1.0 with default weights.
	evt := &event.Decision{
		Hdr:      event.Header{CreatedAt: now},
		DID:      "rule:must-validate",
		Title:    "Validate input",
		Body:     "[file: internal/kdb/db.go:1]\n\nrule body",
		Severity: event.SeverityMust,
		Scope:    event.ScopeFile,
	}
	got := Score(now, req, evt, DefaultWeights())
	if !nearlyEqual(got, 1.0, 0.001) {
		t.Errorf("got %f, want 1.0 for max-on-all-axes case", got)
	}
}

func TestScore_NilEventIsZero(t *testing.T) {
	t.Parallel()
	if got := Score(time.Now(), Request{}, nil, DefaultWeights()); got != 0 {
		t.Errorf("nil event got %f, want 0", got)
	}
}

func TestDefaultWeights_SumsToOne(t *testing.T) {
	t.Parallel()
	w := DefaultWeights()
	if !nearlyEqual(w.Sum(), 1.0, 0.001) {
		t.Errorf("DefaultWeights().Sum() = %f, want 1.0", w.Sum())
	}
}

func TestExtractFileAnchor(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"[file: a/b.go:42]\n\nrest":    "a/b.go",
		"[file: a/b.go]\n\nrest":       "a/b.go",
		"[file: deep/path.go:999]":     "deep/path.go",
		"no anchor here":               "",
		"[file: ]":                     "",
		"prefix [file: x.go:1] suffix": "x.go",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			got := extractFileAnchor(in)
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

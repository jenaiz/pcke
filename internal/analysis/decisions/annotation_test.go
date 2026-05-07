package decisions_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jenaiz/pcke/internal/analysis/annotations"
	"github.com/jenaiz/pcke/internal/analysis/decisions"
	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
)

func openBlankDB(t *testing.T) *kdb.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for range 5 {
		if err := db.Grow(); err != nil {
			t.Fatalf("db.Grow: %v", err)
		}
	}
	return db
}

func TestBackfillFromAnnotations_EmptySlice(t *testing.T) {
	t.Parallel()
	db := openBlankDB(t)
	n, err := decisions.BackfillFromAnnotations(context.Background(), db, nil)
	if err != nil {
		t.Fatalf("BackfillFromAnnotations: %v", err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
}

func TestBackfillFromAnnotations_TranslatesRules(t *testing.T) {
	t.Parallel()
	db := openBlankDB(t)
	anns := []annotations.Annotation{
		{Type: annotations.Rule, Name: "no-raw-sql", Description: "Use prepared statements instead.", File: "internal/db/query.go", Line: 42},
		{Type: annotations.Rule, Name: "must-validate-input", Description: "Validate input at every API boundary.", File: "cmd/api/handler.go", Line: 17},
		{Type: annotations.Lesson, Name: "always-test-fixture", Description: "lesson should be skipped", File: "internal/x/x.go", Line: 1},
	}
	n, err := decisions.BackfillFromAnnotations(context.Background(), db, anns)
	if err != nil {
		t.Fatalf("BackfillFromAnnotations: %v", err)
	}
	if n != 2 {
		t.Errorf("wrote %d, want 2 (lessons skipped)", n)
	}

	store := event.New(db)
	got, err := store.Latest(context.Background(), event.KindDecision, "rule:no-raw-sql")
	if err != nil {
		t.Fatalf("Latest(rule:no-raw-sql): %v", err)
	}
	dec := got.(*event.Decision)
	if dec.Source != string(decisions.SourceAnnotation) {
		t.Errorf("Source = %q, want annotation", dec.Source)
	}
	if dec.Scope != event.ScopeFile {
		t.Errorf("Scope = %d, want ScopeFile", dec.Scope)
	}
	if dec.Severity != event.SeverityShould {
		t.Errorf("Severity = %d, want SeverityShould (default for plain name)", dec.Severity)
	}
	// Anchor present in Body.
	if !strings.Contains(dec.Body, "internal/db/query.go:42") {
		t.Errorf("Body missing file anchor: %q", dec.Body)
	}

	// "must-validate-input" gets SeverityMust from name prefix.
	gotMust, err := store.Latest(context.Background(), event.KindDecision, "rule:must-validate-input")
	if err != nil {
		t.Fatalf("Latest(rule:must-validate-input): %v", err)
	}
	if gotMust.(*event.Decision).Severity != event.SeverityMust {
		t.Errorf("must-* severity = %d, want SeverityMust", gotMust.(*event.Decision).Severity)
	}

	// Lesson should NOT be a Decision.
	if _, err := store.Latest(context.Background(), event.KindDecision, "rule:always-test-fixture"); !errors.Is(err, event.ErrNotFound) {
		t.Errorf("Lesson leaked into decisions: got %v, want ErrNotFound", err)
	}
}

func TestBackfillFromAnnotations_SeverityFromPrefix(t *testing.T) {
	t.Parallel()
	db := openBlankDB(t)
	anns := []annotations.Annotation{
		{Type: annotations.Rule, Name: "must-X", File: "f.go", Line: 1, Description: "x"},
		{Type: annotations.Rule, Name: "may-Y", File: "f.go", Line: 2, Description: "y"},
		{Type: annotations.Rule, Name: "should-Z", File: "f.go", Line: 3, Description: "z"},
		{Type: annotations.Rule, Name: "plain-W", File: "f.go", Line: 4, Description: "w"},
		{Type: annotations.Rule, Name: "MUST-CASE", File: "f.go", Line: 5, Description: "case-insensitive"},
	}
	if _, err := decisions.BackfillFromAnnotations(context.Background(), db, anns); err != nil {
		t.Fatalf("BackfillFromAnnotations: %v", err)
	}
	store := event.New(db)

	cases := map[string]event.Severity{
		"rule:must-X":    event.SeverityMust,
		"rule:may-Y":     event.SeverityMay,
		"rule:should-Z":  event.SeverityShould,
		"rule:plain-W":   event.SeverityShould,
		"rule:MUST-CASE": event.SeverityMust,
	}
	for did, want := range cases {
		got, err := store.Latest(context.Background(), event.KindDecision, did)
		if err != nil {
			t.Errorf("Latest(%s): %v", did, err)
			continue
		}
		if got.(*event.Decision).Severity != want {
			t.Errorf("%s severity = %d, want %d", did, got.(*event.Decision).Severity, want)
		}
	}
}

func TestBackfillFromAnnotations_Idempotent(t *testing.T) {
	t.Parallel()
	db := openBlankDB(t)
	anns := []annotations.Annotation{
		{Type: annotations.Rule, Name: "x", Description: "", File: "x.go", Line: 1},
	}
	if n, err := decisions.BackfillFromAnnotations(context.Background(), db, anns); err != nil || n != 1 {
		t.Fatalf("first run: n=%d err=%v", n, err)
	}
	n, err := decisions.BackfillFromAnnotations(context.Background(), db, anns)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if n != 0 {
		t.Errorf("re-run wrote %d, want 0", n)
	}
}

func TestBackfillFromAnnotations_SkipsEmptyName(t *testing.T) {
	t.Parallel()
	db := openBlankDB(t)
	anns := []annotations.Annotation{
		{Type: annotations.Rule, Name: "", Description: "no name", File: "f.go", Line: 1},
		{Type: annotations.Rule, Name: "good", Description: "ok", File: "g.go", Line: 1},
	}
	n, err := decisions.BackfillFromAnnotations(context.Background(), db, anns)
	if err != nil {
		t.Fatalf("BackfillFromAnnotations: %v", err)
	}
	if n != 1 {
		t.Errorf("wrote %d, want 1 (empty-name should be skipped)", n)
	}
}

func TestBackfillFromAnnotations_DescriptionOptional(t *testing.T) {
	t.Parallel()
	db := openBlankDB(t)
	anns := []annotations.Annotation{
		{Type: annotations.Rule, Name: "no-desc", Description: "", File: "f.go", Line: 7},
	}
	if _, err := decisions.BackfillFromAnnotations(context.Background(), db, anns); err != nil {
		t.Fatalf("BackfillFromAnnotations: %v", err)
	}
	store := event.New(db)
	got, _ := store.Latest(context.Background(), event.KindDecision, "rule:no-desc")
	dec := got.(*event.Decision)
	// Body still gets the anchor even when Description is empty.
	if !strings.Contains(dec.Body, "f.go:7") {
		t.Errorf("Body missing anchor: %q", dec.Body)
	}
}

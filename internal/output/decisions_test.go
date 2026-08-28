package output_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/output"
)

func TestRenderDecisions_Empty(t *testing.T) {
	t.Parallel()
	got := output.RenderDecisions(nil)
	if !strings.Contains(got, "No decisions recorded yet.") {
		t.Errorf("expected empty-state message, got:\n%s", got)
	}
}

func TestLoadDecisions_GroupsFilesAndSkipsSuperseded(t *testing.T) {
	t.Parallel()
	db, store := topoFixture(t)
	ctx := context.Background()

	appendMustDecision(t, store, "rule-mvcc")

	// A should-severity decision, superseded by a later version — must
	// not surface in LoadDecisions.
	if _, err := store.Append(ctx, &event.Decision{
		Hdr:      event.Header{},
		DID:      "rule-format",
		Title:    "Run gofmt",
		Severity: event.SeverityShould,
		Scope:    event.ScopeGlobal,
		Source:   "adr",
	}); err != nil {
		t.Fatalf("append decision v1: %v", err)
	}
	if _, err := store.Append(ctx, &event.Decision{
		Hdr:      event.Header{},
		DID:      "rule-format",
		Title:    "Run gofumpt",
		Severity: event.SeverityShould,
		Scope:    event.ScopeGlobal,
		Source:   "adr",
	}); err != nil {
		t.Fatalf("append decision v2: %v", err)
	}

	appendEntity(t, store, "internal/kdb/db.go")
	appendLink(t, store, "e:internal/kdb/db.go", "decision_link", "d:rule-mvcc")

	decisions, err := output.LoadDecisions(ctx, db)
	if err != nil {
		t.Fatalf("LoadDecisions: %v", err)
	}
	if len(decisions) != 2 {
		t.Fatalf("expected 2 decisions, got %d: %+v", len(decisions), decisions)
	}

	byID := map[string]output.DecisionInfo{}
	for _, d := range decisions {
		byID[d.ID] = d
	}

	mvcc, ok := byID["rule-mvcc"]
	if !ok {
		t.Fatal("expected rule-mvcc in decisions")
	}
	if len(mvcc.Files) != 1 || mvcc.Files[0] != "internal/kdb/db.go" {
		t.Errorf("rule-mvcc.Files = %v, want [internal/kdb/db.go]", mvcc.Files)
	}

	format, ok := byID["rule-format"]
	if !ok {
		t.Fatal("expected rule-format in decisions (latest version only)")
	}
	if format.Title != "Run gofumpt" {
		t.Errorf("rule-format.Title = %q, want latest version %q", format.Title, "Run gofumpt")
	}
}

func TestLoadDecisions_SkipsHistorical(t *testing.T) {
	t.Parallel()
	db, store := topoFixture(t)
	ctx := context.Background()

	appendMustDecision(t, store, "rule-active")
	if _, err := store.Append(ctx, &event.Decision{
		Hdr:      event.Header{Lifecycle: event.LifecycleHistorical},
		DID:      "rule-archived",
		Title:    "Old rule",
		Severity: event.SeverityShould,
		Scope:    event.ScopeGlobal,
		Source:   "adr",
	}); err != nil {
		t.Fatalf("append historical decision: %v", err)
	}

	decisions, err := output.LoadDecisions(ctx, db)
	if err != nil {
		t.Fatalf("LoadDecisions: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("expected only the active decision, got %d: %+v", len(decisions), decisions)
	}
	if decisions[0].ID != "rule-active" {
		t.Errorf("decision ID = %q, want rule-active", decisions[0].ID)
	}
}

func TestRenderDecisions_GroupsBySeverity(t *testing.T) {
	t.Parallel()
	decisions := []output.DecisionInfo{
		{ID: "rule-format", Title: "Run gofumpt", Severity: event.SeverityShould, Source: "adr"},
		{ID: "rule-mvcc", Title: "Always commit transactions", Severity: event.SeverityMust, Source: "adr", Files: []string{"internal/kdb/db.go"}},
	}

	got := output.RenderDecisions(decisions)

	mustIdx := strings.Index(got, "## Must")
	shouldIdx := strings.Index(got, "## Should")
	if mustIdx == -1 || shouldIdx == -1 || mustIdx > shouldIdx {
		t.Fatalf("expected Must section before Should section, got:\n%s", got)
	}
	if !strings.Contains(got, "internal/kdb/db.go") {
		t.Errorf("expected linked file in output, got:\n%s", got)
	}
}

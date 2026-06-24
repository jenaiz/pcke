package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/retrieval"
)

// seedContextDB opens a kdb at dir and adds a small entity/link graph
// the retrieval engine can traverse: focus.go imports dep.go.
func seedContextDB(t *testing.T, dir string) {
	t.Helper()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	for range 10 {
		if err := db.Grow(); err != nil {
			t.Fatalf("grow: %v", err)
		}
	}
	store := event.New(db)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	for _, e := range []struct{ eid, path string }{
		{"internal/kdb/db.go", "internal/kdb/db.go"},
		{"internal/kdb/btree.go", "internal/kdb/btree.go"},
	} {
		if _, err := store.Append(ctx, &event.Entity{
			Hdr: event.Header{CreatedAt: now}, EID: e.eid, Path: e.path, Type: "file",
		}); err != nil {
			t.Fatalf("append entity %s: %v", e.eid, err)
		}
	}
	if _, err := store.AppendLink(ctx, &event.Link{
		Hdr:    event.Header{CreatedAt: now},
		SrcRef: "e:internal/kdb/db.go", EdgeType: "imports", DstRef: "e:internal/kdb/btree.go",
	}); err != nil {
		t.Fatalf("append link: %v", err)
	}
}

func TestRunContext_RendersSectionsAndWorkflow(t *testing.T) {
	dir := t.TempDir()
	seedContextDB(t, dir)

	var buf bytes.Buffer
	if err := runContext(&buf, dir, "internal/kdb/db.go", "review", 0); err != nil {
		t.Fatalf("runContext: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "Context for internal/kdb/db.go") {
		t.Errorf("missing header:\n%s", out)
	}
	if !strings.Contains(out, "workflow: review (explicit)") {
		t.Errorf("missing explicit workflow line:\n%s", out)
	}
	if !strings.Contains(out, "internal/kdb/db.go") {
		t.Errorf("focus entity should appear as a section:\n%s", out)
	}
	// btree.go is a 1-hop neighbour; it should appear either as a section
	// or in the anticipated list.
	if !strings.Contains(out, "btree.go") {
		t.Errorf("neighbour btree.go should appear:\n%s", out)
	}
}

func TestRunContext_AutoDetectsWorkflow(t *testing.T) {
	dir := t.TempDir()
	seedContextDB(t, dir)

	var buf bytes.Buffer
	// No workflow flag -> auto-detect. dir is not a git repo, so detection
	// falls back to explore, and the origin must read "auto-detected".
	if err := runContext(&buf, dir, "internal/kdb/db.go", "", 0); err != nil {
		t.Fatalf("runContext: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "(auto-detected)") {
		t.Errorf("missing auto-detected marker:\n%s", out)
	}
	if !strings.Contains(out, "explore") {
		t.Errorf("non-git repo should detect explore:\n%s", out)
	}
}

func TestRunContext_EmptyFileErrors(t *testing.T) {
	if err := runContext(nil, t.TempDir(), "  ", "", 0); err == nil {
		t.Fatal("expected error for empty file argument")
	}
}

func TestResolveContextWorkflow_FlagWins(t *testing.T) {
	wf, detected := resolveContextWorkflow(t.TempDir(), "Refactor")
	if wf != retrieval.WorkflowRefactor {
		t.Errorf("workflow = %q, want refactor", wf)
	}
	if detected {
		t.Error("explicit flag should not be marked auto-detected")
	}
}

func TestIsTrunkBranch(t *testing.T) {
	for _, name := range []string{"main", "master", "develop", "MAIN", " main "} {
		if !isTrunkBranch(name) {
			t.Errorf("isTrunkBranch(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"fix/bug", "feature/x", ""} {
		if isTrunkBranch(name) {
			t.Errorf("isTrunkBranch(%q) = true, want false", name)
		}
	}
}

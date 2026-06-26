package decisions_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/analysis/decisions"
	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
)

// stubCommits implements decisions.CommitSource for orchestrator tests
// without requiring a real git repository.
type stubCommits []decisions.CommitInfo

func (s stubCommits) RecentCommits() ([]decisions.CommitInfo, error) {
	return []decisions.CommitInfo(s), nil
}

func setupOrchestratorRepo(t *testing.T) (string, *kdb.DB) {
	t.Helper()
	root := t.TempDir()

	// 2 ADRs.
	adrDir := filepath.Join(root, "docs", "adr")
	if err := os.MkdirAll(adrDir, 0o750); err != nil {
		t.Fatalf("mkdir adr: %v", err)
	}
	for name, body := range map[string]string{
		"0001-pivot.md":     "# ADR-0001: Pivot to event log\n\nBody.",
		"0002-deprecate.md": "# ADR-0002: Deprecate federation\n\nBody.",
	} {
		if err := os.WriteFile(filepath.Join(adrDir, name), []byte(body), 0o644); err != nil { //nolint:gosec
			t.Fatalf("write adr %s: %v", name, err)
		}
	}

	// 1 source file with 2 @pcke-rule annotations.
	srcDir := filepath.Join(root, "internal", "x")
	if err := os.MkdirAll(srcDir, 0o750); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	src := `// @pcke-rule must-validate-input: every API boundary validates.
// @pcke-rule no-raw-sql: use prepared statements.
package x

func Foo() {}
`
	if err := os.WriteFile(filepath.Join(srcDir, "x.go"), []byte(src), 0o644); err != nil { //nolint:gosec
		t.Fatalf("write src: %v", err)
	}

	// Hidden directory + vendor that should be skipped.
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o750); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	hiddenSrc := `// @pcke-rule should-not-show: this file is in .git`
	if err := os.WriteFile(filepath.Join(root, ".git", "hooks.go"), []byte(hiddenSrc), 0o644); err != nil { //nolint:gosec
		t.Fatalf("write hidden: %v", err)
	}

	// kdb.
	dbDir := t.TempDir()
	db, err := kdb.Open(dbDir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for range 5 {
		if err := db.Grow(); err != nil {
			t.Fatalf("db.Grow: %v", err)
		}
	}
	return root, db
}

func TestBackfillAll_AggregatesAllSources(t *testing.T) {
	t.Parallel()
	root, db := setupOrchestratorRepo(t)

	commits := stubCommits{
		{Hash: "aaaaaaaaaaaa1234567890ab", Author: "x", Time: time.Now().UTC(), Message: "decision: bump go version"},
		{Hash: "bbbbbbbbbbbb1234567890ab", Author: "y", Time: time.Now().UTC(), Message: "feat: not a decision"},
	}

	got, err := decisions.BackfillAll(context.Background(), db, root, commits, nil)
	if err != nil {
		t.Fatalf("BackfillAll: %v", err)
	}
	if got.ADRs != 2 {
		t.Errorf("ADRs = %d, want 2", got.ADRs)
	}
	if got.Annotations != 2 {
		t.Errorf("Annotations = %d, want 2 (hidden .git file should be skipped)", got.Annotations)
	}
	if got.Commits != 1 {
		t.Errorf("Commits = %d, want 1 (only the decision-prefixed one matches)", got.Commits)
	}
	if got.Total() != 5 {
		t.Errorf("Total = %d, want 5", got.Total())
	}

	store := event.New(db)
	for _, did := range []string{
		"adr:0001-pivot",
		"adr:0002-deprecate",
		"rule:must-validate-input",
		"rule:no-raw-sql",
		"commit:aaaaaaaaaaaa",
	} {
		if _, err := store.Latest(context.Background(), event.KindDecision, did); err != nil {
			t.Errorf("missing decision %q: %v", did, err)
		}
	}
}

func TestBackfillAll_NilCommitSourceSkipsCommitBackfill(t *testing.T) {
	t.Parallel()
	root, db := setupOrchestratorRepo(t)

	got, err := decisions.BackfillAll(context.Background(), db, root, nil, nil)
	if err != nil {
		t.Fatalf("BackfillAll: %v", err)
	}
	if got.Commits != 0 {
		t.Errorf("Commits = %d, want 0 when source is nil", got.Commits)
	}
	if got.ADRs == 0 || got.Annotations == 0 {
		t.Errorf("ADRs/annotations should still run; got %+v", got)
	}
}

func TestBackfillAll_Idempotent(t *testing.T) {
	t.Parallel()
	root, db := setupOrchestratorRepo(t)

	commits := stubCommits{
		{Hash: "aaaaaaaaaaaa1234567890ab", Author: "x", Time: time.Now().UTC(), Message: "decision: x"},
	}
	if _, err := decisions.BackfillAll(context.Background(), db, root, commits, nil); err != nil {
		t.Fatalf("first run: %v", err)
	}
	got, err := decisions.BackfillAll(context.Background(), db, root, commits, nil)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if got.Total() != 0 {
		t.Errorf("re-run wrote %d, want 0 (all idempotent)", got.Total())
	}
}

func TestWalkForAnnotations_SkipsHiddenAndVendor(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, dir := range []string{".git", "vendor", "node_modules", "src"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		body := "// @pcke-rule x: in-" + dir + "\n"
		if err := os.WriteFile(filepath.Join(root, dir, "f.go"), []byte(body), 0o644); err != nil { //nolint:gosec
			t.Fatalf("write %s: %v", dir, err)
		}
	}

	got, err := decisions.WalkForAnnotations(root)
	if err != nil {
		t.Fatalf("WalkForAnnotations: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("found %d annotations, want 1 (only src/ counted)", len(got))
	}
}

package decisions_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jenaiz/pcke/internal/analysis/decisions"
	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
)

// fixture sets up a temp repo with a docs/adr directory and the supplied
// ADR files. Returns the repo root and a *kdb.DB whose lifecycle is
// scoped to t.
func fixture(t *testing.T, files map[string]string) (root string, db *kdb.DB) {
	t.Helper()
	root = t.TempDir()
	adrDir := filepath.Join(root, "docs", "adr")
	if err := os.MkdirAll(adrDir, 0o750); err != nil {
		t.Fatalf("mkdir adr: %v", err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(adrDir, name), []byte(body), 0o644); err != nil { //nolint:gosec
			t.Fatalf("write %s: %v", name, err)
		}
	}

	dbDir := t.TempDir()
	var err error
	db, err = kdb.Open(dbDir, nil)
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

func TestBackfillFromADRs_NoDirectory(t *testing.T) {
	t.Parallel()
	dbDir := t.TempDir()
	db, err := kdb.Open(dbDir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// repoRoot has no docs/adr — must succeed with 0 decisions.
	repoRoot := t.TempDir()
	n, err := decisions.BackfillFromADRs(context.Background(), db, repoRoot)
	if err != nil {
		t.Fatalf("BackfillFromADRs: %v", err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
}

func TestBackfillFromADRs_TranslatesAllFiles(t *testing.T) {
	t.Parallel()
	root, db := fixture(t, map[string]string{
		"0001-foo.md": "# ADR-0001: Foo Decision\n\nBody A.",
		"0002-bar.md": "# ADR-0002: Bar Decision\n\nBody B.",
		"0003-baz.md": "# ADR-0003: Baz Decision\n\nBody C.",
	})

	n, err := decisions.BackfillFromADRs(context.Background(), db, root)
	if err != nil {
		t.Fatalf("BackfillFromADRs: %v", err)
	}
	if n != 3 {
		t.Errorf("wrote %d decisions, want 3", n)
	}

	store := event.New(db)
	for _, base := range []string{"0001-foo", "0002-bar", "0003-baz"} {
		got, err := store.Latest(context.Background(), event.KindDecision, "adr:"+base)
		if err != nil {
			t.Errorf("Latest(adr:%s): %v", base, err)
			continue
		}
		dec, ok := got.(*event.Decision)
		if !ok {
			t.Errorf("adr:%s: got %T, want *Decision", base, got)
			continue
		}
		if dec.Severity != event.SeverityMust {
			t.Errorf("adr:%s severity = %d, want SeverityMust", base, dec.Severity)
		}
		if dec.Scope != event.ScopeGlobal {
			t.Errorf("adr:%s scope = %d, want ScopeGlobal", base, dec.Scope)
		}
		if dec.Source != string(decisions.SourceADR) {
			t.Errorf("adr:%s source = %q, want %q", base, dec.Source, decisions.SourceADR)
		}
	}
}

// TestBackfillFromADRs_CreatesDecisionLink verifies the scan anchors each
// ADR decision back to its source file via a decision_link edge, so the
// review workflow can surface the rule when that file is touched.
func TestBackfillFromADRs_CreatesDecisionLink(t *testing.T) {
	t.Parallel()
	root, db := fixture(t, map[string]string{
		"0001-foo.md": "# ADR-0001: Foo Decision\n\nBody A.",
	})

	if _, err := decisions.BackfillFromADRs(context.Background(), db, root); err != nil {
		t.Fatalf("BackfillFromADRs: %v", err)
	}

	want := &event.Link{
		SrcRef:   "e:docs/adr/0001-foo.md",
		EdgeType: "decision_link",
		DstRef:   "d:adr:0001-foo",
	}
	store := event.New(db)
	got, err := store.Latest(context.Background(), event.KindLink, want.ID())
	if err != nil {
		t.Fatalf("Latest(decision_link): %v", err)
	}
	link, ok := got.(*event.Link)
	if !ok {
		t.Fatalf("got %T, want *event.Link", got)
	}
	if link.SrcRef != want.SrcRef || link.EdgeType != want.EdgeType || link.DstRef != want.DstRef {
		t.Errorf("link = {%s -%s-> %s}, want {%s -%s-> %s}",
			link.SrcRef, link.EdgeType, link.DstRef,
			want.SrcRef, want.EdgeType, want.DstRef)
	}
}

func TestBackfillFromADRs_TitleFromFirstHeading(t *testing.T) {
	t.Parallel()
	root, db := fixture(t, map[string]string{
		"0010-x.md": "# ADR-0010: This Is The Title\n\nLong body content goes here\n\n## Subsection\n\nDetail.",
	})
	if _, err := decisions.BackfillFromADRs(context.Background(), db, root); err != nil {
		t.Fatalf("BackfillFromADRs: %v", err)
	}

	store := event.New(db)
	got, err := store.Latest(context.Background(), event.KindDecision, "adr:0010-x")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	dec := got.(*event.Decision)
	if dec.Title != "ADR-0010: This Is The Title" {
		t.Errorf("Title = %q, want %q", dec.Title, "ADR-0010: This Is The Title")
	}
	// Body should retain the full file content including subsection markers.
	if dec.Body == "" || dec.Body == dec.Title {
		t.Errorf("Body lost full content: %q", dec.Body)
	}
}

func TestBackfillFromADRs_TruncatesLongTitle(t *testing.T) {
	t.Parallel()
	long := make([]rune, 300)
	for i := range long {
		long[i] = 'a'
	}
	root, db := fixture(t, map[string]string{
		"0011-long.md": "# " + string(long) + "\n\nBody",
	})
	if _, err := decisions.BackfillFromADRs(context.Background(), db, root); err != nil {
		t.Fatalf("BackfillFromADRs: %v", err)
	}
	store := event.New(db)
	got, _ := store.Latest(context.Background(), event.KindDecision, "adr:0011-long")
	dec := got.(*event.Decision)
	if r := []rune(dec.Title); len(r) != 200 {
		t.Errorf("Title length = %d runes, want 200", len(r))
	}
}

func TestBackfillFromADRs_Idempotent(t *testing.T) {
	t.Parallel()
	root, db := fixture(t, map[string]string{
		"0001-foo.md": "# Foo",
		"0002-bar.md": "# Bar",
	})
	if n, err := decisions.BackfillFromADRs(context.Background(), db, root); err != nil || n != 2 {
		t.Fatalf("first run: n=%d err=%v, want n=2 err=nil", n, err)
	}
	// Second run: same files, must skip and write 0.
	n, err := decisions.BackfillFromADRs(context.Background(), db, root)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if n != 0 {
		t.Errorf("re-run wrote %d, want 0 (idempotent)", n)
	}

	// Each Decision still at v1.
	store := event.New(db)
	for _, did := range []string{"adr:0001-foo", "adr:0002-bar"} {
		got, err := store.Latest(context.Background(), event.KindDecision, did)
		if err != nil {
			t.Fatalf("Latest(%s): %v", did, err)
		}
		if got.Header().Version != 1 {
			t.Errorf("%s version = %d, want 1", did, got.Header().Version)
		}
	}
}

func TestBackfillFromADRs_SkipsSubdirectories(t *testing.T) {
	t.Parallel()
	root, db := fixture(t, map[string]string{
		"0001-foo.md": "# Foo",
	})
	// Add a nested directory inside docs/adr/ — must be ignored.
	if err := os.MkdirAll(filepath.Join(root, "docs", "adr", "draft"), 0o750); err != nil {
		t.Fatalf("mkdir draft: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "adr", "draft", "wip.md"), []byte("# WIP"), 0o644); err != nil { //nolint:gosec
		t.Fatalf("write nested: %v", err)
	}

	n, err := decisions.BackfillFromADRs(context.Background(), db, root)
	if err != nil {
		t.Fatalf("BackfillFromADRs: %v", err)
	}
	if n != 1 {
		t.Errorf("wrote %d, want 1 (nested file should be skipped)", n)
	}
}

func TestBackfillFromADRs_SkipsNonMd(t *testing.T) {
	t.Parallel()
	root, db := fixture(t, map[string]string{
		"0001-foo.md": "# Foo",
		"notes.txt":   "not an ADR",
		"draft.org":   "* Org headings",
	})

	n, err := decisions.BackfillFromADRs(context.Background(), db, root)
	if err != nil {
		t.Fatalf("BackfillFromADRs: %v", err)
	}
	if n != 1 {
		t.Errorf("wrote %d, want 1 (non-.md ignored)", n)
	}
}

func TestBackfillFromADRs_PreservesFileMtime(t *testing.T) {
	t.Parallel()
	root, db := fixture(t, map[string]string{
		"0001-foo.md": "# Foo",
	})
	if _, err := decisions.BackfillFromADRs(context.Background(), db, root); err != nil {
		t.Fatalf("BackfillFromADRs: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "docs", "adr", "0001-foo.md"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	store := event.New(db)
	got, err := store.Latest(context.Background(), event.KindDecision, "adr:0001-foo")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if !got.Header().CreatedAt.Equal(info.ModTime().UTC()) {
		t.Errorf("CreatedAt = %v, want %v (file mtime)", got.Header().CreatedAt, info.ModTime().UTC())
	}
}

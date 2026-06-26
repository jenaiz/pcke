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

// writeDoc creates <root>/docs/<name> with the given content.
func writeDoc(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, "docs")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func openDocsDB(t *testing.T) *kdb.DB {
	t.Helper()
	db, err := kdb.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestBackfillFromDocs_HarvestsH2Sections(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "getting-started.md", "# Getting Started\n\nIntro text.\n\n## Install\n\nRun make build.\n\n## Configure\n\nEdit config.toml.\n")

	db := openDocsDB(t)
	n, err := decisions.BackfillFromDocs(context.Background(), db, root, nil)
	if err != nil {
		t.Fatalf("BackfillFromDocs: %v", err)
	}
	if n != 2 {
		t.Fatalf("wrote %d decisions, want 2 (one per H2)", n)
	}

	store := event.New(db)
	ctx := context.Background()
	for _, did := range []string{"doc:getting-started:install", "doc:getting-started:configure"} {
		evt, err := store.Latest(ctx, event.KindDecision, did)
		if err != nil {
			t.Fatalf("missing decision %q: %v", did, err)
		}
		d, ok := evt.(*event.Decision)
		if !ok {
			t.Fatalf("decision %q is not *event.Decision", did)
		}
		if d.Source != string(decisions.SourceDoc) {
			t.Errorf("decision %q source = %q, want doc", did, d.Source)
		}
		if d.Scope != event.ScopeGlobal {
			t.Errorf("unmapped doc %q scope = %v, want global", did, d.Scope)
		}
	}

	// The decision is anchored to its source doc file.
	assertLinked(t, db, "e:docs/getting-started.md", "d:doc:getting-started:install")
}

func TestBackfillFromDocs_ModuleScopedLinksToModuleFiles(t *testing.T) {
	root := t.TempDir()
	// architecture.md maps to the internal/kdb module prefix.
	writeDoc(t, root, "architecture.md", "# Architecture\n\n## Storage Engine\n\nThe kdb engine.\n")

	db := openDocsDB(t)
	files := []string{
		"internal/kdb/btree/split.go", // under the module prefix
		"internal/kdb/db.go",          // under the module prefix
		"internal/query/lexer.go",     // outside the module prefix
	}
	if _, err := decisions.BackfillFromDocs(context.Background(), db, root, files); err != nil {
		t.Fatalf("BackfillFromDocs: %v", err)
	}

	store := event.New(db)
	evt, err := store.Latest(context.Background(), event.KindDecision, "doc:architecture:storage-engine")
	if err != nil {
		t.Fatalf("missing architecture decision: %v", err)
	}
	if d := evt.(*event.Decision); d.Scope != event.ScopeModule {
		t.Errorf("architecture decision scope = %v, want module", d.Scope)
	}

	did := "d:doc:architecture:storage-engine"
	// Files under internal/kdb are linked (Option A); the query file is not.
	assertLinked(t, db, "e:internal/kdb/btree/split.go", did)
	assertLinked(t, db, "e:internal/kdb/db.go", did)
	assertNotLinked(t, db, "e:internal/query/lexer.go", did)
}

func TestBackfillFromDocs_Idempotent(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "architecture.md", "# Architecture\n\n## Storage Engine\n\nThe kdb engine.\n")
	db := openDocsDB(t)
	files := []string{"internal/kdb/db.go"}

	if _, err := decisions.BackfillFromDocs(context.Background(), db, root, files); err != nil {
		t.Fatalf("first run: %v", err)
	}
	n, err := decisions.BackfillFromDocs(context.Background(), db, root, files)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if n != 0 {
		t.Errorf("re-run wrote %d decisions, want 0 (idempotent)", n)
	}
}

func TestBackfillFromDocs_NoDocsDir(t *testing.T) {
	db := openDocsDB(t)
	n, err := decisions.BackfillFromDocs(context.Background(), db, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("BackfillFromDocs: %v", err)
	}
	if n != 0 {
		t.Errorf("wrote %d, want 0 for a repo without docs", n)
	}
}

// assertLinked fails unless a decision_link edge srcRef -> dstRef exists.
func assertLinked(t *testing.T, db *kdb.DB, srcRef, dstRef string) {
	t.Helper()
	if !hasDecisionLink(t, db, srcRef, dstRef) {
		t.Errorf("expected decision_link %s -> %s, none found", srcRef, dstRef)
	}
}

// assertNotLinked fails if a decision_link edge srcRef -> dstRef exists.
func assertNotLinked(t *testing.T, db *kdb.DB, srcRef, dstRef string) {
	t.Helper()
	if hasDecisionLink(t, db, srcRef, dstRef) {
		t.Errorf("unexpected decision_link %s -> %s", srcRef, dstRef)
	}
}

func hasDecisionLink(t *testing.T, db *kdb.DB, srcRef, dstRef string) bool {
	t.Helper()
	store := event.New(db)
	var found bool
	err := store.ReverseLinks(context.Background(), dstRef, "decision_link", func(l *event.Link) error {
		if l.SrcRef == srcRef {
			found = true
		}
		return nil
	})
	if err != nil && !found {
		// ReverseLinks returns nil when there are simply no matches; a
		// real error is worth surfacing.
		t.Logf("ReverseLinks(%s): %v", dstRef, err)
	}
	return found
}

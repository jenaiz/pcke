package output

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/analysis"
	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// fixtureNodes returns a deterministic set of knowledge nodes for golden tests.
func fixtureNodes() []analysis.KnowledgeNode {
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	return []analysis.KnowledgeNode{
		{
			ID: "cmd/main.go", Type: "file", Name: "main.go", FilePath: "cmd/main.go",
			Language: "Go", Module: "cmd", Class: "entry_point", Stability: 0.9,
			Status: "active", ContentHash: "aaa", CreatedAt: ts, UpdatedAt: ts,
		},
		{
			ID: "internal/server.go", Type: "file", Name: "server.go", FilePath: "internal/server.go",
			Language: "Go", Module: "internal", Class: "source", Stability: 0.5,
			Status: "active", ContentHash: "bbb", CreatedAt: ts, UpdatedAt: ts,
		},
		{
			ID: "internal/server_test.go", Type: "file", Name: "server_test.go", FilePath: "internal/server_test.go",
			Language: "Go", Module: "internal", Class: "test", Stability: 0.6,
			Status: "active", ContentHash: "ccc", CreatedAt: ts, UpdatedAt: ts,
		},
		{
			ID: "README.md", Type: "file", Name: "README.md", FilePath: "README.md",
			Language: "Markdown", Module: "", Class: "doc", Stability: 0.95,
			Status: "active", ContentHash: "ddd", CreatedAt: ts, UpdatedAt: ts,
		},
		{
			ID: "Makefile", Type: "file", Name: "Makefile", FilePath: "Makefile",
			Language: "", Module: "", Class: "config", Stability: 0.8,
			Status: "active", ContentHash: "eee", CreatedAt: ts, UpdatedAt: ts,
		},
	}
}

func TestSyncGolden(t *testing.T) {
	goldenDir := filepath.Join("testdata", "golden")

	// Create a temp directory for output.
	outDir := t.TempDir()

	// Set up a kdb with fixture nodes.
	dbPath := filepath.Join(t.TempDir(), "test.kdb")
	db, err := kdb.Open(dbPath, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	nodes := fixtureNodes()
	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		for _, n := range nodes {
			val, err := json.Marshal(n)
			if err != nil {
				return err
			}
			if err := wtx.Put([]byte("kn:"+n.ID), val); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed db: %v", err)
	}

	// Run sync.
	renderer := NewRenderer(outDir, db)
	result, err := renderer.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	if result.FilesWritten == 0 {
		t.Fatal("no files written")
	}

	// Compare each output file against golden.
	goldenFiles := []string{
		".context/ARCHITECTURE.md",
		".context/CONVENTIONS.md",
		".context/HISTORY.md",
		".context/DECISIONS.md",
		".context/CONSTRAINTS.md",
		".context/MODULES/cmd.md",
		".context/MODULES/internal.md",
		".github/copilot-instructions.md",
		".claude/CLAUDE.md",
	}

	update := os.Getenv("UPDATE_GOLDEN") != ""

	for _, relPath := range goldenFiles {
		actual, err := os.ReadFile(filepath.Join(outDir, relPath)) //nolint:gosec
		if err != nil {
			t.Errorf("read output %s: %v", relPath, err)
			continue
		}

		goldenPath := filepath.Join(goldenDir, relPath)

		if update {
			if err := os.MkdirAll(filepath.Dir(goldenPath), 0o750); err != nil {
				t.Fatalf("mkdir golden: %v", err)
			}
			if err := os.WriteFile(goldenPath, actual, 0o600); err != nil { //nolint:gosec
				t.Fatalf("write golden: %v", err)
			}
			continue
		}

		golden, err := os.ReadFile(goldenPath) //nolint:gosec
		if err != nil {
			t.Errorf("read golden %s: %v (run with UPDATE_GOLDEN=1 to create)", relPath, err)
			continue
		}

		if string(actual) != string(golden) {
			t.Errorf("%s differs from golden:\n--- golden ---\n%s\n--- actual ---\n%s", relPath, golden, actual)
		}
	}
}

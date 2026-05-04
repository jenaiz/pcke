package context

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/analysis"
	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

func setupTestDB(t *testing.T) *kdb.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func putNode(t *testing.T, db *kdb.DB, node map[string]any) {
	t.Helper()
	id := node["id"].(string)
	data, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal node: %v", err)
	}
	if err := db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		return wtx.Put([]byte("kn:"+id), data)
	}); err != nil {
		t.Fatalf("put node: %v", err)
	}
}

func putEvolutionLog(t *testing.T, db *kdb.DB, log analysis.EvolutionLog) {
	t.Helper()
	data, err := json.Marshal(log)
	if err != nil {
		t.Fatalf("marshal log: %v", err)
	}
	if err := db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		return wtx.Put([]byte("el:"+log.ID), data)
	}); err != nil {
		t.Fatalf("put evolution log: %v", err)
	}
}

func TestEngine_Assemble_Empty(t *testing.T) {
	db := setupTestDB(t)
	eng := NewEngine(db, t.TempDir(), DefaultConfig())

	pkg, err := eng.Assemble(context.Background(), Request{
		FilePath: "internal/kdb/db.go",
		Budget:   2000,
	})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if pkg == nil {
		t.Fatal("expected non-nil package")
	}
	if pkg.TokensUsed < 0 {
		t.Fatal("tokens used should be >= 0")
	}
}

func TestEngine_Assemble_WithConstraints(t *testing.T) {
	db := setupTestDB(t)

	putNode(t, db, map[string]any{
		"id":         "rule-1",
		"name":       "No raw SQL",
		"type":       "rule",
		"class":      "constraint",
		"source":     "manual",
		"severity":   "must",
		"scope":      "global",
		"status":     "active",
		"module":     "",
		"file_path":  "",
		"language":   "",
		"created_at": time.Now().Format(time.RFC3339),
		"updated_at": time.Now().Format(time.RFC3339),
	})

	eng := NewEngine(db, t.TempDir(), DefaultConfig())
	pkg, err := eng.Assemble(context.Background(), Request{
		FilePath: "internal/kdb/db.go",
		Budget:   2000,
	})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(pkg.Sections) == 0 {
		t.Fatal("expected at least 1 section from constraint")
	}
	if pkg.Sections[0].Type != "constraint" {
		t.Fatalf("expected constraint section, got %q", pkg.Sections[0].Type)
	}
}

func TestEngine_Assemble_WithWarnings(t *testing.T) {
	db := setupTestDB(t)

	putNode(t, db, map[string]any{
		"id":         "rule-must",
		"name":       "Never skip tests",
		"type":       "rule",
		"class":      "constraint",
		"source":     "manual",
		"severity":   "must",
		"scope":      "global",
		"status":     "active",
		"module":     "",
		"file_path":  "",
		"language":   "",
		"created_at": time.Now().Format(time.RFC3339),
		"updated_at": time.Now().Format(time.RFC3339),
	})

	cfg := DefaultConfig()
	cfg.ProactiveWarnings = true
	eng := NewEngine(db, t.TempDir(), cfg)

	pkg, err := eng.Assemble(context.Background(), Request{
		FilePath: "foo.go",
		Budget:   2000,
	})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(pkg.Warnings) == 0 {
		t.Fatal("expected proactive warning for must-severity rule")
	}
	if pkg.Warnings[0].Rule != "Never skip tests" {
		t.Fatalf("unexpected warning rule: %q", pkg.Warnings[0].Rule)
	}
}

func TestEngine_Assemble_BudgetRespected(t *testing.T) {
	db := setupTestDB(t)

	// Add a node with long content to test budget enforcement.
	putNode(t, db, map[string]any{
		"id":         "file-1",
		"name":       "big_file.go",
		"type":       "file",
		"class":      "source",
		"source":     "scan",
		"status":     "active",
		"module":     "internal/kdb",
		"file_path":  "internal/kdb/big_file.go",
		"language":   "Go",
		"created_at": time.Now().Format(time.RFC3339),
		"updated_at": time.Now().Format(time.RFC3339),
	})

	eng := NewEngine(db, t.TempDir(), DefaultConfig())
	pkg, err := eng.Assemble(context.Background(), Request{
		FilePath: "internal/kdb/big_file.go",
		Budget:   100,
	})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	// Allow ±10% tolerance.
	if pkg.TokensUsed > 110 {
		t.Fatalf("tokens used %d exceeds budget 100 by more than 10%%", pkg.TokensUsed)
	}
}

func TestEngine_Assemble_SessionNovelty(t *testing.T) {
	db := setupTestDB(t)

	putNode(t, db, map[string]any{
		"id":         "rule-2",
		"name":       "Use interfaces",
		"type":       "rule",
		"class":      "constraint",
		"source":     "manual",
		"severity":   "should",
		"scope":      "global",
		"status":     "active",
		"module":     "",
		"file_path":  "",
		"language":   "",
		"created_at": time.Now().Format(time.RFC3339),
		"updated_at": time.Now().Format(time.RFC3339),
	})

	eng := NewEngine(db, t.TempDir(), DefaultConfig())
	sess := NewSession()
	eng.SetSession(sess)

	// First call.
	pkg1, err := eng.Assemble(context.Background(), Request{
		FilePath: "foo.go",
		Budget:   2000,
	})
	if err != nil {
		t.Fatalf("first assemble: %v", err)
	}

	// Second call — novelty should decrease scores.
	pkg2, err := eng.Assemble(context.Background(), Request{
		FilePath: "foo.go",
		Budget:   2000,
	})
	if err != nil {
		t.Fatalf("second assemble: %v", err)
	}

	if len(pkg1.Sections) == 0 || len(pkg2.Sections) == 0 {
		t.Skip("no sections to compare")
	}

	// Second call should have lower scores due to novelty decay.
	if pkg2.Sections[0].Score >= pkg1.Sections[0].Score {
		t.Fatalf("expected lower score on second call: first=%f, second=%f",
			pkg1.Sections[0].Score, pkg2.Sections[0].Score)
	}
}

func TestEngine_Assemble_WithHistory(t *testing.T) {
	db := setupTestDB(t)

	putNode(t, db, map[string]any{
		"id":         "node-1",
		"name":       "db.go",
		"type":       "file",
		"class":      "source",
		"source":     "scan",
		"status":     "active",
		"module":     "internal/kdb",
		"file_path":  "internal/kdb/db.go",
		"language":   "Go",
		"created_at": time.Now().Format(time.RFC3339),
		"updated_at": time.Now().Format(time.RFC3339),
	})

	putEvolutionLog(t, db, analysis.EvolutionLog{
		ID:         "el-1",
		NodeID:     "node-1",
		CommitHash: "abc1234567890",
		ChangeType: "feat",
		Author:     "dev",
		Timestamp:  time.Now().Add(-2 * 24 * time.Hour),
	})

	eng := NewEngine(db, t.TempDir(), DefaultConfig())
	pkg, err := eng.Assemble(context.Background(), Request{
		FilePath: "internal/kdb/db.go",
		Budget:   2000,
	})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}

	hasHistory := false
	for _, s := range pkg.Sections {
		if s.Type == "history" {
			hasHistory = true
			break
		}
	}
	if !hasHistory {
		t.Fatal("expected history section")
	}
}

func TestEngine_Assemble_SortedByScore(t *testing.T) {
	db := setupTestDB(t)

	// Two rules with different severities.
	putNode(t, db, map[string]any{
		"id":         "rule-must",
		"name":       "Critical rule",
		"type":       "rule",
		"class":      "constraint",
		"source":     "manual",
		"severity":   "must",
		"scope":      "global",
		"status":     "active",
		"module":     "",
		"file_path":  "",
		"language":   "",
		"created_at": time.Now().Format(time.RFC3339),
		"updated_at": time.Now().Format(time.RFC3339),
	})

	putNode(t, db, map[string]any{
		"id":         "rule-may",
		"name":       "Nice to have",
		"type":       "rule",
		"class":      "constraint",
		"source":     "manual",
		"severity":   "may",
		"scope":      "global",
		"status":     "active",
		"module":     "",
		"file_path":  "",
		"language":   "",
		"created_at": time.Now().Format(time.RFC3339),
		"updated_at": time.Now().Format(time.RFC3339),
	})

	eng := NewEngine(db, t.TempDir(), DefaultConfig())
	pkg, err := eng.Assemble(context.Background(), Request{
		FilePath: "foo.go",
		Budget:   2000,
	})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}

	if len(pkg.Sections) < 2 {
		t.Fatalf("expected at least 2 sections, got %d", len(pkg.Sections))
	}

	// Sections should be ordered by score descending.
	for i := 1; i < len(pkg.Sections); i++ {
		if pkg.Sections[i].Score > pkg.Sections[i-1].Score {
			t.Fatalf("sections not sorted: [%d].Score=%f > [%d].Score=%f",
				i, pkg.Sections[i].Score, i-1, pkg.Sections[i-1].Score)
		}
	}
}

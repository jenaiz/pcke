package onboard

import (
	"context"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/analysis"
)

func TestEngine_Generate_MinimumSections(t *testing.T) {
	nodes := makeTestNodes()
	engine := &Engine{
		Nodes:    nodes,
		RepoPath: "/tmp/test-repo",
	}

	w, err := engine.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if len(w.Sections) < 6 {
		t.Errorf("expected >= 6 sections, got %d", len(w.Sections))
	}

	if w.NodeCount != len(nodes) {
		t.Errorf("NodeCount = %d, want %d", w.NodeCount, len(nodes))
	}

	if w.ModuleCount == 0 {
		t.Error("ModuleCount = 0, want > 0")
	}
}

func TestEngine_Generate_SectionsOrdered(t *testing.T) {
	engine := &Engine{
		Nodes:    makeTestNodes(),
		RepoPath: "/tmp/test-repo",
	}

	w, err := engine.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	for i, sec := range w.Sections {
		if sec.Order != i+1 {
			t.Errorf("section %d: Order = %d, want %d", i, sec.Order, i+1)
		}
	}
}

func TestEngine_Generate_SkipSections(t *testing.T) {
	engine := &Engine{
		Nodes:    makeTestNodes(),
		RepoPath: "/tmp/test-repo",
		Config: &Config{
			Walkthrough: WalkthroughConfig{
				SkipSections: []string{"conventions", "constraints"},
			},
		},
	}

	w, err := engine.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	for _, sec := range w.Sections {
		if sec.Name == "conventions" || sec.Name == "constraints" {
			t.Errorf("section %q should have been skipped", sec.Name)
		}
	}
}

func TestEngine_Generate_CustomTitle(t *testing.T) {
	engine := &Engine{
		Nodes:    makeTestNodes(),
		RepoPath: "/tmp/test-repo",
		Config: &Config{
			Walkthrough: WalkthroughConfig{
				Title: "Welcome to TestProject",
			},
		},
	}

	w, err := engine.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if w.Title != "Welcome to TestProject" {
		t.Errorf("Title = %q, want %q", w.Title, "Welcome to TestProject")
	}
}

func TestEngine_Generate_CustomSections(t *testing.T) {
	engine := &Engine{
		Nodes:    makeTestNodes(),
		RepoPath: "/tmp/test-repo",
		Config: &Config{
			Walkthrough: WalkthroughConfig{
				CustomSections: []CustomSection{
					{
						Name:     "Team Conventions",
						Content:  "We use trunk-based development.",
						Position: "after:conventions",
					},
				},
			},
		},
	}

	w, err := engine.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	found := false
	for _, sec := range w.Sections {
		if sec.Name == "Team Conventions" && sec.IsCustom {
			found = true
		}
	}
	if !found {
		t.Error("custom section 'Team Conventions' not found")
	}
}

func TestEngine_GenerateForModule(t *testing.T) {
	engine := &Engine{
		Nodes:    makeTestNodes(),
		RepoPath: "/tmp/test-repo",
	}

	w, err := engine.GenerateForModule(context.Background(), "internal/kdb")
	if err != nil {
		t.Fatalf("GenerateForModule() error: %v", err)
	}

	if w.NodeCount == 0 {
		t.Error("NodeCount = 0 for module walkthrough")
	}
}

func TestEngine_GenerateForModule_NotFound(t *testing.T) {
	engine := &Engine{
		Nodes:    makeTestNodes(),
		RepoPath: "/tmp/test-repo",
	}

	_, err := engine.GenerateForModule(context.Background(), "nonexistent/module")
	if err == nil {
		t.Fatal("expected error for nonexistent module")
	}
}

func TestEngine_EntryPointDetection(t *testing.T) {
	tests := []struct {
		node analysis.KnowledgeNode
		want bool
	}{
		{analysis.KnowledgeNode{Class: "entry_point", FilePath: "cmd/app/main.go"}, true},
		{analysis.KnowledgeNode{Class: "api", FilePath: "api/handler.go"}, true},
		{analysis.KnowledgeNode{Name: "main.go", FilePath: "main.go"}, true},
		{analysis.KnowledgeNode{Class: "lib", FilePath: "internal/kdb/db.go"}, false},
	}

	for _, tt := range tests {
		got := isEntryPoint(tt.node)
		if got != tt.want {
			t.Errorf("isEntryPoint(%v) = %v, want %v", tt.node.FilePath, got, tt.want)
		}
	}
}

func TestInsertCustomSection_After(t *testing.T) {
	sections := []Section{
		{Name: "a", Order: 1},
		{Name: "b", Order: 2},
		{Name: "c", Order: 3},
	}
	custom := Section{Name: "x", IsCustom: true}

	result := insertCustomSection(sections, custom, "after:b")
	if len(result) != 4 {
		t.Fatalf("len = %d, want 4", len(result))
	}
	if result[2].Name != "x" {
		t.Errorf("result[2].Name = %q, want %q", result[2].Name, "x")
	}
}

func TestInsertCustomSection_Before(t *testing.T) {
	sections := []Section{
		{Name: "a", Order: 1},
		{Name: "b", Order: 2},
		{Name: "c", Order: 3},
	}
	custom := Section{Name: "x", IsCustom: true}

	result := insertCustomSection(sections, custom, "before:b")
	if len(result) != 4 {
		t.Fatalf("len = %d, want 4", len(result))
	}
	if result[1].Name != "x" {
		t.Errorf("result[1].Name = %q, want %q", result[1].Name, "x")
	}
}

func TestInsertCustomSection_NoPosition(t *testing.T) {
	sections := []Section{
		{Name: "a", Order: 1},
	}
	custom := Section{Name: "x", IsCustom: true}

	result := insertCustomSection(sections, custom, "")
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if result[1].Name != "x" {
		t.Errorf("expected x at end")
	}
}

func makeTestNodes() []analysis.KnowledgeNode {
	now := time.Now()
	return []analysis.KnowledgeNode{
		{ID: "1", Name: "main.go", FilePath: "cmd/pcke/main.go", Language: "Go", Module: "cmd/pcke", Class: "entry_point", Stability: 0.9, CreatedAt: now, UpdatedAt: now},
		{ID: "2", Name: "db.go", FilePath: "internal/kdb/db.go", Language: "Go", Module: "internal/kdb", Class: "lib", Stability: 0.85, CreatedAt: now, UpdatedAt: now},
		{ID: "3", Name: "tx.go", FilePath: "internal/kdb/tx.go", Language: "Go", Module: "internal/kdb", Class: "lib", Stability: 0.8, CreatedAt: now, UpdatedAt: now},
		{ID: "4", Name: "page.go", FilePath: "internal/kdb/page/page.go", Language: "Go", Module: "internal/kdb/page", Class: "lib", Stability: 0.7, CreatedAt: now, UpdatedAt: now},
		{ID: "5", Name: "scanner.go", FilePath: "internal/analysis/scanner.go", Language: "Go", Module: "internal/analysis", Class: "lib", Stability: 0.75, CreatedAt: now, UpdatedAt: now},
		{ID: "6", Name: "server.go", FilePath: "internal/mcp/server.go", Language: "Go", Module: "internal/mcp", Class: "api", Stability: 0.6, CreatedAt: now, UpdatedAt: now},
		{ID: "7", Name: "tools.go", FilePath: "internal/mcp/tools.go", Language: "Go", Module: "internal/mcp", Class: "lib", Stability: 0.65, CreatedAt: now, UpdatedAt: now},
		{ID: "8", Name: "render.go", FilePath: "internal/output/render.go", Language: "Go", Module: "internal/output", Class: "lib", Stability: 0.8, CreatedAt: now, UpdatedAt: now},
		{ID: "9", Name: "config.go", FilePath: "internal/config/config.go", Language: "Go", Module: "internal/config", Class: "lib", Stability: 0.95, CreatedAt: now, UpdatedAt: now},
		{ID: "10", Name: "README.md", FilePath: "README.md", Language: "Markdown", Module: "(root)", Class: "doc", Stability: 1.0, CreatedAt: now, UpdatedAt: now},
	}
}

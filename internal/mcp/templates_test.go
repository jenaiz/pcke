package mcp_test

import (
	"os"
	"path/filepath"
	"testing"

	pckmcp "github.com/jenaiz/pcke/internal/mcp"
)

func TestLoadCustomTemplates_NoDir(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	templates := pckmcp.LoadCustomTemplates(tmp)
	if len(templates) != 0 {
		t.Errorf("expected empty, got %d templates", len(templates))
	}
}

func TestLoadCustomTemplates_ValidTOML(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".pcke", "templates")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	content := `name = "custom-review"
description = "Custom review template"
has_module = true
sections = ["architecture", "constraints"]
`
	if err := os.WriteFile(filepath.Join(dir, "review.toml"), []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	templates := pckmcp.LoadCustomTemplates(tmp)
	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	td := templates[0]
	if td.Name != "custom-review" {
		t.Errorf("name = %q, want %q", td.Name, "custom-review")
	}
	if td.Description != "Custom review template" {
		t.Errorf("description = %q, want %q", td.Description, "Custom review template")
	}
	if !td.HasModule {
		t.Error("expected HasModule = true")
	}
	if len(td.Sections) != 2 {
		t.Fatalf("sections len = %d, want 2", len(td.Sections))
	}
}

func TestLoadCustomTemplates_InvalidTOML(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".pcke", "templates")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "bad.toml"), []byte("not valid [[ toml"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	templates := pckmcp.LoadCustomTemplates(tmp)
	if len(templates) != 0 {
		t.Errorf("expected 0 templates for malformed TOML, got %d", len(templates))
	}
}

func TestLoadCustomTemplates_NoValidSections(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".pcke", "templates")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	content := `name = "bad-sections"
description = "No valid sections"
sections = ["nonexistent", "fakesection"]
`
	if err := os.WriteFile(filepath.Join(dir, "bad.toml"), []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	templates := pckmcp.LoadCustomTemplates(tmp)
	if len(templates) != 0 {
		t.Errorf("expected 0 templates (no valid sections), got %d", len(templates))
	}
}

func TestFilterValidSections(t *testing.T) {
	t.Parallel()
	input := []string{"architecture", "nonexistent", "constraints", "bogus", "history"}
	got := pckmcp.FilterValidSections(input)
	want := []string{"architecture", "constraints", "history"}

	if len(got) != len(want) {
		t.Fatalf("FilterValidSections = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("FilterValidSections[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFilterValidSections_Empty(t *testing.T) {
	t.Parallel()
	got := pckmcp.FilterValidSections(nil)
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestLoadCustomTemplates_EmptyRoot(t *testing.T) {
	t.Parallel()
	templates := pckmcp.LoadCustomTemplates("")
	if len(templates) != 0 {
		t.Errorf("expected empty for empty root, got %d", len(templates))
	}
}

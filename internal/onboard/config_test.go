package onboard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_Default(t *testing.T) {
	// Temp dir with no config file.
	dir := t.TempDir()

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}

	if cfg.Walkthrough.Title != "" {
		t.Errorf("Title = %q, want empty", cfg.Walkthrough.Title)
	}
}

func TestLoadConfig_FromFile(t *testing.T) {
	dir := t.TempDir()
	pckeDir := filepath.Join(dir, ".pcke")
	if err := os.MkdirAll(pckeDir, 0o750); err != nil {
		t.Fatal(err)
	}

	content := `[walkthrough]
title = "Welcome"
highlight_modules = ["internal/kdb", "cmd/pcke"]
skip_sections = ["history"]

[[walkthrough.custom_sections]]
name = "Team Rules"
content = "Follow the rules."
position = "after:conventions"
`
	if err := os.WriteFile(filepath.Join(pckeDir, "onboarding.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}

	if cfg.Walkthrough.Title != "Welcome" {
		t.Errorf("Title = %q, want %q", cfg.Walkthrough.Title, "Welcome")
	}
	if len(cfg.Walkthrough.HighlightModules) != 2 {
		t.Errorf("HighlightModules len = %d, want 2", len(cfg.Walkthrough.HighlightModules))
	}
	if len(cfg.Walkthrough.SkipSections) != 1 {
		t.Errorf("SkipSections len = %d, want 1", len(cfg.Walkthrough.SkipSections))
	}
	if len(cfg.Walkthrough.CustomSections) != 1 {
		t.Errorf("CustomSections len = %d, want 1", len(cfg.Walkthrough.CustomSections))
	}
	if cfg.Walkthrough.CustomSections[0].Name != "Team Rules" {
		t.Errorf("CustomSection name = %q, want %q", cfg.Walkthrough.CustomSections[0].Name, "Team Rules")
	}
}

func TestLoadConfig_InvalidTOML(t *testing.T) {
	dir := t.TempDir()
	pckeDir := filepath.Join(dir, ".pcke")
	if err := os.MkdirAll(pckeDir, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(pckeDir, "onboarding.toml"), []byte("invalid{{{{"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}

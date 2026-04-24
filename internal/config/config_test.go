package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jenaiz/pcke/internal/config"
)

func TestDefaults(t *testing.T) {
	t.Parallel()

	cfg := config.Defaults()

	if !cfg.Scan.RedactSecrets {
		t.Error("RedactSecrets default should be true")
	}
	if cfg.Scan.IncludeIgnored {
		t.Error("IncludeIgnored default should be false")
	}
	if cfg.Scan.MaxFileBytes != 2*1024*1024 {
		t.Errorf("MaxFileBytes = %d, want %d", cfg.Scan.MaxFileBytes, 2*1024*1024)
	}
	if cfg.KDB.WALSegmentMB != 16 {
		t.Errorf("WALSegmentMB = %d, want 16", cfg.KDB.WALSegmentMB)
	}
	if cfg.KDB.GracefulShutdownS != 10 {
		t.Errorf("GracefulShutdownS = %d, want 10", cfg.KDB.GracefulShutdownS)
	}
	if cfg.FTS.TokenizerCJKMode != "segmenter" {
		t.Errorf("TokenizerCJKMode = %q, want segmenter", cfg.FTS.TokenizerCJKMode)
	}
	if cfg.MCP.ReadTimeoutS != 30 {
		t.Errorf("ReadTimeoutS = %d, want 30", cfg.MCP.ReadTimeoutS)
	}
}

func TestLoadNoFiles(t *testing.T) {
	t.Parallel()

	// Load with a non-existent repo dir; should return defaults.
	cfg, err := config.Load("/nonexistent/repo")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	def := config.Defaults()
	if cfg.Scan.MaxFileBytes != def.Scan.MaxFileBytes {
		t.Errorf("MaxFileBytes = %d, want default %d", cfg.Scan.MaxFileBytes, def.Scan.MaxFileBytes)
	}
}

func TestLoadRepoConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pckeDir := filepath.Join(dir, ".pcke")
	if err := os.MkdirAll(pckeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	configContent := `
[scan]
max_file_bytes = 1048576
redact_secrets = false

[kdb]
wal_segment_mb = 32
`
	if err := os.WriteFile(filepath.Join(pckeDir, "config.toml"), []byte(configContent), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Scan.MaxFileBytes != 1048576 {
		t.Errorf("MaxFileBytes = %d, want 1048576", cfg.Scan.MaxFileBytes)
	}
	if cfg.Scan.RedactSecrets {
		t.Error("RedactSecrets should be false from repo config")
	}
	if cfg.KDB.WALSegmentMB != 32 {
		t.Errorf("WALSegmentMB = %d, want 32", cfg.KDB.WALSegmentMB)
	}

	// Fields not in repo config should still be defaults.
	if cfg.KDB.GracefulShutdownS != 10 {
		t.Errorf("GracefulShutdownS = %d, want default 10", cfg.KDB.GracefulShutdownS)
	}
}

// TestPrecedence is the table-driven test from T12 DoD: flag > env > repo > user > default.
func TestPrecedence(t *testing.T) {
	// Not parallel: modifies environment.

	tests := []struct {
		name       string
		repoTOML   string
		envVars    map[string]string
		wantWALMB  int
		wantSource string
	}{
		{
			name:       "default only",
			wantWALMB:  16,
			wantSource: "default",
		},
		{
			name:       "repo overrides default",
			repoTOML:   "[kdb]\nwal_segment_mb = 24\n",
			wantWALMB:  24,
			wantSource: "repo",
		},
		{
			name:       "env overrides repo",
			repoTOML:   "[kdb]\nwal_segment_mb = 24\n",
			envVars:    map[string]string{"PCKE_KDB_WAL_SEGMENT_MB": "48"},
			wantWALMB:  48,
			wantSource: "env",
		},
		{
			name:       "env overrides default",
			envVars:    map[string]string{"PCKE_KDB_WAL_SEGMENT_MB": "64"},
			wantWALMB:  64,
			wantSource: "env",
		},
		{
			name:       "invalid env keeps repo value",
			repoTOML:   "[kdb]\nwal_segment_mb = 24\n",
			envVars:    map[string]string{"PCKE_KDB_WAL_SEGMENT_MB": "notanumber"},
			wantWALMB:  24,
			wantSource: "repo",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Set up repo config.
			dir := t.TempDir()
			if tc.repoTOML != "" {
				pckeDir := filepath.Join(dir, ".pcke")
				if err := os.MkdirAll(pckeDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(pckeDir, "config.toml"), []byte(tc.repoTOML), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			// Set env vars.
			for k, v := range tc.envVars {
				t.Setenv(k, v)
			}

			cfg, err := config.Load(dir)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			if cfg.KDB.WALSegmentMB != tc.wantWALMB {
				t.Errorf("WALSegmentMB = %d, want %d (source: %s)",
					cfg.KDB.WALSegmentMB, tc.wantWALMB, tc.wantSource)
			}
		})
	}
}

func TestEnvBoolValues(t *testing.T) {
	tests := []struct {
		env  string
		want bool
	}{
		{"true", true},
		{"True", true},
		{"TRUE", true},
		{"1", true},
		{"yes", true},
		{"false", false},
		{"False", false},
		{"0", false},
		{"no", false},
	}

	for _, tc := range tests {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv("PCKE_SCAN_REDACT_SECRETS", tc.env)

			cfg, err := config.Load("")
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			if cfg.Scan.RedactSecrets != tc.want {
				t.Errorf("RedactSecrets = %v, want %v for env=%q",
					cfg.Scan.RedactSecrets, tc.want, tc.env)
			}
		})
	}
}

func TestEnvStringOverride(t *testing.T) {
	t.Setenv("PCKE_FTS_TOKENIZER_CJK_MODE", "per_codepoint")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.FTS.TokenizerCJKMode != "per_codepoint" {
		t.Errorf("TokenizerCJKMode = %q, want per_codepoint", cfg.FTS.TokenizerCJKMode)
	}
}

func TestInvalidTOML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pckeDir := filepath.Join(dir, ".pcke")
	if err := os.MkdirAll(pckeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pckeDir, "config.toml"), []byte("{{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := config.Load(dir)
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}

func TestLoadEmptyRepoDir(t *testing.T) {
	t.Parallel()

	// Empty string for repoDir: skip repo config.
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	def := config.Defaults()
	if cfg.KDB.WALSegmentMB != def.KDB.WALSegmentMB {
		t.Errorf("WALSegmentMB = %d, want default %d", cfg.KDB.WALSegmentMB, def.KDB.WALSegmentMB)
	}
}

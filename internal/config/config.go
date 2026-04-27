// Package config implements the layered configuration system for pcke.
//
// Precedence (highest to lowest):
//
//	CLI flag > env PCKE_* > .pcke/config.toml (repo) > ~/.config/pcke/config.toml (user) > defaults
//
// Phase 0 — Task T12.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config holds the merged configuration values.
type Config struct {
	Scan ScanConfig `toml:"scan"`
	KDB  KDBConfig  `toml:"kdb"`
	FTS  FTSConfig  `toml:"fts"`
	MCP  MCPConfig  `toml:"mcp"`
}

// ScanConfig holds scan-related settings.
type ScanConfig struct {
	RedactSecrets  bool     `toml:"redact_secrets"`
	IncludeIgnored bool     `toml:"include_ignored"`
	ExcludeGlobs   []string `toml:"exclude_globs"`
	MaxFileBytes   int64    `toml:"max_file_bytes"`
}

// KDBConfig holds storage engine settings.
type KDBConfig struct {
	BufferPoolMB        int `toml:"buffer_pool_mb"`
	WALSegmentMB        int `toml:"wal_segment_mb"`
	CheckpointWALMB     int `toml:"checkpoint_wal_mb"`
	CheckpointIntervalS int `toml:"checkpoint_interval_sec"`
	GracefulShutdownS   int `toml:"graceful_shutdown_sec"`
}

// FTSConfig holds full-text search settings.
type FTSConfig struct {
	TokenizerCJKMode   string `toml:"tokenizer_cjk_mode"`
	MergeTierThreshold int    `toml:"merge_tier_threshold"`
}

// MCPConfig holds MCP server settings.
type MCPConfig struct {
	ReadTimeoutS     int  `toml:"read_timeout_sec"`
	ProactiveContext bool `toml:"proactive_context"`
	StreamThreshold  int  `toml:"stream_threshold"`
	ChunkSize        int  `toml:"chunk_size"`
}

// Defaults returns a Config populated with default values.
// See PRDs/PRD_PCKE_v3_1_EXECUTION_PLAN.md §9.2.
func Defaults() Config {
	return Config{
		Scan: ScanConfig{
			RedactSecrets:  true,
			IncludeIgnored: false,
			ExcludeGlobs:   nil,
			MaxFileBytes:   2 * 1024 * 1024, // 2 MiB
		},
		KDB: KDBConfig{
			BufferPoolMB:        0, // auto
			WALSegmentMB:        16,
			CheckpointWALMB:     32,
			CheckpointIntervalS: 60,
			GracefulShutdownS:   10,
		},
		FTS: FTSConfig{
			TokenizerCJKMode:   "segmenter",
			MergeTierThreshold: 10,
		},
		MCP: MCPConfig{
			ReadTimeoutS: 30,
		},
	}
}

// Source identifies where a config value originated.
type Source string

// Config value sources, ordered by precedence (highest first).
const (
	SourceDefault Source = "default"
	SourceUser    Source = "user"
	SourceRepo    Source = "repo"
	SourceEnv     Source = "env"
	SourceFlag    Source = "flag"
)

// Load resolves the final Config by merging layers in precedence order.
// repoDir is the repo root (containing .pcke/); if empty, repo-level config is skipped.
func Load(repoDir string) (Config, error) {
	cfg := Defaults()

	// Layer 1: user-level config (~/.config/pcke/config.toml).
	userPath := userConfigPath()
	if userPath != "" {
		if err := mergeFromFile(&cfg, userPath); err != nil {
			return cfg, fmt.Errorf("config: user config: %w", err)
		}
	}

	// Layer 2: repo-level config (.pcke/config.toml).
	if repoDir != "" {
		repoPath := filepath.Join(repoDir, ".pcke", "config.toml")
		if err := mergeFromFile(&cfg, repoPath); err != nil {
			return cfg, fmt.Errorf("config: repo config: %w", err)
		}
	}

	// Layer 3: environment variables.
	mergeFromEnv(&cfg)

	return cfg, nil
}

// mergeFromFile decodes a TOML file into cfg, overwriting only fields present
// in the file. If the file doesn't exist, it's a no-op.
func mergeFromFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is constructed from known bases.
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := toml.Decode(string(data), cfg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// mergeFromEnv applies PCKE_* environment variables.
func mergeFromEnv(cfg *Config) {
	if v := os.Getenv("PCKE_SCAN_REDACT_SECRETS"); v != "" {
		cfg.Scan.RedactSecrets = envBool(v, cfg.Scan.RedactSecrets)
	}
	if v := os.Getenv("PCKE_SCAN_INCLUDE_IGNORED"); v != "" {
		cfg.Scan.IncludeIgnored = envBool(v, cfg.Scan.IncludeIgnored)
	}
	if v := os.Getenv("PCKE_SCAN_MAX_FILE_BYTES"); v != "" {
		cfg.Scan.MaxFileBytes = envInt64(v, cfg.Scan.MaxFileBytes)
	}
	if v := os.Getenv("PCKE_KDB_BUFFER_POOL_MB"); v != "" {
		cfg.KDB.BufferPoolMB = envInt(v, cfg.KDB.BufferPoolMB)
	}
	if v := os.Getenv("PCKE_KDB_WAL_SEGMENT_MB"); v != "" {
		cfg.KDB.WALSegmentMB = envInt(v, cfg.KDB.WALSegmentMB)
	}
	if v := os.Getenv("PCKE_KDB_CHECKPOINT_WAL_MB"); v != "" {
		cfg.KDB.CheckpointWALMB = envInt(v, cfg.KDB.CheckpointWALMB)
	}
	if v := os.Getenv("PCKE_KDB_CHECKPOINT_INTERVAL_SEC"); v != "" {
		cfg.KDB.CheckpointIntervalS = envInt(v, cfg.KDB.CheckpointIntervalS)
	}
	if v := os.Getenv("PCKE_KDB_GRACEFUL_SHUTDOWN_SEC"); v != "" {
		cfg.KDB.GracefulShutdownS = envInt(v, cfg.KDB.GracefulShutdownS)
	}
	if v := os.Getenv("PCKE_FTS_TOKENIZER_CJK_MODE"); v != "" {
		cfg.FTS.TokenizerCJKMode = v
	}
	if v := os.Getenv("PCKE_FTS_MERGE_TIER_THRESHOLD"); v != "" {
		cfg.FTS.MergeTierThreshold = envInt(v, cfg.FTS.MergeTierThreshold)
	}
	if v := os.Getenv("PCKE_MCP_READ_TIMEOUT_SEC"); v != "" {
		cfg.MCP.ReadTimeoutS = envInt(v, cfg.MCP.ReadTimeoutS)
	}
	if v := os.Getenv("PCKE_MCP_PROACTIVE_CONTEXT"); v != "" {
		cfg.MCP.ProactiveContext = envBool(v, cfg.MCP.ProactiveContext)
	}
	if v := os.Getenv("PCKE_MCP_STREAM_THRESHOLD"); v != "" {
		cfg.MCP.StreamThreshold = envInt(v, cfg.MCP.StreamThreshold)
	}
	if v := os.Getenv("PCKE_MCP_CHUNK_SIZE"); v != "" {
		cfg.MCP.ChunkSize = envInt(v, cfg.MCP.ChunkSize)
	}
}

func envBool(v string, fallback bool) bool {
	switch strings.ToLower(v) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return fallback
	}
}

func envInt(v string, fallback int) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envInt64(v string, fallback int64) int64 {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

// userConfigPath returns the path to the user-level config file.
func userConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "pcke", "config.toml")
}

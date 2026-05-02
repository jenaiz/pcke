package onboard

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config holds the configuration for walkthrough generation.
type Config struct {
	Walkthrough WalkthroughConfig `toml:"walkthrough"`
}

// WalkthroughConfig configures walkthrough generation.
type WalkthroughConfig struct {
	Title            string          `toml:"title"`
	HighlightModules []string        `toml:"highlight_modules"`
	SkipSections     []string        `toml:"skip_sections"`
	CustomSections   []CustomSection `toml:"custom_sections"`
}

// CustomSection defines a user-defined walkthrough section.
type CustomSection struct {
	Name     string `toml:"name"`
	Content  string `toml:"content"`
	Position string `toml:"position"`
}

// DefaultConfig returns a config with default values.
func DefaultConfig() *Config {
	return &Config{}
}

// LoadConfig reads the onboarding configuration from .pcke/onboarding.toml.
// If the file does not exist, it returns [DefaultConfig].
func LoadConfig(repoDir string) (*Config, error) {
	path := filepath.Join(repoDir, ".pcke", "onboarding.toml")
	data, err := os.ReadFile(path) //nolint:gosec // G304: path constructed from known root.
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("onboard: read config: %w", err)
	}

	cfg := DefaultConfig()
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("onboard: parse config: %w", err)
	}
	return cfg, nil
}

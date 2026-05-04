// Package federation implements multi-repo intelligence for pcke.
// It provides a read-time overlay that federates knowledge bases from
// multiple repositories without centralizing data.
//
// Deprecated: federation is frozen as of v0.9.1 (PRD v5.2, ADR-0008 §4.1).
// The package remains in the binary for backward compatibility but receives
// no new features and will not be ported to the v1.0 graph model. Removal
// after v1.0.0 is contingent on adoption signals.
package federation

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Manifest represents the federation configuration file.
type Manifest struct {
	Federation  Meta             `toml:"federation"`
	Repos       []RepoEntry      `toml:"repos"`
	Constraints ConstraintConfig `toml:"constraints"`
}

// Meta holds the federation identity.
type Meta struct {
	Name string `toml:"name"`
}

// RepoEntry describes a single federated repository.
type RepoEntry struct {
	Name string `toml:"name"`
	Path string `toml:"path"`
}

// ConstraintConfig holds org-wide constraints.
type ConstraintConfig struct {
	Rules []OrgConstraint `toml:"rules"`
}

// OrgConstraint is an org-wide rule that applies across repos.
type OrgConstraint struct {
	Scope       string `toml:"scope"`
	Severity    string `toml:"severity"`
	Description string `toml:"description"`
}

// RepoHealth reports the status of a federated repo.
type RepoHealth struct {
	Name    string
	Path    string
	Valid   bool
	Problem string
}

// manifestPath returns the path to the federation manifest file.
// Uses XDG_CONFIG_HOME if available, falls back to ~/.config/pcke/federation.toml,
// then to ~/.pcke/federation.toml.
func manifestPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "pcke", "federation.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	primary := filepath.Join(home, ".config", "pcke", "federation.toml")
	if _, err := os.Stat(primary); err == nil {
		return primary
	}
	fallback := filepath.Join(home, ".pcke", "federation.toml")
	if _, err := os.Stat(fallback); err == nil {
		return fallback
	}
	return primary
}

// ManifestPathForTest allows tests to override the manifest path.
var manifestPathOverride string

func resolveManifestPath() string {
	if manifestPathOverride != "" {
		return manifestPathOverride
	}
	return manifestPath()
}

// LoadManifest reads the federation manifest from the default location.
func LoadManifest() (*Manifest, error) {
	return LoadManifestFrom(resolveManifestPath())
}

// LoadManifestFrom reads a federation manifest from the given path.
func LoadManifestFrom(path string) (*Manifest, error) {
	if path == "" {
		return nil, fmt.Errorf("federation: cannot determine manifest path")
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is from known config locations.
	if os.IsNotExist(err) {
		return &Manifest{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("federation: read manifest: %w", err)
	}
	var m Manifest
	if _, err := toml.Decode(string(data), &m); err != nil {
		return nil, fmt.Errorf("federation: parse manifest: %w", err)
	}
	return &m, nil
}

// SaveManifest writes the manifest to the default location.
func SaveManifest(m *Manifest) error {
	return SaveManifestTo(m, resolveManifestPath())
}

// SaveManifestTo writes the manifest to the given path.
func SaveManifestTo(m *Manifest, path string) error {
	if path == "" {
		return fmt.Errorf("federation: cannot determine manifest path")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil { //nolint:gosec // G301: config dir
		return fmt.Errorf("federation: create config dir: %w", err)
	}
	f, err := os.Create(path) //nolint:gosec // G304: path is from known config locations.
	if err != nil {
		return fmt.Errorf("federation: create manifest: %w", err)
	}
	defer f.Close() //nolint:errcheck // best-effort close on write path
	enc := toml.NewEncoder(f)
	if err := enc.Encode(m); err != nil {
		return fmt.Errorf("federation: encode manifest: %w", err)
	}
	return nil
}

// AddRepo adds a repository entry to the manifest. Idempotent: if a repo
// with the same name exists, its path is updated.
func AddRepo(m *Manifest, name, path string) {
	for i := range m.Repos {
		if m.Repos[i].Name == name {
			m.Repos[i].Path = path
			return
		}
	}
	m.Repos = append(m.Repos, RepoEntry{Name: name, Path: path})
}

// RemoveRepo removes a repository entry by name. No-op if not found.
func RemoveRepo(m *Manifest, name string) {
	for i := range m.Repos {
		if m.Repos[i].Name == name {
			m.Repos = append(m.Repos[:i], m.Repos[i+1:]...)
			return
		}
	}
}

// ListRepos returns all repo entries from the manifest.
func ListRepos(m *Manifest) []RepoEntry {
	return m.Repos
}

// ValidateRepos checks each repo entry for a valid .pcke/ directory.
func ValidateRepos(m *Manifest) []RepoHealth {
	results := make([]RepoHealth, 0, len(m.Repos))
	for _, r := range m.Repos {
		h := RepoHealth{Name: r.Name, Path: r.Path}
		pckeDir := filepath.Join(r.Path, ".pcke")
		info, err := os.Stat(pckeDir)
		if err != nil {
			h.Valid = false
			h.Problem = "missing .pcke/ directory"
		} else if !info.IsDir() {
			h.Valid = false
			h.Problem = ".pcke is not a directory"
		} else {
			h.Valid = true
		}
		results = append(results, h)
	}
	return results
}

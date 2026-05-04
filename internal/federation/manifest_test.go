package federation

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadManifest_NotExist(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "federation.toml")
	m, err := LoadManifestFrom(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Repos) != 0 {
		t.Fatalf("expected empty repos, got %d", len(m.Repos))
	}
}

func TestLoadSaveManifest(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "federation.toml")

	m := &Manifest{
		Federation: Meta{Name: "test-org"},
		Repos: []RepoEntry{
			{Name: "backend", Path: "/tmp/backend"},
			{Name: "frontend", Path: "/tmp/frontend"},
		},
		Constraints: ConstraintConfig{
			Rules: []OrgConstraint{
				{Scope: "all", Severity: "must", Description: "No secrets in code"},
			},
		},
	}
	if err := SaveManifestTo(m, path); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadManifestFrom(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Federation.Name != "test-org" {
		t.Errorf("federation name: got %q, want %q", loaded.Federation.Name, "test-org")
	}
	if len(loaded.Repos) != 2 {
		t.Fatalf("repos count: got %d, want 2", len(loaded.Repos))
	}
	if loaded.Repos[0].Name != "backend" {
		t.Errorf("repo 0 name: got %q", loaded.Repos[0].Name)
	}
	if len(loaded.Constraints.Rules) != 1 {
		t.Fatalf("constraints: got %d, want 1", len(loaded.Constraints.Rules))
	}
}

func TestAddRepo_Idempotent(t *testing.T) {
	m := &Manifest{}
	AddRepo(m, "api", "/path/api")
	AddRepo(m, "web", "/path/web")
	AddRepo(m, "api", "/new/path/api")

	if len(m.Repos) != 2 {
		t.Fatalf("repos count: got %d, want 2", len(m.Repos))
	}
	if m.Repos[0].Path != "/new/path/api" {
		t.Errorf("api path: got %q, want %q", m.Repos[0].Path, "/new/path/api")
	}
}

func TestRemoveRepo(t *testing.T) {
	m := &Manifest{
		Repos: []RepoEntry{
			{Name: "a", Path: "/a"},
			{Name: "b", Path: "/b"},
			{Name: "c", Path: "/c"},
		},
	}
	RemoveRepo(m, "b")
	if len(m.Repos) != 2 {
		t.Fatalf("repos count: got %d, want 2", len(m.Repos))
	}
	for _, r := range m.Repos {
		if r.Name == "b" {
			t.Fatal("repo 'b' should have been removed")
		}
	}
	// Remove non-existent — no-op.
	RemoveRepo(m, "nonexistent")
	if len(m.Repos) != 2 {
		t.Fatalf("repos count after no-op: got %d, want 2", len(m.Repos))
	}
}

func TestValidateRepos(t *testing.T) {
	tmp := t.TempDir()

	// Valid repo.
	validRepo := filepath.Join(tmp, "valid")
	if err := os.MkdirAll(filepath.Join(validRepo, ".pcke"), 0o750); err != nil {
		t.Fatal(err)
	}
	// Invalid repo — no .pcke/.
	invalidRepo := filepath.Join(tmp, "invalid")
	if err := os.MkdirAll(invalidRepo, 0o750); err != nil {
		t.Fatal(err)
	}
	// Missing repo.
	missingRepo := filepath.Join(tmp, "missing")

	m := &Manifest{
		Repos: []RepoEntry{
			{Name: "valid", Path: validRepo},
			{Name: "invalid", Path: invalidRepo},
			{Name: "missing", Path: missingRepo},
		},
	}

	results := ValidateRepos(m)
	if len(results) != 3 {
		t.Fatalf("results: got %d, want 3", len(results))
	}
	if !results[0].Valid {
		t.Errorf("valid repo should be valid: %s", results[0].Problem)
	}
	if results[1].Valid {
		t.Errorf("invalid repo should be invalid")
	}
	if results[2].Valid {
		t.Errorf("missing repo should be invalid")
	}
}

func TestListRepos(t *testing.T) {
	m := &Manifest{
		Repos: []RepoEntry{
			{Name: "a", Path: "/a"},
			{Name: "b", Path: "/b"},
		},
	}
	repos := ListRepos(m)
	if len(repos) != 2 {
		t.Fatalf("count: got %d, want 2", len(repos))
	}
}

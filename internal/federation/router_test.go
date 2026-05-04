package federation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// setupTestRepo creates a temporary repo with a .pcke/ DB and seeds a knowledge node.
func setupTestRepo(t *testing.T, name, module string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("open db for %s: %v", name, err)
	}
	// Seed a node.
	err = db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		key := "kn:" + name + "-node1"
		val := []byte(`{"id":"` + name + `-node1","name":"` + name + `_main","module":"` + module + `","type":"function","file_path":"main.go"}`)
		wtx.Put([]byte(key), val) //nolint:errcheck
		return nil
	})
	if err != nil {
		t.Fatalf("seed %s: %v", name, err)
	}
	db.Close() //nolint:errcheck
	return dir
}

func TestQueryFederation_BasicFanOut(t *testing.T) {
	repoA := setupTestRepo(t, "repoA", "cmd/api")
	repoB := setupTestRepo(t, "repoB", "internal/core")
	repoC := setupTestRepo(t, "repoC", "pkg/shared")

	manifest := &Manifest{
		Federation: Meta{Name: "test-org"},
		Repos: []RepoEntry{
			{Name: "repoA", Path: repoA},
			{Name: "repoB", Path: repoB},
			{Name: "repoC", Path: repoC},
		},
	}

	ctx := context.Background()
	rs, err := QueryFederation(ctx, manifest, "nodes", QueryOpts{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rs.Results) < 3 {
		t.Errorf("expected at least 3 results, got %d", len(rs.Results))
	}
	if len(rs.Repos) != 3 {
		t.Errorf("expected 3 contributing repos, got %d", len(rs.Repos))
	}
	if len(rs.Errors) != 0 {
		t.Errorf("unexpected errors: %v", rs.Errors)
	}
	// Check provenance.
	for _, r := range rs.Results {
		if r.Repo == "" {
			t.Error("missing repo provenance")
		}
		if r.Row["_repo"] == nil {
			t.Error("missing _repo annotation in row")
		}
	}
}

func TestQueryFederation_PartialFailure(t *testing.T) {
	repoA := setupTestRepo(t, "repoA", "cmd/api")

	manifest := &Manifest{
		Repos: []RepoEntry{
			{Name: "repoA", Path: repoA},
			{Name: "badRepo", Path: "/nonexistent/path"},
		},
	}

	ctx := context.Background()
	rs, err := QueryFederation(ctx, manifest, "nodes", QueryOpts{})
	if err != nil {
		t.Fatalf("query should not fail entirely: %v", err)
	}
	if len(rs.Results) == 0 {
		t.Error("expected results from healthy repo")
	}
	if len(rs.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(rs.Errors))
	}
	if rs.Errors[0].Repo != "badRepo" {
		t.Errorf("error repo: got %q, want %q", rs.Errors[0].Repo, "badRepo")
	}
}

func TestQueryFederation_RepoFilter(t *testing.T) {
	repoA := setupTestRepo(t, "repoA", "cmd/api")
	repoB := setupTestRepo(t, "repoB", "internal/core")

	manifest := &Manifest{
		Repos: []RepoEntry{
			{Name: "repoA", Path: repoA},
			{Name: "repoB", Path: repoB},
		},
	}

	ctx := context.Background()
	rs, err := QueryFederation(ctx, manifest, "nodes", QueryOpts{
		RepoFilter: []string{"repoA"},
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rs.Repos) != 1 || rs.Repos[0] != "repoA" {
		t.Errorf("expected only repoA, got repos: %v", rs.Repos)
	}
}

func TestQueryFederation_Limit(t *testing.T) {
	repoA := setupTestRepo(t, "repoA", "cmd/api")
	repoB := setupTestRepo(t, "repoB", "internal/core")

	manifest := &Manifest{
		Repos: []RepoEntry{
			{Name: "repoA", Path: repoA},
			{Name: "repoB", Path: repoB},
		},
	}

	ctx := context.Background()
	rs, err := QueryFederation(ctx, manifest, "nodes", QueryOpts{Limit: 1})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rs.Results) != 1 {
		t.Errorf("expected 1 result (limit), got %d", len(rs.Results))
	}
}

func TestQueryFederation_Timeout(t *testing.T) {
	manifest := &Manifest{
		Repos: []RepoEntry{
			{Name: "bad", Path: "/nonexistent"},
		},
	}

	ctx := context.Background()
	rs, err := QueryFederation(ctx, manifest, "nodes", QueryOpts{
		Timeout: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("should not fail entirely: %v", err)
	}
	if len(rs.Errors) == 0 {
		t.Error("expected error for bad repo")
	}
}

func TestQueryFederation_EmptyManifest(t *testing.T) {
	ctx := context.Background()
	rs, err := QueryFederation(ctx, &Manifest{}, "nodes", QueryOpts{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rs.Results) != 0 {
		t.Errorf("expected 0 results, got %d", len(rs.Results))
	}
}

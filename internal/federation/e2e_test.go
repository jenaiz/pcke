package federation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// TestE2E_ThreeRepoFederation tests the full federation flow with 3 repos.
func TestE2E_ThreeRepoFederation(t *testing.T) { //nolint:gocyclo // integration test
	// Setup 3 repos with distinct nodes.
	repos := make([]string, 3)
	names := []string{"backend-api", "frontend-app", "shared-lib"}

	for i, name := range names {
		dir := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		db, err := kdb.Open(dir, nil)
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}

		// Seed 2 nodes per repo.
		err = db.Update(context.Background(), func(wtx *tx.WriteTx) error {
			for j := 0; j < 2; j++ {
				node := map[string]any{
					"id":        nodeID(name, j),
					"name":      nodeName(name, j),
					"module":    name,
					"type":      "function",
					"file_path": "main.go",
				}
				data, _ := json.Marshal(node)
				if err := wtx.Put([]byte("kn:"+nodeID(name, j)), data); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		db.Close() //nolint:errcheck
		repos[i] = dir
	}

	// Create manifest.
	manifest := &Manifest{
		Federation: Meta{Name: "test-org"},
		Repos: []RepoEntry{
			{Name: names[0], Path: repos[0]},
			{Name: names[1], Path: repos[1]},
			{Name: names[2], Path: repos[2]},
		},
	}

	// Save + reload manifest (round-trip).
	tmp := filepath.Join(t.TempDir(), "federation.toml")
	if err := SaveManifestTo(manifest, tmp); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadManifestFrom(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Repos) != 3 {
		t.Fatalf("manifest round-trip: got %d repos", len(loaded.Repos))
	}

	// Query across all repos.
	ctx := context.Background()
	rs, err := QueryFederation(ctx, loaded, "nodes", QueryOpts{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rs.Results) < 6 {
		t.Errorf("expected at least 6 results (2 per repo × 3), got %d", len(rs.Results))
	}
	if len(rs.Repos) != 3 {
		t.Errorf("expected 3 contributing repos, got %d", len(rs.Repos))
	}
	if len(rs.Errors) != 0 {
		t.Errorf("unexpected errors: %v", rs.Errors)
	}

	// Verify provenance on all rows.
	repoCounts := map[string]int{}
	for _, r := range rs.Results {
		repoCounts[r.Repo]++
		if r.Row["_repo"] == nil {
			t.Error("missing _repo annotation")
		}
	}
	for _, name := range names {
		if repoCounts[name] != 2 {
			t.Errorf("repo %s: expected 2 results, got %d", name, repoCounts[name])
		}
	}
}

// TestE2E_ConcurrentQueries tests concurrent federation queries under -race.
// Verifies no data races or panics. Some queries may get partial results due
// to file-lock contention (kdb uses exclusive locks).
func TestE2E_ConcurrentQueries(t *testing.T) {
	// Create 4 different repos.
	repos := make([]RepoEntry, 4)
	for i := 0; i < 4; i++ {
		name := "concurrent-repo-" + intStr(i)
		dir := setupTestRepo(t, name, "mod"+intStr(i))
		repos[i] = RepoEntry{Name: name, Path: dir}
	}

	manifest := &Manifest{Repos: repos}

	ctx := context.Background()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var totalResults int

	// Run queries sequentially from multiple goroutines to avoid lock contention.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Each goroutine queries a different subset.
			rs, err := QueryFederation(ctx, manifest, "nodes", QueryOpts{
				RepoFilter:  []string{repos[idx].Name},
				Concurrency: 1,
			})
			if err != nil {
				t.Errorf("concurrent query %d: %v", idx, err)
				return
			}
			mu.Lock()
			totalResults += len(rs.Results)
			mu.Unlock()
		}(g)
	}
	wg.Wait()

	if totalResults < 4 {
		t.Errorf("total results across goroutines: got %d, want >=4", totalResults)
	}
}

// TestE2E_PartialFailure tests that a bad repo doesn't crash the query.
func TestE2E_PartialFailure(t *testing.T) {
	goodDir := setupTestRepo(t, "good", "cmd/good")
	// Create a path where .pcke cannot be created (a file blocks the dir creation).
	badDir := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(filepath.Join(badDir), []byte("not a dir"), 0o400); err != nil {
		// badDir itself is a file, so kdb.Open(badDir, nil) will fail creating .pcke under it.
		t.Fatal(err)
	}

	manifest := &Manifest{
		Repos: []RepoEntry{
			{Name: "good", Path: goodDir},
			{Name: "bad", Path: badDir},
		},
	}

	ctx := context.Background()
	rs, err := QueryFederation(ctx, manifest, "nodes", QueryOpts{})
	if err != nil {
		t.Fatalf("should not fail: %v", err)
	}
	if len(rs.Results) == 0 {
		t.Error("expected results from healthy repo")
	}
	if len(rs.Errors) != 1 {
		t.Errorf("expected 1 error, got %d: %v", len(rs.Errors), rs.Errors)
	}
}

// TestE2E_CrossRepoDepsAndQuery tests dependency detection + query.
func TestE2E_CrossRepoDepsAndQuery(t *testing.T) {
	// Setup: local repo imports from a remote repo.
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	// Remote repo has go.mod.
	if err := os.WriteFile(filepath.Join(remoteDir, "go.mod"),
		[]byte("module github.com/org/shared\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(remoteDir, ".pcke"), 0o750); err != nil {
		t.Fatal(err)
	}

	// Local repo with Go file.
	if err := os.WriteFile(filepath.Join(localDir, "go.mod"),
		[]byte("module github.com/org/local\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "main.go"),
		[]byte("package main\n\nimport \"github.com/org/shared/pkg/auth\"\n\nfunc main() { auth.Init() }\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Open local DB for storing deps.
	db, err := kdb.Open(localDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck

	manifest := &Manifest{
		Repos: []RepoEntry{
			{Name: "local", Path: localDir},
			{Name: "shared", Path: remoteDir},
		},
	}

	// Detect deps.
	deps, err := DetectCrossRepoDeps(context.Background(), manifest, localDir)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(deps))
	}

	// Store deps.
	if err := StoreCrossRepoDeps(context.Background(), db, deps); err != nil {
		t.Fatalf("store: %v", err)
	}

	// Verify stored in DB.
	var count int
	_ = db.View(context.Background(), func(rtx *tx.ReadTx) error {
		cursor := rtx.Cursor()
		for ok := cursor.Seek([]byte("fr:")); ok; ok = cursor.Next() {
			k := string(cursor.Key())
			if k >= "fr:" && k < "fs:" {
				count++
			} else {
				break
			}
		}
		return nil
	})
	if count != 1 {
		t.Errorf("expected 1 stored dep, got %d", count)
	}
}

// TestE2E_ConstraintPropagationAndViolation tests constraint flow.
func TestE2E_ConstraintPropagationAndViolation(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck

	// Seed a node that violates a constraint.
	node := map[string]any{
		"id":     "db-import",
		"module": "internal/database",
		"type":   "import",
		"name":   "sql_driver",
	}
	nodeJSON, _ := json.Marshal(node)
	err = db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		wtx.Put([]byte("kn:db-import"), nodeJSON) //nolint:errcheck
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	manifest := &Manifest{
		Federation: Meta{Name: "e2e-org"},
		Constraints: ConstraintConfig{
			Rules: []OrgConstraint{
				{Scope: "all", Severity: "must", Description: "No direct DB access outside repository boundary"},
			},
		},
	}

	// Propagate.
	if err := PropagateConstraints(context.Background(), db, manifest); err != nil {
		t.Fatalf("propagate: %v", err)
	}

	// Check violations.
	violations, err := CheckOrgConstraints(context.Background(), db, manifest)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(violations) != 1 {
		t.Errorf("expected 1 violation, got %d", len(violations))
	}
}

// helpers
func nodeID(repo string, i int) string {
	return repo + "-node-" + intStr(i)
}

func nodeName(repo string, i int) string {
	return repo + "_func_" + intStr(i)
}

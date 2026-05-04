package federation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
	"github.com/jenaiz/pcke/internal/query"
)

// setupBenchRepo creates a temporary repo with n knowledge nodes.
func setupBenchRepo(b *testing.B, name string, n int) string {
	b.Helper()
	dir := filepath.Join(b.TempDir(), name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		b.Fatal(err)
	}
	db, err := kdb.Open(dir, nil)
	if err != nil {
		b.Fatal(err)
	}
	err = db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		for i := 0; i < n; i++ {
			node := map[string]any{
				"id":        name + "-" + intStr(i),
				"name":      "func_" + intStr(i),
				"module":    name + "/pkg",
				"type":      "function",
				"file_path": "pkg/file_" + intStr(i) + ".go",
			}
			data, _ := json.Marshal(node)
			wtx.Put([]byte("kn:"+name+"-"+intStr(i)), data) //nolint:errcheck
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	db.Close() //nolint:errcheck
	return dir
}

func BenchmarkFederationQuery3Repos(b *testing.B) {
	repos := make([]RepoEntry, 3)
	for i := 0; i < 3; i++ {
		name := "repo" + intStr(i)
		repos[i] = RepoEntry{Name: name, Path: setupBenchRepo(b, name, 1000)}
	}
	manifest := &Manifest{Repos: repos}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := QueryFederation(ctx, manifest, "nodes", QueryOpts{})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFederationQuery10Repos(b *testing.B) {
	repos := make([]RepoEntry, 10)
	for i := 0; i < 10; i++ {
		name := "repo" + intStr(i)
		repos[i] = RepoEntry{Name: name, Path: setupBenchRepo(b, name, 1000)}
	}
	manifest := &Manifest{Repos: repos}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := QueryFederation(ctx, manifest, "nodes", QueryOpts{})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCrossRepoDeps(b *testing.B) {
	// Setup 5 repos with go.mod files.
	localDir := b.TempDir()
	if err := os.WriteFile(filepath.Join(localDir, "go.mod"),
		[]byte("module github.com/org/local\n\ngo 1.21\n"), 0o600); err != nil {
		b.Fatal(err)
	}
	// Create multiple Go files with imports.
	for i := 0; i < 20; i++ {
		src := "package main\n\nimport \"github.com/org/dep" + intStr(i%5) + "/pkg\"\n\nvar _ = pkg.X\n"
		if err := os.WriteFile(filepath.Join(localDir, "file_"+intStr(i)+".go"), []byte(src), 0o600); err != nil {
			b.Fatal(err)
		}
	}

	repos := []RepoEntry{{Name: "local", Path: localDir}}
	for i := 0; i < 5; i++ {
		dir := b.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"),
			[]byte("module github.com/org/dep"+intStr(i)+"\n\ngo 1.21\n"), 0o600); err != nil {
			b.Fatal(err)
		}
		repos = append(repos, RepoEntry{Name: "dep" + intStr(i), Path: dir})
	}
	manifest := &Manifest{Repos: repos}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := DetectCrossRepoDeps(ctx, manifest, localDir)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFederationResultMerge(b *testing.B) {
	// Simulate merging results from 10 repos × 10K rows.
	results := make([]repoResult, 10)
	for i := 0; i < 10; i++ {
		rows := make([]map[string]any, 10000)
		for j := 0; j < 10000; j++ {
			rows[j] = map[string]any{
				"id":   "n-" + intStr(i) + "-" + intStr(j),
				"name": "func_" + intStr(j),
			}
		}
		results[i] = repoResult{repo: "repo" + intStr(i), rows: toQueryRows(rows)}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mergeResults(results, 0)
	}
}

func toQueryRows(m []map[string]any) []query.Row {
	rows := make([]query.Row, len(m))
	for i, v := range m {
		rows[i] = query.Row(v)
	}
	return rows
}

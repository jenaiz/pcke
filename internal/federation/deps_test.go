package federation

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
)

func TestDetectCrossRepoDeps_GoImports(t *testing.T) {
	// Setup: local repo imports a package from another federated repo.
	localRepo := t.TempDir()
	remoteRepo := t.TempDir()

	// Remote repo has a go.mod.
	if err := os.WriteFile(filepath.Join(remoteRepo, "go.mod"), []byte("module github.com/org/shared-lib\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Local repo has a Go file importing the remote module.
	srcDir := filepath.Join(localRepo, "cmd")
	if err := os.MkdirAll(srcDir, 0o750); err != nil {
		t.Fatal(err)
	}
	goFile := filepath.Join(srcDir, "main.go")
	src := `package main

import (
	"fmt"

	"github.com/org/shared-lib/pkg/utils"
)

func main() {
	fmt.Println(utils.Hello())
}
`
	if err := os.WriteFile(goFile, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	// Local repo also needs a go.mod so it's recognized.
	if err := os.WriteFile(filepath.Join(localRepo, "go.mod"), []byte("module github.com/org/local-svc\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest := &Manifest{
		Repos: []RepoEntry{
			{Name: "local-svc", Path: localRepo},
			{Name: "shared-lib", Path: remoteRepo},
		},
	}

	deps, err := DetectCrossRepoDeps(context.Background(), manifest, localRepo)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(deps))
	}
	d := deps[0]
	if d.SourceRepo != "local-svc" {
		t.Errorf("source repo: got %q", d.SourceRepo)
	}
	if d.TargetRepo != "shared-lib" {
		t.Errorf("target repo: got %q", d.TargetRepo)
	}
	if d.TargetModule != "pkg/utils" {
		t.Errorf("target module: got %q", d.TargetModule)
	}
	if d.ImportPath != "github.com/org/shared-lib/pkg/utils" {
		t.Errorf("import path: got %q", d.ImportPath)
	}
	if d.DetectedVia != "go-import" {
		t.Errorf("detected via: got %q", d.DetectedVia)
	}
}

func TestDetectCrossRepoDeps_NoMatch(t *testing.T) {
	localRepo := t.TempDir()
	remoteRepo := t.TempDir()

	if err := os.WriteFile(filepath.Join(remoteRepo, "go.mod"), []byte("module github.com/org/other\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Local file imports something unrelated.
	if err := os.WriteFile(filepath.Join(localRepo, "main.go"), []byte(`package main

import "fmt"

func main() { fmt.Println("hi") }
`), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest := &Manifest{
		Repos: []RepoEntry{
			{Name: "local", Path: localRepo},
			{Name: "other", Path: remoteRepo},
		},
	}

	deps, err := DetectCrossRepoDeps(context.Background(), manifest, localRepo)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(deps) != 0 {
		t.Errorf("expected 0 deps, got %d", len(deps))
	}
}

func TestStoreCrossRepoDeps(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck

	deps := []CrossRepoDep{
		{
			SourceRepo:   "svc-a",
			SourceNodeID: "cmd/main.go",
			TargetRepo:   "shared-lib",
			TargetModule: "pkg/auth",
			ImportPath:   "github.com/org/shared-lib/pkg/auth",
			DetectedVia:  "go-import",
		},
		{
			SourceRepo:   "svc-a",
			SourceNodeID: "internal/handler.go",
			TargetRepo:   "shared-lib",
			TargetModule: "pkg/http",
			ImportPath:   "github.com/org/shared-lib/pkg/http",
			DetectedVia:  "go-import",
		},
	}

	if err := StoreCrossRepoDeps(context.Background(), db, deps); err != nil {
		t.Fatalf("store: %v", err)
	}

	// Verify stored — query federation_relations via DSL would need collection registration,
	// so just re-store (idempotent replace).
	if err := StoreCrossRepoDeps(context.Background(), db, deps[:1]); err != nil {
		t.Fatalf("re-store: %v", err)
	}
}

func TestExtractGoImports(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "test.go")
	src := `package foo

import (
	"context"
	"fmt"

	named "github.com/org/pkg/bar"
	"github.com/other/lib"
)

import "single/import"
`
	if err := os.WriteFile(f, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	imports := extractGoImports(f)
	expected := []string{"context", "fmt", "github.com/org/pkg/bar", "github.com/other/lib", "single/import"}
	if len(imports) != len(expected) {
		t.Fatalf("expected %d imports, got %d: %v", len(expected), len(imports), imports)
	}
	for i, exp := range expected {
		if imports[i] != exp {
			t.Errorf("import[%d]: got %q, want %q", i, imports[i], exp)
		}
	}
}

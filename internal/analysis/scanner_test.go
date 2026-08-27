package analysis

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jenaiz/pcke/internal/config"
	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

func TestScannerFullScan(t *testing.T) {
	root := findRepoRoot(t)

	// Open a temp kdb database.
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	cfg := config.Defaults().Scan
	scanner, err := NewScanner(root, db, cfg)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}

	ctx := context.Background()
	result, err := scanner.Scan(ctx, true)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if result.NodesCreated == 0 {
		t.Error("expected at least some nodes created")
	}
	if result.CommitHash == "" {
		t.Error("expected commit hash")
	}
	t.Logf("scan: created=%d updated=%d deleted=%d scanned=%d skipped=%d secrets=%d duration=%s",
		result.NodesCreated, result.NodesUpdated, result.NodesDeleted,
		result.FilesScanned, result.FilesSkipped, result.SecretsFound, result.Duration)

	// Verify last scan commit was persisted.
	lastCommit := scanner.LastScanCommit(ctx)
	if lastCommit == "" {
		t.Error("expected last scan commit to be persisted")
	}
	if lastCommit != result.CommitHash {
		t.Errorf("last commit = %q, want %q", lastCommit, result.CommitHash)
	}
}

// TestScannerIncrementalTracksChanges verifies that a second scan sees the
// nodes written by the first (loadExistingNodes cursor scan), so unchanged
// files are skipped, edits count as updates, and removals are marked
// deleted — the incremental machinery is dead if existing nodes don't load.
func TestScannerIncrementalTracksChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeRepoFile(t, dir, "a.go", "package main\n")
	writeRepoFile(t, dir, "b.go", "package main\n")
	gitAdd(t, dir, "a.go")
	gitAdd(t, dir, "b.go")

	dbDir := t.TempDir()
	db, err := kdb.Open(dbDir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	cfg := config.Defaults().Scan
	scanner, err := NewScanner(dir, db, cfg)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	ctx := context.Background()

	// First incremental scan: a.go and b.go are new.
	r1, err := scanner.Scan(ctx, false)
	if err != nil {
		t.Fatalf("Scan #1: %v", err)
	}
	if r1.NodesCreated < 2 {
		t.Fatalf("first scan NodesCreated = %d, want >= 2", r1.NodesCreated)
	}

	// Second scan, nothing changed: existing nodes must load, so no file
	// is re-created or updated.
	r2, err := scanner.Scan(ctx, false)
	if err != nil {
		t.Fatalf("Scan #2: %v", err)
	}
	if r2.NodesCreated != 0 || r2.NodesUpdated != 0 {
		t.Errorf("re-scan created=%d updated=%d, want 0/0 (loadExistingNodes must see prior nodes)",
			r2.NodesCreated, r2.NodesUpdated)
	}

	// Edit a.go: it must count as an update, not a create.
	writeRepoFile(t, dir, "a.go", "package main\n\nvar X = 1\n")
	gitAdd(t, dir, "a.go")
	r3, err := scanner.Scan(ctx, false)
	if err != nil {
		t.Fatalf("Scan #3: %v", err)
	}
	if r3.NodesUpdated < 1 {
		t.Errorf("after edit NodesUpdated = %d, want >= 1", r3.NodesUpdated)
	}
	if r3.NodesCreated != 0 {
		t.Errorf("after edit NodesCreated = %d, want 0", r3.NodesCreated)
	}

	// Remove b.go: it must be marked deleted.
	run(t, dir, "git", "rm", "-q", "b.go")
	run(t, dir, "git", "commit", "-m", "rm b.go")
	r4, err := scanner.Scan(ctx, false)
	if err != nil {
		t.Fatalf("Scan #4: %v", err)
	}
	if r4.NodesDeleted < 1 {
		t.Errorf("after removal NodesDeleted = %d, want >= 1", r4.NodesDeleted)
	}
}

// writeRepoFile writes filename (relative to dir) with content.
func writeRepoFile(t *testing.T, dir, filename, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
}

func TestScannerSecretFilesExcluded(t *testing.T) {
	root := findRepoRoot(t)

	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	cfg := config.Defaults().Scan
	cfg.RedactSecrets = true

	scanner, err := NewScanner(root, db, cfg)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}

	files, err := scanner.collectFiles()
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}

	// Verify no secret files were collected.
	for _, f := range files {
		if IsSecretPath(f) {
			t.Errorf("secret file collected: %s", f)
		}
	}
	t.Logf("collected %d files, no secrets", len(files))
}

// TestScannerDeepScan verifies that --deep extracts AST entities from Go files.
func TestScannerDeepScan(t *testing.T) {
	// Create a minimal repo with a known Go file.
	dir := t.TempDir()
	initGitRepo(t, dir)

	goFile := filepath.Join(dir, "main.go")
	goSrc := `package main

import "fmt"

// Hello greets the world.
func Hello() {
	fmt.Println("hello")
}

type Server struct {
	addr string
}

func (s *Server) Start() error {
	return nil
}
`
	if err := os.WriteFile(goFile, []byte(goSrc), 0o600); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	gitAdd(t, dir, "main.go")

	dbDir := t.TempDir()
	db, err := kdb.Open(dbDir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	cfg := config.Defaults().Scan
	scanner, err := NewScanner(dir, db, cfg, WithDeep())
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}

	ctx := context.Background()
	result, err := scanner.Scan(ctx, true)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	t.Logf("deep scan: created=%d entities=%d relations=%d",
		result.NodesCreated, result.EntitiesExtracted, result.RelationsCreated)

	if result.EntitiesExtracted == 0 {
		t.Error("expected entities to be extracted in deep mode")
	}
	// main.go has: Hello (function), Server (struct), Start (method) = 3 entities
	if result.EntitiesExtracted < 3 {
		t.Errorf("got %d entities, want >= 3 (Hello, Server, Start)", result.EntitiesExtracted)
	}
	// main.go imports "fmt" → 1 relation
	if result.RelationsCreated < 1 {
		t.Errorf("got %d relations, want >= 1 (imports fmt)", result.RelationsCreated)
	}
}

// TestRelationsPopulator verifies that deep scan creates import relations
// and that they are persisted in the kdb store.
func TestRelationsPopulator(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	writeFile(t, dir, "main.go", `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	fmt.Println(strings.Join(os.Args, " "))
}
`)
	writeFile(t, dir, "util.go", `package main

import "encoding/json"

func encode(v any) ([]byte, error) {
	return json.Marshal(v)
}
`)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "add files")

	dbDir := t.TempDir()
	db, err := kdb.Open(dbDir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	cfg := config.Defaults().Scan
	scanner, err := NewScanner(dir, db, cfg, WithDeep())
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}

	result, err := scanner.Scan(context.Background(), true)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	// main.go imports fmt, os, strings (3); util.go imports encoding/json (1) → 4 total
	if result.RelationsCreated != 4 {
		t.Errorf("got %d relations, want 4", result.RelationsCreated)
	}

	// Verify relations are persisted in kdb.
	relCount := countRelations(t, db)
	if relCount != 4 {
		t.Errorf("kdb has %d relation keys, want 4", relCount)
	}

	// Gap B: the scan must populate the typed-event graph natively, so
	// graph/context work without a manual `pcke migrate`. Expect one e:
	// entity per scanned file (README.md, main.go, util.go) and one l:
	// link per import (4).
	if got := countKeysWithPrefix(t, db, "e:"); got != 3 {
		t.Errorf("event log has %d e: entity keys, want 3", got)
	}
	if got := countKeysWithPrefix(t, db, "l:"); got != 4 {
		t.Errorf("event log has %d l: link keys, want 4", got)
	}

	t.Logf("relations: created=%d persisted=%d", result.RelationsCreated, relCount)
}

// TestEntityVersioning verifies that re-scanning a repository appends a
// new e: entity version only for files whose content changed, building a
// supersedes chain, while unchanged files stay at a single version.
func TestEntityVersioning(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	writeFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, dir, "util.go", "package main\n\nfunc helper() int { return 1 }\n")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "initial")

	dbDir := t.TempDir()
	db, err := kdb.Open(dbDir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	cfg := config.Defaults().Scan

	// Each scan uses a fresh Scanner, mirroring production where every
	// `pcke scan` invocation builds a new Scanner (the AST parser is
	// single-use and closed at the end of Scan).
	scan := func(full bool) {
		t.Helper()
		scanner, err := NewScanner(dir, db, cfg, WithDeep())
		if err != nil {
			t.Fatalf("NewScanner: %v", err)
		}
		if _, err := scanner.Scan(context.Background(), full); err != nil {
			t.Fatalf("Scan(full=%v): %v", full, err)
		}
	}

	// First scan: every file gets v1.
	scan(true)
	if got := entityVersionCount(t, db, "main.go"); got != 1 {
		t.Fatalf("after first scan main.go has %d versions, want 1", got)
	}

	// Re-scan with no changes: no new versions (idempotent on content).
	scan(true)
	if got := entityVersionCount(t, db, "main.go"); got != 1 {
		t.Fatalf("after no-op scan main.go has %d versions, want 1", got)
	}

	// Change main.go only, then re-scan: main.go gains v2, util.go stays v1.
	writeFile(t, dir, "main.go", "package main\n\nfunc main() { println(\"changed\") }\n")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "edit main")

	scan(false)
	if got := entityVersionCount(t, db, "main.go"); got != 2 {
		t.Errorf("after edit main.go has %d versions, want 2", got)
	}
	if got := entityVersionCount(t, db, "util.go"); got != 1 {
		t.Errorf("unchanged util.go has %d versions, want 1", got)
	}

	// The newest main.go version must point back via supersedes.
	store := event.New(db)
	latest, err := store.Latest(context.Background(), event.KindEntity, "main.go")
	if err != nil {
		t.Fatalf("Latest main.go: %v", err)
	}
	if len(latest.Header().Supersedes) == 0 {
		t.Errorf("latest main.go version missing supersedes pointer")
	}
}

// entityVersionCount returns the number of e: entity versions stored for id.
func entityVersionCount(t *testing.T, db *kdb.DB, id string) int {
	t.Helper()
	store := event.New(db)
	var n int
	err := store.History(context.Background(), event.KindEntity, id, func(event.Event) error {
		n++
		return nil
	})
	if err != nil {
		t.Fatalf("History %q: %v", id, err)
	}
	return n
}

// countKeysWithPrefix counts keys whose byte prefix exactly matches p.
func countKeysWithPrefix(t *testing.T, db *kdb.DB, p string) int {
	t.Helper()
	var count int
	err := db.View(context.Background(), func(rtx *tx.ReadTx) error {
		c := rtx.Cursor()
		if !c.First() {
			return nil
		}
		for c.Valid() {
			if strings.HasPrefix(string(c.Key()), p) {
				count++
			}
			if !c.Next() {
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("count prefix %q: %v", p, err)
	}
	return count
}

func countRelations(t *testing.T, db *kdb.DB) int {
	t.Helper()
	var count int
	err := db.View(context.Background(), func(rtx *tx.ReadTx) error {
		c := rtx.Cursor()
		if !c.First() {
			return nil
		}
		for c.Valid() {
			if strings.HasPrefix(string(c.Key()), "rel:") {
				count++
			}
			if !c.Next() {
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("count relations: %v", err)
	}
	return count
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestScannerShallowNoEntities verifies that without --deep, no entities are extracted.
func TestScannerShallowNoEntities(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitAdd(t, dir, "main.go")

	dbDir := t.TempDir()
	db, err := kdb.Open(dbDir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	cfg := config.Defaults().Scan
	scanner, err := NewScanner(dir, db, cfg) // no WithDeep
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}

	result, err := scanner.Scan(context.Background(), true)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if result.EntitiesExtracted != 0 {
		t.Errorf("shallow scan got %d entities, want 0", result.EntitiesExtracted)
	}
}

// initGitRepo creates a bare-minimum git repo at dir with an initial commit.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test")
	// Need at least one commit for HEAD to exist.
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# test\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "init")
}

// gitAdd stages and commits a file.
func gitAdd(t *testing.T, dir, file string) {
	t.Helper()
	run(t, dir, "git", "add", file)
	run(t, dir, "git", "commit", "-m", "add "+file)
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...) //nolint:gosec // test helper
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

package analysis

import (
	"context"
	"testing"

	"github.com/jenaiz/pcke/internal/config"
	"github.com/jenaiz/pcke/internal/kdb"
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

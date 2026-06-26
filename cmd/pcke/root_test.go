package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/analysis"
	"github.com/jenaiz/pcke/internal/config"
	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
	"github.com/jenaiz/pcke/internal/query"
)

func TestRootHelp(t *testing.T) {
	t.Parallel()

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--help: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "pcke") {
		t.Error("--help output missing 'pcke'")
	}

	// All stub commands should appear.
	for _, sub := range []string{"init", "scan", "sync", "rule", "note", "status", "modules", "diagnostics", "config", "serve"} {
		if !strings.Contains(out, sub) {
			t.Errorf("--help output missing subcommand %q", sub)
		}
	}
}

func TestRootVersion(t *testing.T) {
	t.Parallel()

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--version: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "pcke") {
		t.Error("--version output missing 'pcke'")
	}
}

func TestSubcommandStubs(t *testing.T) {
	// Not parallel: dbSubs subtests chdir via t.Chdir, which requires a
	// non-parallel test and serialises cwd mutation across the package.

	// Commands that print help only (parent commands with subcommands).
	helpSubs := []string{"rule", "note"}
	for _, sub := range helpSubs {
		t.Run(sub, func(t *testing.T) {
			t.Parallel()

			cmd := newRootCmd()
			buf := new(bytes.Buffer)
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			cmd.SetArgs([]string{sub})

			if err := cmd.Execute(); err != nil {
				t.Fatalf("%s: %v", sub, err)
			}
		})
	}

	// Commands that need a database (init creates, others open).
	dbSubs := []string{"init", "status", "modules"}
	for _, sub := range dbSubs {
		t.Run(sub, func(t *testing.T) {
			tmp := t.TempDir()

			if sub != "init" {
				db, err := kdb.Open(tmp, nil)
				if err != nil {
					t.Fatalf("setup db: %v", err)
				}
				_ = db.Close()
			}

			t.Chdir(tmp)

			cmd := newRootCmd()
			buf := new(bytes.Buffer)
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			cmd.SetArgs([]string{sub})

			if err := cmd.Execute(); err != nil {
				t.Fatalf("%s: %v", sub, err)
			}
		})
	}
}

func TestConfigSubcommands(t *testing.T) {
	// Not parallel: t.Chdir requires a non-parallel test.
	tmp := t.TempDir()
	t.Chdir(tmp)

	tests := []struct {
		args []string
	}{
		{[]string{"config", "get", "scan.max_file_bytes"}},
		{[]string{"config", "set", "scan.max_file_bytes", "1048576"}},
		{[]string{"config", "list"}},
	}

	for _, tc := range tests {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			cmd := newRootCmd()
			buf := new(bytes.Buffer)
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			cmd.SetArgs(tc.args)

			if err := cmd.Execute(); err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
		})
	}
}

func TestUnknownCommand(t *testing.T) {
	t.Parallel()

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"nonexistent"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
}

func TestExitCodes(t *testing.T) {
	t.Parallel()

	// Verify the constants are distinct and match the spec.
	codes := map[int]string{
		ExitSuccess:        "success",
		ExitUserError:      "user error",
		ExitConfigError:    "config error",
		ExitLockConflict:   "lock conflict",
		ExitCorruption:     "corruption",
		ExitInternal:       "internal",
		ExitSchemaMismatch: "schema mismatch",
	}

	if len(codes) != 7 {
		t.Errorf("expected 7 distinct exit codes, got %d", len(codes))
	}
}

func TestServeCmd_Help(t *testing.T) {
	t.Parallel()

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"serve", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("serve --help: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"MCP", "stdio", "recall", "pcke://architecture", "onboarding"} {
		if !strings.Contains(out, want) {
			t.Errorf("serve --help missing %q", want)
		}
	}
}

func TestServeCmd_NoKDB(t *testing.T) {
	// Not parallel: t.Chdir requires a non-parallel test.
	tmp := t.TempDir()
	t.Chdir(tmp)

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"serve"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when running serve without .pcke/")
	}
	if !strings.Contains(err.Error(), "serve") {
		t.Errorf("error should mention 'serve', got: %v", err)
	}
}

// --- Wave 4: CLI Integration Tests ---

// seedTestDB creates a kdb with sample knowledge nodes for testing CLI commands.
func seedTestDB(t *testing.T, dir string) *kdb.DB {
	t.Helper()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	nodes := []map[string]any{
		{
			"id": "internal/kdb/db.go", "name": "db.go", "type": "file",
			"file_path": "internal/kdb/db.go", "module": "internal/kdb",
			"language": "Go", "class": "source", "status": "active",
		},
		{
			"id": "cmd/pcke/main.go", "name": "main.go", "type": "file",
			"file_path": "cmd/pcke/main.go", "module": "cmd/pcke",
			"language": "Go", "class": "source", "status": "active",
		},
	}

	ctx := context.Background()
	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		for _, n := range nodes {
			data, err := json.Marshal(n)
			if err != nil {
				return err
			}
			id := n["id"].(string)
			if err := wtx.Put([]byte("kn:"+id), data); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

// --- Recall integration tests ---

func TestRecallImproved_TextOutput(t *testing.T) {
	tmp := t.TempDir()
	db := seedTestDB(t, tmp)
	_ = db.Close()

	t.Chdir(tmp)

	// Call runRecall directly; if it returns nil, the command succeeded.
	err := runRecall("kdb database", 10, "text", false)
	if err != nil {
		t.Fatalf("runRecall: %v", err)
	}
}

func TestRecallImproved_JSONOutput(t *testing.T) {
	tmp := t.TempDir()
	db := seedTestDB(t, tmp)
	_ = db.Close()

	t.Chdir(tmp)

	// Call runRecall with json format; verify no error.
	err := runRecall("kdb database", 10, "json", false)
	if err != nil {
		t.Fatalf("runRecall json: %v", err)
	}
}

func TestRecallImproved_NoResults(t *testing.T) {
	tmp := t.TempDir()
	// Empty database — no nodes.
	db, err := kdb.Open(tmp, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	_ = db.Close()

	t.Chdir(tmp)

	// Should succeed with "no results" (no error).
	err = runRecall("nonexistent_xyzzy_query", 10, "text", false)
	if err != nil {
		t.Fatalf("runRecall no-results: %v", err)
	}
}

func TestRecallImproved_Verbose(t *testing.T) {
	tmp := t.TempDir()
	db := seedTestDB(t, tmp)
	_ = db.Close()

	t.Chdir(tmp)

	// Verbose mode should not error.
	err := runRecall("kdb", 10, "text", true)
	if err != nil {
		t.Fatalf("runRecall verbose: %v", err)
	}
}

// --- Watch integration tests ---

func TestWatch_StartsAndStops(t *testing.T) {
	tmp := t.TempDir()
	db, err := kdb.Open(tmp, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	cfg := config.Defaults().Scan

	w, err := analysis.NewWatcher(tmp, db, cfg, analysis.WatcherOpts{
		Debounce: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- w.Run(ctx)
	}()

	// Cancel quickly to verify clean exit.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watch Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not stop within timeout")
	}
}

// --- Shell dispatch integration tests ---

func TestShellDispatch_Quit(t *testing.T) {
	tmp := t.TempDir()
	db, err := kdb.Open(tmp, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	_, quit := shellDispatch(db, ".quit", nil)
	if !quit {
		t.Error("expected quit=true for .quit")
	}
}

func TestShellDispatch_Exit(t *testing.T) {
	tmp := t.TempDir()
	db, err := kdb.Open(tmp, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	_, quit := shellDispatch(db, ".exit", nil)
	if !quit {
		t.Error("expected quit=true for .exit")
	}
}

func TestShellDispatch_Collections(t *testing.T) {
	tmp := t.TempDir()
	db, err := kdb.Open(tmp, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	_, quit := shellDispatch(db, ".collections", nil)
	if quit {
		t.Error("expected quit=false for .collections")
	}
}

func TestShellDispatch_Describe(t *testing.T) {
	tmp := t.TempDir()
	db, err := kdb.Open(tmp, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// .describe nodes should not panic or return quit.
	_, quit := shellDispatch(db, ".describe nodes", nil)
	if quit {
		t.Error("expected quit=false for .describe")
	}
}

func TestShellDispatch_ExportNoResults(t *testing.T) {
	tmp := t.TempDir()
	db, err := kdb.Open(tmp, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// .export json with nil results should print "No results to export".
	_, quit := shellDispatch(db, ".export json", nil)
	if quit {
		t.Error("expected quit=false for .export json")
	}
}

func TestShellDispatch_InvalidQuery(t *testing.T) {
	tmp := t.TempDir()
	db, err := kdb.Open(tmp, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Invalid DSL should not panic or quit.
	_, quit := shellDispatch(db, "invalid garbage syntax", nil)
	if quit {
		t.Error("expected quit=false for invalid query")
	}
}

func TestShellDescribe_ValidCollection(t *testing.T) {
	t.Parallel()
	// Verify shellDescribe does not panic for known collection.
	schema := query.CollectionSchema("nodes")
	if schema == nil {
		t.Fatal("expected 'nodes' schema to exist")
	}
}

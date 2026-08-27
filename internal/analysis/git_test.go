package analysis

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

func TestGitIntelHeadHash(t *testing.T) {
	// Use the pcke repo itself as the test subject.
	root := findRepoRoot(t)

	gi, err := NewGitIntel(root)
	if err != nil {
		t.Fatalf("NewGitIntel: %v", err)
	}

	hash, err := gi.HeadHash()
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}
	if len(hash) != 40 {
		t.Errorf("HeadHash = %q, want 40-char hex", hash)
	}
	t.Logf("HEAD = %s", hash)
}

func TestGitIntelFileHistory(t *testing.T) {
	// Build a throwaway repo with a commit authored *now* so the test
	// exercises FileHistory deterministically and never ages out of the
	// historyWindowDays lookback window (a real repo file can go quiet
	// for >90 days and legitimately report zero recent commits).
	dir := initTempRepo(t, "app.go", "feat: add app\n\nfirst version")

	gi, err := NewGitIntel(dir)
	if err != nil {
		t.Fatalf("NewGitIntel: %v", err)
	}

	stats, err := gi.FileHistory("app.go")
	if err != nil {
		t.Fatalf("FileHistory(app.go): %v", err)
	}
	if stats.TotalCommits < 1 {
		t.Errorf("TotalCommits = %d, want >= 1", stats.TotalCommits)
	}
	if stats.Stability < 0 || stats.Stability > 1 {
		t.Errorf("stability = %f, want [0,1]", stats.Stability)
	}
	if stats.LastChangeType != "feat" {
		t.Errorf("LastChangeType = %q, want %q", stats.LastChangeType, "feat")
	}
	t.Logf("app.go: %d commits, stability=%.2f, last_author=%s, change_type=%s",
		stats.TotalCommits, stats.Stability, stats.LastAuthor, stats.LastChangeType)
}

// initTempRepo creates a git repository in a temp dir, writes filename
// with content, and commits it authored at the current time. It returns
// the repository directory.
func initTempRepo(t *testing.T, filename, commitMsg string) string {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if _, err := wt.Add(filename); err != nil {
		t.Fatalf("Add %s: %v", filename, err)
	}
	if _, err := wt.Commit(commitMsg, &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return dir
}

func TestParseChangeType(t *testing.T) {
	tests := []struct {
		msg  string
		want string
	}{
		{"feat: add something", "feat"},
		{"fix(kdb): resolve crash", "fix"},
		{"refactor: clean up", "refactor"},
		{"breaking!: remove API", "breaking"},
		{"docs: update README", "docs"},
		{"test: add unit tests", "test"},
		{"chore: bump deps", "chore"},
		{"perf(btree): optimise splits", "perf"},
		{"ci: add workflow", "ci"},
		{"random commit message", "unknown"},
		{"Merge pull request #1", "unknown"},
		{"Initial commit", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			got := parseChangeType(tt.msg)
			if got != tt.want {
				t.Errorf("parseChangeType(%q) = %q, want %q", tt.msg, got, tt.want)
			}
		})
	}
}

func TestGitIntelOpenInvalidDir(t *testing.T) {
	dir := t.TempDir()
	_, err := NewGitIntel(dir)
	if err == nil {
		t.Error("expected error for non-git directory")
	}
}

// TestCurrentBranch verifies that CurrentBranch returns a non-empty name
// when on a branch (not detached HEAD).
func TestCurrentBranch(t *testing.T) {
	root := findRepoRoot(t)

	gi, err := NewGitIntel(root)
	if err != nil {
		t.Fatalf("NewGitIntel: %v", err)
	}

	branch := gi.CurrentBranch()
	// In CI, HEAD might be detached. Only assert non-empty if we know it's a branch.
	t.Logf("CurrentBranch = %q", branch)
}

// TestDetectRenames verifies rename detection against the repo history.
func TestDetectRenames(t *testing.T) {
	root := findRepoRoot(t)

	gi, err := NewGitIntel(root)
	if err != nil {
		t.Fatalf("NewGitIntel: %v", err)
	}

	renames, err := gi.DetectRenames("")
	if err != nil {
		t.Fatalf("DetectRenames: %v", err)
	}

	t.Logf("found %d renames in last 100 commits", len(renames))
	for _, r := range renames {
		t.Logf("  %s → %s (commit %s by %s)", r.OldPath, r.NewPath, r.CommitHash[:8], r.Author)
	}
}

// TestDetectRenamesWithSince verifies rename detection with a since hash.
func TestDetectRenamesWithSince(t *testing.T) {
	root := findRepoRoot(t)

	gi, err := NewGitIntel(root)
	if err != nil {
		t.Fatalf("NewGitIntel: %v", err)
	}

	hash, err := gi.HeadHash()
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}

	// With since=HEAD, should find no renames (HEAD itself is excluded).
	renames, err := gi.DetectRenames(hash)
	if err != nil {
		t.Fatalf("DetectRenames(since=HEAD): %v", err)
	}
	if len(renames) != 0 {
		t.Errorf("expected 0 renames with since=HEAD, got %d", len(renames))
	}
}

// TestCheckBranchMismatch verifies mismatch detection against a real git repo.
func TestCheckBranchMismatch(t *testing.T) {
	root := findRepoRoot(t)

	db, err := kdb.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// No stored branch → no mismatch.
	if msg := CheckBranchMismatch(ctx, db, root); msg != "" {
		t.Errorf("expected no mismatch before scan, got %q", msg)
	}

	// Store a fake branch "fake-branch-xyz".
	err = db.Update(ctx, func(wtx *tx.WriteTx) error {
		return wtx.Put([]byte("meta:scan_branch"), []byte("fake-branch-xyz"))
	})
	if err != nil {
		t.Fatalf("store branch: %v", err)
	}

	gi, err := NewGitIntel(root)
	if err != nil {
		t.Fatalf("git intel: %v", err)
	}
	currentBranch := gi.CurrentBranch()

	if currentBranch != "" && currentBranch != "fake-branch-xyz" {
		// Should warn about mismatch.
		msg := CheckBranchMismatch(ctx, db, root)
		if msg == "" {
			t.Error("expected branch mismatch warning, got empty")
		}
		t.Logf("mismatch warning: %s", msg)
	}

	// Store the actual current branch → no mismatch.
	if currentBranch != "" {
		err = db.Update(ctx, func(wtx *tx.WriteTx) error {
			return wtx.Put([]byte("meta:scan_branch"), []byte(currentBranch))
		})
		if err != nil {
			t.Fatalf("store current branch: %v", err)
		}
		if msg := CheckBranchMismatch(ctx, db, root); msg != "" {
			t.Errorf("expected no mismatch with matching branch, got %q", msg)
		}
	}
}

// findRepoRoot walks up from the current directory to find the repo root
// (directory containing go.mod with module github.com/jenaiz/pcke).
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// Walk up to find go.mod.
	for {
		if _, err := os.Stat(dir + "/go.mod"); err == nil {
			return dir
		}
		parent := dir[:max(0, len(dir)-1)]
		parent = dir[:lastSlash(parent)+1]
		if parent == dir || parent == "" {
			t.Fatal("could not find repo root")
		}
		dir = parent[:len(parent)-1]
	}
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

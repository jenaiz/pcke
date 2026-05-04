package analysis // git.go — go-git intelligence for change frequency, stability, and authorship.

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// GitIntel extracts intelligence from a git repository.
type GitIntel struct {
	repo *git.Repository
}

// NewGitIntel opens the git repository at dir and returns a [GitIntel].
// Returns an error if dir is not inside a git working tree.
func NewGitIntel(dir string) (*GitIntel, error) {
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{
		DetectDotGit: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open git repo: %w", err)
	}
	return &GitIntel{repo: repo}, nil
}

// HeadHash returns the current HEAD commit hash as a hex string.
func (g *GitIntel) HeadHash() (string, error) {
	ref, err := g.repo.Head()
	if err != nil {
		return "", fmt.Errorf("resolve HEAD: %w", err)
	}
	return ref.Hash().String(), nil
}

// CurrentBranch returns the short name of the current branch (e.g., "main").
// Returns an empty string if HEAD is detached.
func (g *GitIntel) CurrentBranch() string {
	ref, err := g.repo.Head()
	if err != nil {
		return ""
	}
	name := ref.Name()
	if !name.IsBranch() {
		return "" // detached HEAD
	}
	return name.Short()
}

// FileStats holds per-file git statistics.
type FileStats struct {
	TotalCommits   int
	RecentCommits  int // Commits in the last 90 days.
	Stability      float64
	LastAuthor     string
	LastChangeType string
	LastCommitTime time.Time
}

// FileHistory returns [FileStats] for the given file path. The path
// should be relative to the repository root, using forward slashes.
func (g *GitIntel) FileHistory(relPath string) (FileStats, error) {
	logOpts := &git.LogOptions{
		FileName: &relPath,
	}
	iter, err := g.repo.Log(logOpts)
	if err != nil {
		return FileStats{}, fmt.Errorf("git log %s: %w", relPath, err)
	}
	defer iter.Close()

	cutoff := time.Now().AddDate(0, 0, -90)
	var stats FileStats

	if err := iter.ForEach(func(c *object.Commit) error {
		stats.TotalCommits++
		if c.Author.When.After(cutoff) {
			stats.RecentCommits++
		}
		if stats.TotalCommits == 1 {
			stats.LastAuthor = c.Author.Name
			stats.LastCommitTime = c.Author.When
			stats.LastChangeType = parseChangeType(c.Message)
		}
		return nil
	}); err != nil {
		return FileStats{}, fmt.Errorf("iterate log %s: %w", relPath, err)
	}

	if stats.TotalCommits > 0 {
		stats.Stability = 1.0 - float64(stats.RecentCommits)/float64(max(stats.TotalCommits, 1))
		if stats.Stability < 0 {
			stats.Stability = 0
		}
	}

	return stats, nil
}

// AuthorCommits pairs an author name with their commit count for a module.
type AuthorCommits struct {
	Author  string
	Commits int
}

// Authorship returns authorship information for all files, grouped by
// module. The key is the module name (from [DetectModule]), the value
// lists authors sorted by commit count (descending).
func (g *GitIntel) Authorship() (map[string][]AuthorCommits, error) {
	iter, err := g.repo.Log(&git.LogOptions{})
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	defer iter.Close()

	// module → author → count
	counts := map[string]map[string]int{}

	if err := iter.ForEach(func(c *object.Commit) error {
		fstats, err := c.Stats()
		if err != nil {
			return nil // Skip commits where stats can't be computed.
		}
		for _, f := range fstats {
			mod := DetectModule(f.Name)
			if counts[mod] == nil {
				counts[mod] = map[string]int{}
			}
			counts[mod][c.Author.Name]++
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("iterate authorship: %w", err)
	}

	result := make(map[string][]AuthorCommits, len(counts))
	for mod, authors := range counts {
		for author, n := range authors {
			result[mod] = append(result[mod], AuthorCommits{Author: author, Commits: n})
		}
		sortAuthorCommits(result[mod])
	}
	return result, nil
}

// GitIgnoredFiles returns the set of files that are git-ignored.
// This uses the worktree status to detect ignored files.
func (g *GitIntel) GitIgnoredFiles() (map[string]bool, error) {
	wt, err := g.repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("worktree: %w", err)
	}
	status, err := wt.Status()
	if err != nil {
		return nil, fmt.Errorf("worktree status: %w", err)
	}

	ignored := map[string]bool{}
	for path, s := range status {
		if s.Worktree == git.Untracked {
			ignored[path] = true
		}
	}
	return ignored, nil
}

// conventionalRe matches conventional commit prefixes.
var conventionalRe = regexp.MustCompile(`^(feat|fix|refactor|breaking|docs|test|chore|perf|ci|build|style|revert)(\(.+?\))?[!]?:\s`)

// parseChangeType extracts the conventional commit type from a message.
// Returns "unknown" if the message does not follow the convention.
func parseChangeType(message string) string {
	first := strings.SplitN(message, "\n", 2)[0]
	m := conventionalRe.FindStringSubmatch(first)
	if len(m) >= 2 {
		return m[1]
	}
	return "unknown"
}

// sortAuthorCommits sorts by commit count descending.
func sortAuthorCommits(ac []AuthorCommits) {
	for i := 1; i < len(ac); i++ {
		for j := i; j > 0 && ac[j].Commits > ac[j-1].Commits; j-- {
			ac[j], ac[j-1] = ac[j-1], ac[j]
		}
	}
}

// RenameEntry records a detected file rename in git history.
type RenameEntry struct {
	OldPath    string
	NewPath    string
	CommitHash string
	Author     string
	Timestamp  time.Time
}

// DetectRenames finds file renames in git history since the given commit hash.
// If sinceHash is empty, it scans the last 100 commits. Returns renames detected
// via tree-diff similarity matching (equivalent to git log --follow --diff-filter=R).
func (g *GitIntel) DetectRenames(sinceHash string) ([]RenameEntry, error) {
	logOpts := &git.LogOptions{}
	iter, err := g.repo.Log(logOpts)
	if err != nil {
		return nil, fmt.Errorf("git log for renames: %w", err)
	}
	defer iter.Close()

	var since plumbing.Hash
	if sinceHash != "" {
		since = plumbing.NewHash(sinceHash)
	}

	var renames []RenameEntry
	const maxCommits = 100
	count := 0

	err = iter.ForEach(func(c *object.Commit) error {
		count++
		if count > maxCommits {
			return errStopIter
		}

		// Stop if we've reached the "since" commit.
		if sinceHash != "" && c.Hash == since {
			return errStopIter
		}

		// Compare with parent(s) to find renames.
		parentIter := c.Parents()
		defer parentIter.Close()

		return parentIter.ForEach(func(parent *object.Commit) error {
			parentTree, err := parent.Tree()
			if err != nil {
				return nil // skip
			}
			childTree, err := c.Tree()
			if err != nil {
				return nil // skip
			}

			changes, err := parentTree.Diff(childTree)
			if err != nil {
				return nil // skip
			}

			for _, change := range changes {
				// A rename shows as a delete + add with high similarity.
				// go-git's Diff detects this via the Action field.
				from := change.From
				to := change.To

				if from.Name != "" && to.Name != "" && from.Name != to.Name {
					renames = append(renames, RenameEntry{
						OldPath:    from.Name,
						NewPath:    to.Name,
						CommitHash: c.Hash.String(),
						Author:     c.Author.Name,
						Timestamp:  c.Author.When,
					})
				}
			}
			return nil
		})
	})

	if err != nil && err != errStopIter {
		return nil, fmt.Errorf("iterate commits for renames: %w", err)
	}

	return renames, nil
}

// errStopIter is a sentinel error to stop commit iteration early.
var errStopIter = fmt.Errorf("stop iteration")

// ChangedFiles returns files changed on the current branch vs base (main/master).
// Falls back to uncommitted changes if already on main/master.
func (g *GitIntel) ChangedFiles() ([]string, error) {
	branch := g.CurrentBranch()

	// If on main/master or detached, return uncommitted changes.
	if branch == "" || branch == "main" || branch == "master" {
		return g.uncommittedFiles()
	}

	return g.branchChangedFiles()
}

// branchChangedFiles returns files changed on the current branch vs base.
func (g *GitIntel) branchChangedFiles() ([]string, error) {
	baseRef, err := g.resolveBase()
	if err != nil {
		return g.uncommittedFiles()
	}

	headRef, err := g.repo.Head()
	if err != nil {
		return nil, fmt.Errorf("resolve HEAD: %w", err)
	}

	baseCommit, err := g.repo.CommitObject(baseRef.Hash())
	if err != nil {
		return g.uncommittedFiles()
	}

	headCommit, err := g.repo.CommitObject(headRef.Hash())
	if err != nil {
		return nil, fmt.Errorf("resolve HEAD commit: %w", err)
	}

	baseTree, err := baseCommit.Tree()
	if err != nil {
		return g.uncommittedFiles()
	}

	headTree, err := headCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("resolve HEAD tree: %w", err)
	}

	changes, err := baseTree.Diff(headTree)
	if err != nil {
		return g.uncommittedFiles()
	}

	files := make(map[string]bool)
	for _, change := range changes {
		if change.From.Name != "" {
			files[change.From.Name] = true
		}
		if change.To.Name != "" {
			files[change.To.Name] = true
		}
	}

	result := make([]string, 0, len(files))
	for f := range files {
		result = append(result, f)
	}
	return result, nil
}

// resolveBase finds the reference for main or master branch.
func (g *GitIntel) resolveBase() (*plumbing.Reference, error) {
	ref, err := g.repo.Reference(plumbing.NewBranchReferenceName("main"), true)
	if err == nil {
		return ref, nil
	}
	return g.repo.Reference(plumbing.NewBranchReferenceName("master"), true)
}

// uncommittedFiles returns files with uncommitted changes (staged + unstaged).
func (g *GitIntel) uncommittedFiles() ([]string, error) {
	wt, err := g.repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("worktree: %w", err)
	}
	status, err := wt.Status()
	if err != nil {
		return nil, fmt.Errorf("worktree status: %w", err)
	}

	var files []string
	for path, s := range status {
		if s.Staging != git.Unmodified || s.Worktree != git.Unmodified {
			files = append(files, path)
		}
	}
	return files, nil
}

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

// historyWindowDays is the lookback window FileHistory walks when
// computing per-file git statistics.
//
// The previous implementation iterated the entire commit history per
// file, which is O(commits × files) under go-git's --follow semantics
// and scales poorly: on PCKE's own repo TestScannerFullScan crossed
// the 10-minute test timeout once the branch grew past ~50 commits.
// Bounding the window keeps the scan deterministic-time without
// changing the meaning of "stable file" — files with no recent activity
// are still surfaced as such; ancient cold history rarely changes the
// answer.
const historyWindowDays = 90

// FileStats holds per-file git statistics.
//
// Both commit counts cover the [now-historyWindowDays, now] window. The
// older "TotalCommits = all commits ever" semantic was retired together
// with the unbounded scan; downstream consumers (the Stability metric
// and onboarding aggregates) only care about recent churn.
type FileStats struct {
	TotalCommits   int     // commits within the history window
	RecentCommits  int     // same as TotalCommits in the windowed implementation
	Stability      float64 // 1.0 = no churn, 0.0 = ≥ stabilityChurnSaturation commits in window
	LastAuthor     string
	LastChangeType string
	LastCommitTime time.Time
}

// stabilityChurnSaturation is the number of in-window commits at which
// Stability hits 0. Anything between 0 and this scales linearly.
const stabilityChurnSaturation = 10

// fileHistoryMaxIter caps the number of commits FileHistory walks per
// file. go-git's LogOptions{FileName: ...} uses --follow semantics that
// scan the whole history checking each commit's diff against parent.
// Without a cap, that is O(all-commits) per file even when the Since
// filter restricts output. The cap turns it into O(min(cap, commits-
// to-touch-this-file)) and bounds full-scan time.
//
// Once the cap is hit, FileHistory returns the partial stats it
// gathered and sets Stability to 0 (treat capped files as high-churn:
// any file touched by ≥cap commits in 90 days is, by definition, busy).
const fileHistoryMaxIter = 50

// AllFileHistory returns FileStats keyed by repository-relative path
// for every file touched within the last historyWindowDays.
//
// Implementation: one pass over the commit DAG (filtered by Since) +
// per-commit diff vs first parent. Each commit's diff is O(changed
// files for that commit), so the total cost is O(commits-in-window ×
// avg-files-per-commit) — much cheaper than calling FileHistory per
// file (each of those walks the full DAG).
//
// Files absent from the returned map have no commits in the window.
// Treat absence as zero-stats.
//
// LastAuthor / LastCommitTime / LastChangeType reflect the most recent
// commit that touched a given file (the iterator yields commits in
// reverse chronological order from HEAD, so the first visit per file
// is the most recent).
func (g *GitIntel) AllFileHistory() (map[string]FileStats, error) {
	cutoff := time.Now().AddDate(0, 0, -historyWindowDays)
	iter, err := g.repo.Log(&git.LogOptions{Since: &cutoff})
	if err != nil {
		return nil, fmt.Errorf("git log (all-file): %w", err)
	}
	defer iter.Close()

	out := make(map[string]FileStats)
	if err := iter.ForEach(func(c *object.Commit) error {
		fileStats, err := c.Stats()
		if err != nil {
			// Root commits and a few edge cases can fail Stats();
			// skip rather than abort the whole aggregation.
			return nil
		}
		for _, fs := range fileStats {
			entry := out[fs.Name]
			entry.TotalCommits++
			entry.RecentCommits++
			if entry.TotalCommits == 1 {
				entry.LastAuthor = c.Author.Name
				entry.LastCommitTime = c.Author.When
				entry.LastChangeType = parseChangeType(c.Message)
			}
			out[fs.Name] = entry
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("iterate log (all-file): %w", err)
	}

	for path, entry := range out {
		entry.Stability = 1.0 - float64(entry.RecentCommits)/float64(stabilityChurnSaturation)
		if entry.Stability < 0 {
			entry.Stability = 0
		}
		if entry.Stability > 1 {
			entry.Stability = 1
		}
		out[path] = entry
	}
	return out, nil
}

// FileHistory returns [FileStats] for the given file path. The path
// should be relative to the repository root, using forward slashes.
//
// Iteration is bounded by:
//
//   - LogOptions.Since (the historyWindowDays cutoff) — go-git filters
//     output to commits within the window.
//   - fileHistoryMaxIter — early-exits the iteration once enough
//     commits are seen, capping per-file work even when the underlying
//     walk is slow.
func (g *GitIntel) FileHistory(relPath string) (FileStats, error) {
	cutoff := time.Now().AddDate(0, 0, -historyWindowDays)
	logOpts := &git.LogOptions{
		FileName: &relPath,
		Since:    &cutoff,
	}
	iter, err := g.repo.Log(logOpts)
	if err != nil {
		return FileStats{}, fmt.Errorf("git log %s: %w", relPath, err)
	}
	defer iter.Close()

	var stats FileStats
	capped := false
	if err := iter.ForEach(func(c *object.Commit) error {
		stats.TotalCommits++
		stats.RecentCommits++
		if stats.TotalCommits == 1 {
			stats.LastAuthor = c.Author.Name
			stats.LastCommitTime = c.Author.When
			stats.LastChangeType = parseChangeType(c.Message)
		}
		if stats.TotalCommits >= fileHistoryMaxIter {
			capped = true
			return errStopIter
		}
		return nil
	}); err != nil && err != errStopIter {
		return FileStats{}, fmt.Errorf("iterate log %s: %w", relPath, err)
	}

	if capped {
		// Capped files are treated as fully unstable: they have at least
		// fileHistoryMaxIter commits in the window, well past the
		// stabilityChurnSaturation threshold.
		stats.Stability = 0
		return stats, nil
	}
	stats.Stability = 1.0 - float64(stats.RecentCommits)/float64(stabilityChurnSaturation)
	if stats.Stability < 0 {
		stats.Stability = 0
	}
	if stats.Stability > 1 {
		stats.Stability = 1
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

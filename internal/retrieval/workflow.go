package retrieval

import (
	"path"
	"strings"
)

// Confidence is a [0, 1] score describing how sure DetectWorkflow is of
// its result. Callers can branch on the named thresholds: a "low"
// confidence detection should generally fall back to WorkflowExplore.
type Confidence float64

// Confidence thresholds. A detection at or above ConfidenceHigh is
// trustworthy enough to drive ranking; below ConfidenceLow the engine
// should treat the workflow as WorkflowExplore (neutral weights).
const (
	ConfidenceLow  Confidence = 0.5
	ConfidenceHigh Confidence = 0.8
)

// Level reports the qualitative bucket for a confidence value:
// "high" (>=0.8), "medium" (0.5-0.8), or "low" (<0.5).
func (c Confidence) Level() string {
	switch {
	case c >= ConfidenceHigh:
		return "high"
	case c >= ConfidenceLow:
		return "medium"
	default:
		return "low"
	}
}

// DetectionContext holds the signals DetectWorkflow reasons over. All
// fields are optional; an empty context detects WorkflowExplore with
// low confidence. Callers populate what they can from git state and the
// active MCP session.
type DetectionContext struct {
	// BranchName is the current git branch short name (e.g. "fix/leak").
	BranchName string
	// ChangedFiles are worktree-relative paths that differ from HEAD.
	ChangedFiles []string
	// SessionFiles are file refs the agent accessed this session (from
	// the o:session subgraph). Used to detect review workflows where the
	// agent reads many files but changes none.
	SessionFiles []string
	// HasUncommitted is true when the worktree has uncommitted changes.
	HasUncommitted bool
	// IsMainBranch is true when BranchName is a trunk branch
	// (main / master / develop).
	IsMainBranch bool
}

// DetectWorkflow infers the developer's current workflow from git state
// and session shape (PRD v5.2 §6.2 F15.T1). It evaluates an ordered set
// of heuristics and returns the first match, falling back to
// WorkflowExplore at low confidence when nothing matches.
//
// The rules, in priority order:
//
//  1. Branch fix/* | bug/* | hotfix/*       -> bugfix   (0.9)
//  2. Branch feat/* | feature/* | add-*     -> feature  (0.9)
//  3. Branch refactor/* | cleanup/* | chore/* -> refactor (0.9)
//  4. Branch test/* | testing/*             -> test     (0.85)
//  5. >80% of changed files are *_test.*    -> test     (0.8)
//  6. >=3 session files, 0 changed files    -> review   (0.7)
//  7. On a trunk branch, no uncommitted work -> explore (0.6)
//  8. (default)                             -> explore  (0.3)
//
// Detection is deterministic: the same context always yields the same
// result.
func DetectWorkflow(dc DetectionContext) (Workflow, Confidence) {
	branch := strings.ToLower(strings.TrimSpace(dc.BranchName))

	if w, ok := detectFromBranch(branch); ok {
		return w.workflow, w.confidence
	}
	if testFileRatio(dc.ChangedFiles) > 0.8 {
		return WorkflowTest, 0.8
	}
	if len(dc.ChangedFiles) == 0 && len(dc.SessionFiles) >= 3 {
		return WorkflowReview, 0.7
	}
	if dc.IsMainBranch && !dc.HasUncommitted {
		return WorkflowExplore, 0.6
	}
	return WorkflowExplore, 0.3
}

// branchMatch pairs a detected workflow with its confidence.
type branchMatch struct {
	workflow   Workflow
	confidence Confidence
}

// branchPrefixRules maps git-flow style branch prefixes to workflows.
// Order matters only for documentation; prefixes are disjoint so the
// first hit is unambiguous.
var branchPrefixRules = []struct {
	prefixes   []string
	workflow   Workflow
	confidence Confidence
}{
	{[]string{"fix/", "bug/", "bugfix/", "hotfix/"}, WorkflowBugfix, 0.9},
	{[]string{"feat/", "feature/", "add-"}, WorkflowFeature, 0.9},
	{[]string{"refactor/", "cleanup/", "chore/"}, WorkflowRefactor, 0.9},
	{[]string{"test/", "testing/", "tests/"}, WorkflowTest, 0.85},
}

// detectFromBranch returns the workflow implied by the branch name, if
// any prefix rule matches.
func detectFromBranch(branch string) (branchMatch, bool) {
	if branch == "" {
		return branchMatch{}, false
	}
	for _, rule := range branchPrefixRules {
		for _, p := range rule.prefixes {
			if strings.HasPrefix(branch, p) {
				return branchMatch{rule.workflow, rule.confidence}, true
			}
		}
	}
	return branchMatch{}, false
}

// testFileRatio returns the fraction of paths that look like test files
// (basename matching *_test.* or living under a test/ or tests/ dir).
// An empty slice returns 0.
func testFileRatio(files []string) float64 {
	if len(files) == 0 {
		return 0
	}
	testCount := 0
	for _, f := range files {
		if isTestFile(f) {
			testCount++
		}
	}
	return float64(testCount) / float64(len(files))
}

// isTestFile reports whether p is a test file: a basename containing
// "_test." (Go, plus _test.py/_test.js style) or a path segment that is
// exactly "test" or "tests".
func isTestFile(p string) bool {
	base := path.Base(p)
	if strings.Contains(base, "_test.") {
		return true
	}
	if strings.HasPrefix(base, "test_") {
		return true
	}
	for _, seg := range strings.Split(path.Dir(p), "/") {
		switch seg {
		case "test", "tests", "testdata":
			return true
		}
	}
	return false
}

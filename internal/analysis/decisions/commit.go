package decisions

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// CommitInfo is the minimal commit metadata BackfillFromCommits
// consumes. The orchestrator (T5.4) populates this from GitIntel; the
// decisions package stays decoupled from go-git.
type CommitInfo struct {
	// Hash is the full commit SHA. shortSHA(12) becomes part of the DID.
	Hash string
	// Author is the commit's recorded author name.
	Author string
	// Time is the author timestamp.
	Time time.Time
	// Message is the full commit message (subject + body).
	Message string
}

// commitDecisionPattern matches conventional decision-marker prefixes
// at the start of a commit subject. Case-insensitive. Requires the
// trailing colon to avoid false positives from words like "decisional"
// or "rfcs".
var commitDecisionPattern = regexp.MustCompile(`(?i)^(decision|adr|rfc):`)

// shortSHALen is the prefix length used in DIDs derived from commit
// hashes. 12 hex chars (~48 bits) is enough collision resistance for
// per-repo dedup and matches `git log --oneline` defaults.
const shortSHALen = 12

// BackfillFromCommits writes one Decision per commit whose subject
// matches commitDecisionPattern. Other commits are skipped.
//
// Translation policy:
//
//   - DID = "commit:" + shortSHA(Hash, 12).
//   - Title = first non-empty line of the message (the conventional
//     subject), with leading "decision:" / "adr:" / "rfc:" prefix
//     trimmed for legibility.
//   - Body = full commit message unchanged.
//   - Severity = SeverityShould (commits are conventions / advice; the
//     stronger SeverityMust is reserved for ADR files).
//   - Scope = ScopeGlobal — commit messages aren't file-scoped.
//   - Source = "commit".
//   - Header.CreatedAt = commit Time.
//   - Header.Lifecycle = Active.
//
// Idempotent: the DID is stable per commit, so re-runs over the same
// commit set are no-ops.
func BackfillFromCommits(ctx context.Context, db UpdateDB, commits []CommitInfo) (int, error) {
	if len(commits) == 0 {
		return 0, nil
	}

	store := event.New(db)
	written := 0
	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		for _, c := range commits {
			if !commitDecisionPattern.MatchString(commitSubject(c.Message)) {
				continue
			}
			if c.Hash == "" {
				continue
			}
			d := decisionFromCommit(c)
			ok, err := writeDecision(wtx, store, d)
			if err != nil {
				return err
			}
			if ok {
				written++
			}
		}
		return nil
	}); err != nil {
		return written, fmt.Errorf("backfill commits: %w", err)
	}
	return written, nil
}

// decisionFromCommit builds the Decision payload for one commit.
// Pulled out so unit tests verify the mapping without touching kdb.
func decisionFromCommit(c CommitInfo) *event.Decision {
	subject := commitSubject(c.Message)
	title := commitDecisionPattern.ReplaceAllString(subject, "")
	title = trimSpaceLeft(title)
	if title == "" {
		title = subject
	}
	if r := []rune(title); len(r) > 200 {
		title = string(r[:200])
	}

	return &event.Decision{
		Hdr: event.Header{
			CreatedAt: c.Time,
			Lifecycle: event.LifecycleActive,
		},
		DID:      "commit:" + shortSHA(c.Hash),
		Title:    title,
		Body:     c.Message,
		Severity: event.SeverityShould,
		Scope:    event.ScopeGlobal,
		Source:   string(SourceCommit),
	}
}

// commitSubject returns the first line of msg (the conventional commit
// subject). Empty input yields "".
func commitSubject(msg string) string {
	for i := 0; i < len(msg); i++ {
		if msg[i] == '\n' {
			return msg[:i]
		}
	}
	return msg
}

// shortSHA returns the first shortSHALen chars of hash, or the whole
// hash if shorter.
func shortSHA(hash string) string {
	if len(hash) <= shortSHALen {
		return hash
	}
	return hash[:shortSHALen]
}

// trimSpaceLeft trims leading whitespace bytes (space, tab) without
// touching the trailing edge. We don't use strings.TrimLeft because
// the caller has already trimmed the suffix via the regex.
func trimSpaceLeft(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[i:]
}

var _ = context.Background

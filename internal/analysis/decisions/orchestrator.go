package decisions

import (
	"context"
	"fmt"
)

// CommitSource is the dependency the orchestrator needs to fetch
// recent commits. *analysis.GitIntel.RecentCommits satisfies it; tests
// can supply a stub.
type CommitSource interface {
	RecentCommits() ([]CommitInfo, error)
}

// BackfillAll runs every decision-backfill source against the database
// and returns the per-source totals. Source ordering is stable so test
// output is deterministic: ADR -> annotation -> doc -> commit.
//
// Each source is independent: if one fails, the orchestrator returns
// the error along with the partial Result accumulated so far. Callers
// can decide whether to proceed (e.g. log + continue) or abort.
//
// repoRoot is the path the scanner is operating on; commits is
// optional (pass nil to skip the commit-message scan, useful for tests
// or repos without git history). files is the full set of scanned file
// ids used by the doc backfill's module-link pass; pass nil to skip it.
func BackfillAll(ctx context.Context, db UpdateDB, repoRoot string, commits CommitSource, files []string) (Result, error) {
	var r Result

	n, err := BackfillFromADRs(ctx, db, repoRoot)
	r.ADRs = n
	if err != nil {
		return r, fmt.Errorf("backfill ADRs: %w", err)
	}

	anns, err := WalkForAnnotations(repoRoot)
	if err != nil {
		return r, fmt.Errorf("walk for annotations: %w", err)
	}
	n, err = BackfillFromAnnotations(ctx, db, anns)
	r.Annotations = n
	if err != nil {
		return r, fmt.Errorf("backfill annotations: %w", err)
	}

	n, err = BackfillFromDocs(ctx, db, repoRoot, files)
	r.Docs = n
	if err != nil {
		return r, fmt.Errorf("backfill docs: %w", err)
	}

	if commits != nil {
		commitInfos, err := commits.RecentCommits()
		if err != nil {
			return r, fmt.Errorf("fetch recent commits: %w", err)
		}
		n, err = BackfillFromCommits(ctx, db, commitInfos)
		r.Commits = n
		if err != nil {
			return r, fmt.Errorf("backfill commits: %w", err)
		}
	}

	return r, nil
}

package federation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/query"
)

// QueryOpts controls federated query behavior.
type QueryOpts struct {
	Timeout     time.Duration // per-repo timeout (default 10s)
	Concurrency int           // max parallel DB opens (default 4)
	Limit       int           // total result limit (0 = unlimited)
	RepoFilter  []string      // optional: only query these repos (empty = all)
}

// FederatedResultSet contains merged results from multiple repos.
type FederatedResultSet struct {
	Results []FederatedRow
	Repos   []string    // repos that contributed results
	Errors  []RepoError // repos that failed (partial results)
}

// FederatedRow is a query result annotated with its source repo.
type FederatedRow struct {
	Repo string
	Row  query.Row
}

// RepoError records a failure from a specific repo.
type RepoError struct {
	Repo  string
	Error error
}

func defaultOpts(opts QueryOpts) QueryOpts {
	if opts.Timeout == 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	return opts
}

// QueryFederation executes a DSL query across all federated repos.
// It parses the query once, then fans out execution to each repo in parallel.
// Repos that fail to open or query are reported in Errors but do not block
// results from healthy repos.
func QueryFederation(ctx context.Context, manifest *Manifest, dsl string, opts QueryOpts) (*FederatedResultSet, error) {
	opts = defaultOpts(opts)

	// Parse + plan once.
	q, err := query.Parse(dsl)
	if err != nil {
		return nil, fmt.Errorf("federation query: parse: %w", err)
	}
	plan := query.BuildPlan(q)

	// Determine target repos.
	repos := manifest.Repos
	if len(opts.RepoFilter) > 0 {
		repos = filterRepos(manifest.Repos, opts.RepoFilter)
	}
	if len(repos) == 0 {
		return &FederatedResultSet{}, nil
	}

	// Fan out with bounded concurrency.
	results := make([]repoResult, len(repos))
	sem := make(chan struct{}, opts.Concurrency)
	var wg sync.WaitGroup

	for i, repo := range repos {
		wg.Add(1)
		go func(idx int, r RepoEntry) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			repoCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
			defer cancel()

			rows, err := queryRepo(repoCtx, r.Path, plan)
			results[idx] = repoResult{repo: r.Name, rows: rows, err: err}
		}(i, repo)
	}
	wg.Wait()

	return mergeResults(results, opts.Limit), nil
}

// queryRepo opens a repo's kdb database and executes the plan.
func queryRepo(ctx context.Context, path string, plan *query.Plan) ([]query.Row, error) {
	db, err := kdb.Open(path, nil)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer db.Close() //nolint:errcheck // best-effort close after read

	rs, err := query.Execute(ctx, db, plan)
	if err != nil {
		return nil, fmt.Errorf("execute: %w", err)
	}
	return rs.Rows, nil
}

// filterRepos returns only repos whose names are in the filter list.
func filterRepos(repos []RepoEntry, filter []string) []RepoEntry {
	set := make(map[string]struct{}, len(filter))
	for _, f := range filter {
		set[f] = struct{}{}
	}
	var out []RepoEntry
	for _, r := range repos {
		if _, ok := set[r.Name]; ok {
			out = append(out, r)
		}
	}
	return out
}

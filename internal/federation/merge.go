package federation

import (
	"github.com/jenaiz/pcke/internal/query"
)

// repoResult holds per-repo query results or error.
type repoResult struct {
	repo string
	rows []query.Row
	err  error
}

// mergeResults combines per-repo results into a FederatedResultSet.
// Each row is annotated with _repo provenance. Dedup key is (repo, id).
func mergeResults(results []repoResult, limit int) *FederatedResultSet {
	frs := &FederatedResultSet{}
	seen := make(map[string]struct{}) // dedup key: "repo:id"

	for _, r := range results {
		if r.err != nil {
			frs.Errors = append(frs.Errors, RepoError{Repo: r.repo, Error: r.err})
			continue
		}
		if len(r.rows) > 0 {
			frs.Repos = append(frs.Repos, r.repo)
		}
		for _, row := range r.rows {
			// Annotate with provenance.
			row["_repo"] = r.repo

			// Dedup by (repo, id).
			id, _ := row["id"].(string)
			key := r.repo + ":" + id
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}

			frs.Results = append(frs.Results, FederatedRow{
				Repo: r.repo,
				Row:  row,
			})
			if limit > 0 && len(frs.Results) >= limit {
				return frs
			}
		}
	}
	return frs
}

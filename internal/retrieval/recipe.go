package retrieval

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// recipesDir is the conventional location, relative to a repo root, of
// user-defined workflow recipes (PRD v5.2 §6.2 F15.T5).
const recipesDir = ".pcke/recipes"

// Recipe is the on-disk (TOML) form of a WorkflowProfile. It lets users
// override a built-in workflow's weights and edge priorities, or define
// an entirely new named workflow, without recompiling.
//
// Example .pcke/recipes/review.toml:
//
//	name = "review"
//	edge_priority = ["decision_link"]
//	edge_boost    = 0.15
//	[weights]
//	recency   = 0.15
//	severity  = 0.40
//	proximity = 0.20
//	novelty   = 0.25
type Recipe struct {
	// Name is the workflow key the recipe defines or overrides
	// (e.g. "review", or a custom "security-audit").
	Name string `toml:"name"`
	// Weights is the four-factor scoring blend. All four fields should
	// sum to ~1.0; a sum outside [0.95, 1.05] is rejected.
	Weights RecipeWeights `toml:"weights"`
	// EdgePriority lists edge types whose 1-hop neighbours get boosted.
	EdgePriority []string `toml:"edge_priority"`
	// EdgeBoost is the additive priority-edge bonus; defaults to
	// edgeBoostDefault when omitted (zero) but a priority list is set.
	EdgeBoost float64 `toml:"edge_boost"`
}

// RecipeWeights mirrors Weights with TOML tags for decoding.
type RecipeWeights struct {
	Recency   float64 `toml:"recency"`
	Severity  float64 `toml:"severity"`
	Proximity float64 `toml:"proximity"`
	Novelty   float64 `toml:"novelty"`
}

// profile converts a validated Recipe into a WorkflowProfile.
func (r Recipe) profile() WorkflowProfile {
	boost := r.EdgeBoost
	if boost == 0 && len(r.EdgePriority) > 0 {
		boost = edgeBoostDefault
	}
	return WorkflowProfile{
		Workflow: Workflow(r.Name),
		Weights: Weights{
			Recency:   r.Weights.Recency,
			Severity:  r.Weights.Severity,
			Proximity: r.Weights.Proximity,
			Novelty:   r.Weights.Novelty,
		},
		EdgePriority: r.EdgePriority,
		EdgeBoost:    boost,
	}
}

// validate checks a recipe is well-formed: a non-empty name, weights
// summing to ~1.0, and an edge boost in [0, 1].
func (r Recipe) validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("recipe: name is required")
	}
	sum := r.Weights.Recency + r.Weights.Severity + r.Weights.Proximity + r.Weights.Novelty
	if math.Abs(sum-1.0) > 0.05 {
		return fmt.Errorf("recipe %q: weights sum to %.3f, want 1.0 (±0.05)", r.Name, sum)
	}
	if r.EdgeBoost < 0 || r.EdgeBoost > 1 {
		return fmt.Errorf("recipe %q: edge_boost %.3f out of range [0, 1]", r.Name, r.EdgeBoost)
	}
	return nil
}

// RecipeSet is the result of loading recipes: built-in profiles with
// any user overrides applied. Lookup falls back to the built-in table
// for workflows no recipe touched.
type RecipeSet struct {
	profiles map[Workflow]WorkflowProfile
}

// ProfileFor returns the (possibly recipe-overridden) profile for w,
// falling back to the WorkflowExplore baseline for unknown workflows —
// matching the package-level ProfileFor contract.
func (rs RecipeSet) ProfileFor(w Workflow) WorkflowProfile {
	if rs.profiles != nil {
		if p, ok := rs.profiles[w]; ok {
			return p
		}
	}
	return ProfileFor(w)
}

// Workflows returns the names of every workflow the set knows about
// (built-ins plus custom recipes), sorted for deterministic output.
func (rs RecipeSet) Workflows() []Workflow {
	seen := make(map[Workflow]struct{})
	for w := range workflowProfiles {
		seen[w] = struct{}{}
	}
	for w := range rs.profiles {
		seen[w] = struct{}{}
	}
	out := make([]Workflow, 0, len(seen))
	for w := range seen {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// LoadRecipes reads every *.toml file under <repoRoot>/.pcke/recipes,
// validates each, and returns a RecipeSet that overlays them on the
// built-in profile table (PRD v5.2 §6.2 F15.T5).
//
// A missing recipes directory is not an error: the returned set simply
// exposes the built-in profiles. A malformed or invalid recipe file is
// a hard error, named so the user can fix it.
func LoadRecipes(repoRoot string) (RecipeSet, error) {
	dir := filepath.Join(repoRoot, recipesDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return RecipeSet{}, nil
		}
		return RecipeSet{}, fmt.Errorf("recipes: read dir: %w", err)
	}

	overrides := make(map[Workflow]WorkflowProfile)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		r, err := decodeRecipe(path)
		if err != nil {
			return RecipeSet{}, err
		}
		overrides[Workflow(r.Name)] = r.profile()
	}
	if len(overrides) == 0 {
		return RecipeSet{}, nil
	}
	return RecipeSet{profiles: overrides}, nil
}

// decodeRecipe reads and validates a single recipe file.
func decodeRecipe(path string) (Recipe, error) {
	var r Recipe
	if _, err := toml.DecodeFile(path, &r); err != nil {
		return Recipe{}, fmt.Errorf("recipes: decode %s: %w", filepath.Base(path), err)
	}
	if err := r.validate(); err != nil {
		return Recipe{}, fmt.Errorf("recipes: %s: %w", filepath.Base(path), err)
	}
	return r, nil
}

package retrieval_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jenaiz/pcke/internal/retrieval"
)

// writeRecipe creates <root>/.pcke/recipes/<name> with body.
func writeRecipe(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, ".pcke", "recipes")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write recipe: %v", err)
	}
}

func TestLoadRecipes_MissingDirIsBuiltinOnly(t *testing.T) {
	t.Parallel()
	rs, err := retrieval.LoadRecipes(t.TempDir())
	if err != nil {
		t.Fatalf("LoadRecipes: %v", err)
	}
	// Falls back to built-in profile for a known workflow.
	got := rs.ProfileFor(retrieval.WorkflowReview)
	want := retrieval.ProfileFor(retrieval.WorkflowReview)
	if got.Weights != want.Weights {
		t.Errorf("missing dir should yield built-in profile; got %+v want %+v", got.Weights, want.Weights)
	}
}

func TestLoadRecipes_OverridesBuiltin(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeRecipe(t, root, "review.toml", `
name = "review"
edge_priority = ["decision_link", "imports"]
edge_boost    = 0.25
[weights]
recency   = 0.10
severity  = 0.50
proximity = 0.20
novelty   = 0.20
`)

	rs, err := retrieval.LoadRecipes(root)
	if err != nil {
		t.Fatalf("LoadRecipes: %v", err)
	}
	p := rs.ProfileFor(retrieval.WorkflowReview)
	if p.Weights.Severity != 0.50 {
		t.Errorf("override severity = %v, want 0.50", p.Weights.Severity)
	}
	if p.EdgeBoost != 0.25 {
		t.Errorf("override edge_boost = %v, want 0.25", p.EdgeBoost)
	}
	if !slices.Equal(p.EdgePriority, []string{"decision_link", "imports"}) {
		t.Errorf("override edge_priority = %v", p.EdgePriority)
	}
}

func TestLoadRecipes_CustomWorkflowAndDefaultBoost(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// Custom workflow, edge_priority set but edge_boost omitted -> default.
	writeRecipe(t, root, "audit.toml", `
name = "security-audit"
edge_priority = ["decision_link"]
[weights]
recency   = 0.20
severity  = 0.50
proximity = 0.20
novelty   = 0.10
`)

	rs, err := retrieval.LoadRecipes(root)
	if err != nil {
		t.Fatalf("LoadRecipes: %v", err)
	}
	p := rs.ProfileFor(retrieval.Workflow("security-audit"))
	if p.Workflow != retrieval.Workflow("security-audit") {
		t.Fatalf("custom workflow = %q", p.Workflow)
	}
	if p.EdgeBoost <= 0 {
		t.Errorf("omitted edge_boost with priority set should default > 0, got %v", p.EdgeBoost)
	}

	// The custom workflow should appear in Workflows() alongside built-ins.
	if !slices.Contains(rs.Workflows(), retrieval.Workflow("security-audit")) {
		t.Errorf("Workflows() missing custom recipe: %v", rs.Workflows())
	}
	if !slices.Contains(rs.Workflows(), retrieval.WorkflowBugfix) {
		t.Errorf("Workflows() missing built-in bugfix: %v", rs.Workflows())
	}
}

func TestLoadRecipes_RejectsInvalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{"missing name", `
[weights]
recency = 0.25
severity = 0.25
proximity = 0.25
novelty = 0.25
`},
		{"weights dont sum", `
name = "broken"
[weights]
recency = 0.10
severity = 0.10
proximity = 0.10
novelty = 0.10
`},
		{"edge_boost out of range", `
name = "broken"
edge_boost = 1.5
[weights]
recency = 0.25
severity = 0.25
proximity = 0.25
novelty = 0.25
`},
		{"malformed toml", `name = "x" [[[`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeRecipe(t, root, "bad.toml", tc.body)
			if _, err := retrieval.LoadRecipes(root); err == nil {
				t.Fatalf("expected error for %q, got nil", tc.name)
			}
		})
	}
}

func TestLoadRecipes_IgnoresNonToml(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeRecipe(t, root, "README.md", "not a recipe")
	rs, err := retrieval.LoadRecipes(root)
	if err != nil {
		t.Fatalf("LoadRecipes should ignore non-toml: %v", err)
	}
	// No overrides -> built-in review profile.
	if rs.ProfileFor(retrieval.WorkflowReview).Weights != retrieval.ProfileFor(retrieval.WorkflowReview).Weights {
		t.Errorf("non-toml file should not override built-ins")
	}
}

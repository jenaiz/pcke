package retrieval

import "time"

// Workflow identifies the kind of task an agent is performing. Phase
// 15 (Workflow Awareness) consumes this to tune weights and edge
// priorities. Phase 13 reads it but does not yet vary scoring.
type Workflow string

// Recognised workflow kinds. Empty string is treated as "explore".
const (
	WorkflowExplore  Workflow = "explore"
	WorkflowBugfix   Workflow = "bugfix"
	WorkflowFeature  Workflow = "feature"
	WorkflowReview   Workflow = "review"
	WorkflowRefactor Workflow = "refactor"
	WorkflowTest     Workflow = "test"
)

// Focus narrows the engine's selection criteria. "all" means "do not
// filter by focus"; the others bias selection toward the named slice.
//
// Phase 13 surfaces Focus as informational; per-focus scoring biases
// land in F13.T2 once the MCP tool surface is wired.
type Focus string

// Recognised focus values.
const (
	FocusAll         Focus = "all"
	FocusConstraints Focus = "constraints"
	FocusHistory     Focus = "history"
	FocusPatterns    Focus = "patterns"
	FocusImpact      Focus = "impact"
)

// Request specifies what context the engine should assemble.
//
// Either FilePath (single-file scoping) or ChangedFiles (diff
// scoping) should be set; both empty produces a generic ranking
// based purely on recency + severity.
type Request struct {
	// FilePath is the target file for file-scoped requests
	// (e.g. the file currently open in the agent's context).
	FilePath string

	// ChangedFiles, if non-empty, switches the engine into diff
	// mode: the candidate set is the union of subgraphs around each
	// changed file.
	ChangedFiles []string

	// Workflow tunes weights/edge priorities once Phase 15 lands.
	Workflow Workflow

	// Budget is the approximate-token ceiling for the assembled
	// package (word count × 1.3 per section body, summed). Zero
	// means use the engine default (2000).
	Budget int

	// Focus narrows selection; empty defaults to FocusAll.
	Focus Focus

	// SessionID, if non-empty, pairs with AlreadyServed to apply
	// novelty scoring across an MCP session. The engine itself
	// stays stateless; sessions are tracked one layer up
	// (F13.T6 / Phase 14).
	SessionID string

	// AlreadyServed is the set of refs the engine should treat as
	// "novelty 0" (already in the agent's context).
	AlreadyServed []string
}

// Weights controls the relative contribution of each scoring factor.
// PRD v5.2 §4.3 recommends DefaultWeights(); call sites that need to
// emphasise constraints over recency (e.g. compliance sweeps) can
// override.
//
// All fields should be in [0, 1] and sum to 1.0; the engine does not
// normalise. Sums outside [0.95, 1.05] are flagged as a warning on
// the returned ContextPackage.
type Weights struct {
	Recency   float64
	Severity  float64
	Proximity float64
	Novelty   float64
}

// DefaultWeights returns the PRD-recommended weight set.
func DefaultWeights() Weights {
	return Weights{
		Recency:   0.25,
		Severity:  0.35,
		Proximity: 0.25,
		Novelty:   0.15,
	}
}

// Sum returns the total weight (used to detect mis-configured calls).
func (w Weights) Sum() float64 {
	return w.Recency + w.Severity + w.Proximity + w.Novelty
}

// Section is one ranked entry in a ContextPackage. Body is the text
// payload that counts against the budget; Tokens is the
// pre-computed approximate-token cost (word_count × 1.3 rounded up).
type Section struct {
	// Ref is the typed reference of the source event (e.g.
	// "e:internal/kdb/db.go" or "d:adr-0008-context-graph-pivot").
	Ref string
	// Kind is the lowercase event-kind name ("entity" | "decision" | "link").
	Kind string
	// Title is a short label suitable for an agent's section header.
	Title string
	// Body is the section payload — the text the budget is computed against.
	Body string
	// Score is the final weighted Score value the engine assigned, in [0, 1].
	Score float64
	// Tokens is the approximate-token cost of Body (word_count × 1.3 ceil).
	Tokens int
	// CreatedAt is the source event's Header().CreatedAt; surfaced so
	// downstream consumers can sort by recency without re-fetching.
	CreatedAt time.Time
}

// ContextPackage is the engine's output: a budget-bounded slice of
// Sections plus metadata about the assembly.
type ContextPackage struct {
	// Sections are sorted by descending Score.
	Sections []Section
	// TokensUsed is the sum of Section.Tokens (≤ effective budget).
	TokensUsed int
	// BudgetLimit is the effective budget the engine enforced (after
	// applying the engine's default if the request omitted it).
	BudgetLimit int
	// Warnings is informational diagnostics: weights mis-summed,
	// candidate set was empty, etc. Never an error condition.
	Warnings []string
	// Truncated is true when at least one candidate was excluded
	// because adding it would have crossed BudgetLimit.
	Truncated bool
}

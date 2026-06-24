package retrieval

// WorkflowProfile bundles the scoring knobs that a detected workflow
// tunes (PRD v5.2 §6.2 F15.T2): the four-factor Weights plus an ordered
// list of edge types whose 1-hop neighbours get a proximity boost.
//
// The profile is the single source of truth for "how does this workflow
// change retrieval". The engine applies it via WithWorkflow.
type WorkflowProfile struct {
	// Workflow is the profile's key.
	Workflow Workflow
	// Weights tunes the recency/severity/proximity/novelty blend.
	Weights Weights
	// EdgePriority lists edge types (e.g. "decision_link", "imports")
	// whose direct neighbours of the request's files are considered
	// especially relevant for this workflow. Empty means no edge bias.
	EdgePriority []string
	// EdgeBoost is the additive score bonus applied to sections reached
	// via a priority edge, clamped so the final score stays in [0, 1].
	EdgeBoost float64
}

// edgeBoostDefault is the standard bonus for priority-edge neighbours.
// Small enough not to swamp the weighted score, large enough to
// reorder near-ties toward the workflow's preferred edges.
const edgeBoostDefault = 0.15

// workflowProfiles is the static profile table. WorkflowExplore is the
// neutral baseline (DefaultWeights, no edge bias); every other workflow
// shifts emphasis per PRD v5.2 §6.2:
//
//   - bugfix:   severity + proximity (what must hold near the fix)
//   - feature:  proximity + novelty (learn the new area)
//   - review:   severity + novelty, prioritise decision_link
//   - refactor: proximity, prioritise imports (dependency blast radius)
//   - test:     proximity + recency (what changed and where)
var workflowProfiles = map[Workflow]WorkflowProfile{
	WorkflowBugfix: {
		Workflow:     WorkflowBugfix,
		Weights:      Weights{Recency: 0.20, Severity: 0.40, Proximity: 0.30, Novelty: 0.10},
		EdgePriority: []string{"decision_link", "imports"},
		EdgeBoost:    edgeBoostDefault,
	},
	WorkflowFeature: {
		Workflow:     WorkflowFeature,
		Weights:      Weights{Recency: 0.20, Severity: 0.25, Proximity: 0.30, Novelty: 0.25},
		EdgePriority: []string{"imports", "decision_link"},
		EdgeBoost:    edgeBoostDefault,
	},
	WorkflowReview: {
		Workflow:     WorkflowReview,
		Weights:      Weights{Recency: 0.15, Severity: 0.40, Proximity: 0.20, Novelty: 0.25},
		EdgePriority: []string{"decision_link"},
		EdgeBoost:    edgeBoostDefault,
	},
	WorkflowRefactor: {
		Workflow:     WorkflowRefactor,
		Weights:      Weights{Recency: 0.15, Severity: 0.30, Proximity: 0.40, Novelty: 0.15},
		EdgePriority: []string{"imports"},
		EdgeBoost:    edgeBoostDefault,
	},
	WorkflowTest: {
		Workflow:     WorkflowTest,
		Weights:      Weights{Recency: 0.30, Severity: 0.20, Proximity: 0.35, Novelty: 0.15},
		EdgePriority: []string{"imports"},
		EdgeBoost:    edgeBoostDefault,
	},
	WorkflowExplore: {
		Workflow:     WorkflowExplore,
		Weights:      DefaultWeights(),
		EdgePriority: nil,
		EdgeBoost:    0,
	},
}

// ProfileFor returns the WorkflowProfile for w. Unknown or empty
// workflows resolve to the WorkflowExplore baseline so callers always
// get a usable, neutral profile.
func ProfileFor(w Workflow) WorkflowProfile {
	if p, ok := workflowProfiles[w]; ok {
		return p
	}
	return workflowProfiles[WorkflowExplore]
}

// WeightsForWorkflow is a convenience accessor for ProfileFor(w).Weights.
func WeightsForWorkflow(w Workflow) Weights {
	return ProfileFor(w).Weights
}

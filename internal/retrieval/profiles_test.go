package retrieval

import (
	"math"
	"testing"
)

func TestProfileFor_AllWorkflowsSumToOne(t *testing.T) {
	workflows := []Workflow{
		WorkflowExplore, WorkflowBugfix, WorkflowFeature,
		WorkflowReview, WorkflowRefactor, WorkflowTest,
	}
	for _, w := range workflows {
		p := ProfileFor(w)
		if p.Workflow != w {
			t.Errorf("ProfileFor(%q).Workflow = %q", w, p.Workflow)
		}
		if sum := p.Weights.Sum(); math.Abs(sum-1.0) > 1e-9 {
			t.Errorf("%q weights sum = %v, want 1.0", w, sum)
		}
	}
}

func TestProfileFor_UnknownFallsBackToExplore(t *testing.T) {
	p := ProfileFor(Workflow("nonsense"))
	if p.Workflow != WorkflowExplore {
		t.Fatalf("unknown workflow profile = %q, want explore", p.Workflow)
	}
	if len(p.EdgePriority) != 0 || p.EdgeBoost != 0 {
		t.Fatalf("explore profile should have no edge bias, got %+v", p)
	}
}

func TestProfileFor_EdgePriorities(t *testing.T) {
	tests := []struct {
		workflow Workflow
		wantHead string
	}{
		{WorkflowReview, "decision_link"},
		{WorkflowRefactor, "imports"},
		{WorkflowTest, "imports"},
		{WorkflowBugfix, "decision_link"},
		{WorkflowFeature, "imports"},
	}
	for _, tc := range tests {
		p := ProfileFor(tc.workflow)
		if len(p.EdgePriority) == 0 {
			t.Errorf("%q has no edge priority, want %q first", tc.workflow, tc.wantHead)
			continue
		}
		if p.EdgePriority[0] != tc.wantHead {
			t.Errorf("%q edge priority head = %q, want %q",
				tc.workflow, p.EdgePriority[0], tc.wantHead)
		}
		if p.EdgeBoost <= 0 {
			t.Errorf("%q edge boost = %v, want > 0", tc.workflow, p.EdgeBoost)
		}
	}
}

func TestWeightsForWorkflow_DiffersFromDefault(t *testing.T) {
	// Refactor should emphasise proximity more than the default blend.
	if got := WeightsForWorkflow(WorkflowRefactor).Proximity; got <= DefaultWeights().Proximity {
		t.Errorf("refactor proximity = %v, want > default %v",
			got, DefaultWeights().Proximity)
	}
	// Review should emphasise severity more than the default blend.
	if got := WeightsForWorkflow(WorkflowReview).Severity; got <= DefaultWeights().Severity {
		t.Errorf("review severity = %v, want > default %v",
			got, DefaultWeights().Severity)
	}
}

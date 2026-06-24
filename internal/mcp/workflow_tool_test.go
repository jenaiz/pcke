package mcp

import (
	"testing"

	"github.com/jenaiz/pcke/internal/retrieval"
)

func TestParseWorkflow(t *testing.T) {
	tests := []struct {
		in     string
		want   retrieval.Workflow
		wantOK bool
	}{
		{"bugfix", retrieval.WorkflowBugfix, true},
		{"FEATURE", retrieval.WorkflowFeature, true},
		{"  review  ", retrieval.WorkflowReview, true},
		{"refactor", retrieval.WorkflowRefactor, true},
		{"test", retrieval.WorkflowTest, true},
		{"explore", retrieval.WorkflowExplore, true},
		{"", retrieval.WorkflowExplore, true},
		{"nonsense", "", false},
	}
	for _, tc := range tests {
		got, ok := parseWorkflow(tc.in)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("parseWorkflow(%q) = (%q, %v), want (%q, %v)",
				tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestSessionWorkflowStore(t *testing.T) {
	s := &Server{workflows: make(map[string]retrieval.Workflow)}

	// Unset session -> empty.
	if got := s.sessionWorkflow("alpha"); got != "" {
		t.Errorf("unset sessionWorkflow = %q, want empty", got)
	}
	// Empty session id is a no-op on set and read.
	s.setSessionWorkflow("", retrieval.WorkflowBugfix)
	if got := s.sessionWorkflow(""); got != "" {
		t.Errorf("empty id sessionWorkflow = %q, want empty", got)
	}

	s.setSessionWorkflow("alpha", retrieval.WorkflowReview)
	if got := s.sessionWorkflow("alpha"); got != retrieval.WorkflowReview {
		t.Errorf("sessionWorkflow(alpha) = %q, want review", got)
	}
}

func TestResolveWorkflow(t *testing.T) {
	s := &Server{workflows: make(map[string]retrieval.Workflow)}
	s.setSessionWorkflow("alpha", retrieval.WorkflowReview)

	tests := []struct {
		name      string
		sessionID string
		param     string
		want      retrieval.Workflow
	}{
		{"explicit param wins", "alpha", "refactor", retrieval.WorkflowRefactor},
		{"empty param inherits session", "alpha", "", retrieval.WorkflowReview},
		{"explicit explore overrides session", "alpha", "explore", retrieval.WorkflowExplore},
		{"no session, empty param", "beta", "", ""},
		{"unknown param falls back to explore", "beta", "garbage", retrieval.WorkflowExplore},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.resolveWorkflow(tc.sessionID, tc.param); got != tc.want {
				t.Errorf("resolveWorkflow(%q, %q) = %q, want %q",
					tc.sessionID, tc.param, got, tc.want)
			}
		})
	}
}

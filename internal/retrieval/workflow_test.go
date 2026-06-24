package retrieval

import "testing"

func TestDetectWorkflow_BranchPrefixes(t *testing.T) {
	tests := []struct {
		name       string
		branch     string
		wantWf     Workflow
		wantConfGE Confidence
	}{
		{"fix prefix", "fix/memory-leak", WorkflowBugfix, 0.9},
		{"bug prefix", "bug/123", WorkflowBugfix, 0.9},
		{"bugfix prefix", "bugfix/race", WorkflowBugfix, 0.9},
		{"hotfix prefix", "hotfix/prod", WorkflowBugfix, 0.9},
		{"feat prefix", "feat/graph", WorkflowFeature, 0.9},
		{"feature prefix", "feature/recipes", WorkflowFeature, 0.9},
		{"add prefix", "add-onboarding", WorkflowFeature, 0.9},
		{"refactor prefix", "refactor/engine", WorkflowRefactor, 0.9},
		{"cleanup prefix", "cleanup/dead-code", WorkflowRefactor, 0.9},
		{"chore prefix", "chore/deps", WorkflowRefactor, 0.9},
		{"test prefix", "test/coverage", WorkflowTest, 0.85},
		{"testing prefix", "testing/flaky", WorkflowTest, 0.85},
		{"case-insensitive", "FIX/UPPER", WorkflowBugfix, 0.9},
		{"whitespace trimmed", "  feat/spaced  ", WorkflowFeature, 0.9},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotWf, gotConf := DetectWorkflow(DetectionContext{BranchName: tc.branch})
			if gotWf != tc.wantWf {
				t.Errorf("workflow = %q, want %q", gotWf, tc.wantWf)
			}
			if gotConf < tc.wantConfGE {
				t.Errorf("confidence = %v, want >= %v", gotConf, tc.wantConfGE)
			}
		})
	}
}

func TestDetectWorkflow_TestFileRatio(t *testing.T) {
	dc := DetectionContext{
		BranchName: "wip", // no prefix match
		ChangedFiles: []string{
			"internal/retrieval/score_test.go",
			"internal/retrieval/workflow_test.go",
			"internal/retrieval/session/session_test.go",
			"internal/retrieval/engine.go", // 1 of 4 non-test -> 75% test
		},
	}
	// 3/4 = 0.75 which is NOT > 0.8, so this should NOT be test.
	if wf, _ := DetectWorkflow(dc); wf == WorkflowTest {
		t.Fatalf("75%% test files should not detect as test, got %q", wf)
	}

	dc.ChangedFiles = []string{
		"a_test.go", "b_test.go", "c_test.go", "d_test.go", "e.go",
	}
	// 4/5 = 0.8, not > 0.8 -> still not test.
	if wf, _ := DetectWorkflow(dc); wf == WorkflowTest {
		t.Fatalf("exactly 80%% should not detect as test, got %q", wf)
	}

	dc.ChangedFiles = []string{
		"a_test.go", "b_test.go", "c_test.go", "d_test.go", "e_test.go",
	}
	// 5/5 = 1.0 > 0.8 -> test.
	if wf, conf := DetectWorkflow(dc); wf != WorkflowTest || conf < 0.8 {
		t.Fatalf("all test files = %q (%v), want test >= 0.8", wf, conf)
	}
}

func TestDetectWorkflow_TestDirAndPyJs(t *testing.T) {
	dc := DetectionContext{
		BranchName: "wip",
		ChangedFiles: []string{
			"tests/unit/foo.py",
			"test/integration/bar.js",
			"app/test_models.py",
		},
	}
	if wf, _ := DetectWorkflow(dc); wf != WorkflowTest {
		t.Fatalf("test-dir / test_ files should detect as test, got %q", wf)
	}
}

func TestDetectWorkflow_ReviewFromSession(t *testing.T) {
	dc := DetectionContext{
		BranchName:   "main",
		IsMainBranch: true,
		ChangedFiles: nil,
		SessionFiles: []string{"e:a.go", "e:b.go", "e:c.go"},
	}
	if wf, conf := DetectWorkflow(dc); wf != WorkflowReview || conf < 0.7 {
		t.Fatalf("3 read files, 0 changed = %q (%v), want review >= 0.7", wf, conf)
	}

	// Fewer than 3 session files -> not review; falls through to explore.
	dc.SessionFiles = []string{"e:a.go", "e:b.go"}
	if wf, _ := DetectWorkflow(dc); wf == WorkflowReview {
		t.Fatalf("2 read files should not detect review, got %q", wf)
	}
}

func TestDetectWorkflow_ExploreOnTrunk(t *testing.T) {
	dc := DetectionContext{
		BranchName:     "main",
		IsMainBranch:   true,
		HasUncommitted: false,
	}
	wf, conf := DetectWorkflow(dc)
	if wf != WorkflowExplore || conf < 0.6 {
		t.Fatalf("clean trunk = %q (%v), want explore >= 0.6", wf, conf)
	}
}

func TestDetectWorkflow_DefaultLowConfidence(t *testing.T) {
	wf, conf := DetectWorkflow(DetectionContext{})
	if wf != WorkflowExplore {
		t.Fatalf("empty context = %q, want explore", wf)
	}
	if conf.Level() != "low" {
		t.Fatalf("empty context confidence level = %q, want low", conf.Level())
	}
}

func TestConfidence_Level(t *testing.T) {
	tests := []struct {
		conf Confidence
		want string
	}{
		{0.95, "high"},
		{0.8, "high"},
		{0.7, "medium"},
		{0.5, "medium"},
		{0.49, "low"},
		{0.0, "low"},
	}
	for _, tc := range tests {
		if got := tc.conf.Level(); got != tc.want {
			t.Errorf("Confidence(%v).Level() = %q, want %q", tc.conf, got, tc.want)
		}
	}
}

// TestDetectWorkflow_TenScenarios is the F15.T1 acceptance gate:
// detection must be correct on >=80% of 10 simulated scenarios.
func TestDetectWorkflow_TenScenarios(t *testing.T) {
	scenarios := []struct {
		name string
		dc   DetectionContext
		want Workflow
	}{
		{
			name: "1 bugfix branch with changes",
			dc:   DetectionContext{BranchName: "fix/null-deref", HasUncommitted: true, ChangedFiles: []string{"db.go"}},
			want: WorkflowBugfix,
		},
		{
			name: "2 feature branch new files",
			dc:   DetectionContext{BranchName: "feature/recipes", HasUncommitted: true, ChangedFiles: []string{"recipes.go"}},
			want: WorkflowFeature,
		},
		{
			name: "3 refactor branch",
			dc:   DetectionContext{BranchName: "refactor/engine-split", HasUncommitted: true, ChangedFiles: []string{"engine.go", "score.go"}},
			want: WorkflowRefactor,
		},
		{
			name: "4 explicit test branch",
			dc:   DetectionContext{BranchName: "test/add-coverage", HasUncommitted: true},
			want: WorkflowTest,
		},
		{
			name: "5 test-heavy diff on generic branch",
			dc:   DetectionContext{BranchName: "wip", ChangedFiles: []string{"a_test.go", "b_test.go", "c_test.go"}},
			want: WorkflowTest,
		},
		{
			name: "6 reviewing on main, many reads no edits",
			dc:   DetectionContext{BranchName: "main", IsMainBranch: true, SessionFiles: []string{"e:x.go", "e:y.go", "e:z.go", "e:w.go"}},
			want: WorkflowReview,
		},
		{
			name: "7 clean trunk exploration",
			dc:   DetectionContext{BranchName: "master", IsMainBranch: true},
			want: WorkflowExplore,
		},
		{
			name: "8 hotfix urgent",
			dc:   DetectionContext{BranchName: "hotfix/prod-down", HasUncommitted: true},
			want: WorkflowBugfix,
		},
		{
			name: "9 chore dependency bump",
			dc:   DetectionContext{BranchName: "chore/bump-go-git", HasUncommitted: true},
			want: WorkflowRefactor,
		},
		{
			name: "10 add- prefixed feature",
			dc:   DetectionContext{BranchName: "add-federation-router", HasUncommitted: true},
			want: WorkflowFeature,
		},
	}

	correct := 0
	for _, s := range scenarios {
		got, _ := DetectWorkflow(s.dc)
		if got == s.want {
			correct++
		} else {
			t.Logf("scenario %q: got %q, want %q", s.name, got, s.want)
		}
	}
	accuracy := float64(correct) / float64(len(scenarios))
	if accuracy < 0.8 {
		t.Fatalf("detection accuracy %.0f%% (%d/%d), want >= 80%%",
			accuracy*100, correct, len(scenarios))
	}
}

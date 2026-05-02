package onboard

import (
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/analysis"
)

func TestScoreModules_AllModulesScored(t *testing.T) {
	nodes := makeTestNodes()
	scores := ScoreModules(nodes, nil, nil)

	// We have multiple modules in the test data.
	if len(scores) == 0 {
		t.Fatal("expected scores for modules")
	}

	modules := map[string]bool{
		"cmd/pcke":          true,
		"internal/kdb":      true,
		"internal/kdb/page": true,
		"internal/analysis": true,
		"internal/mcp":      true,
		"internal/output":   true,
		"internal/config":   true,
		"(root)":            true,
	}

	for mod := range modules {
		if _, ok := scores[mod]; !ok {
			t.Errorf("module %q not scored", mod)
		}
	}
}

func TestScoreModules_NormalizedRange(t *testing.T) {
	nodes := makeTestNodes()
	scores := ScoreModules(nodes, nil, nil)

	for mod, score := range scores {
		if score < 0 || score > 1 {
			t.Errorf("module %q score %.4f out of [0, 1] range", mod, score)
		}
	}
}

func TestScoreModules_FanInAffectsScore(t *testing.T) {
	nodes := makeTestNodes()

	// Create relations where many modules import internal/kdb.
	rels := []analysis.Relation{
		{Type: "imports", SourceNodeID: "cmd/pcke/main.go", TargetNodeID: "internal/kdb/db.go"},
		{Type: "imports", SourceNodeID: "internal/analysis/scanner.go", TargetNodeID: "internal/kdb/db.go"},
		{Type: "imports", SourceNodeID: "internal/mcp/server.go", TargetNodeID: "internal/kdb/db.go"},
		{Type: "imports", SourceNodeID: "internal/output/render.go", TargetNodeID: "internal/kdb/db.go"},
	}

	scores := ScoreModules(nodes, rels, nil)

	// internal/kdb should have higher complexity due to high fan-in.
	kdbScore := scores["internal/kdb"]
	configScore := scores["internal/config"]

	if kdbScore <= configScore {
		t.Errorf("internal/kdb (%.4f) should score higher than internal/config (%.4f) due to fan-in",
			kdbScore, configScore)
	}
}

func TestScoreModules_ChurnAffectsScore(t *testing.T) {
	nodes := makeTestNodes()
	now := time.Now()

	// Heavy churn in internal/mcp.
	var logs []analysis.EvolutionLog
	for i := 0; i < 20; i++ {
		logs = append(logs, analysis.EvolutionLog{
			NodeID:    "internal/mcp/server.go",
			Timestamp: now.AddDate(0, 0, -10),
		})
	}

	scoresWithChurn := ScoreModules(nodes, nil, logs)
	scoresNoChurn := ScoreModules(nodes, nil, nil)

	mcpWithChurn := scoresWithChurn["internal/mcp"]
	mcpNoChurn := scoresNoChurn["internal/mcp"]

	if mcpWithChurn <= mcpNoChurn {
		t.Errorf("internal/mcp with churn (%.4f) should score >= without churn (%.4f)",
			mcpWithChurn, mcpNoChurn)
	}
}

func TestScoreModules_Empty(t *testing.T) {
	scores := ScoreModules(nil, nil, nil)
	if len(scores) != 0 {
		t.Errorf("expected empty scores for nil nodes, got %d", len(scores))
	}
}

func TestModulesByComplexity_Ordering(t *testing.T) {
	scores := map[string]float64{
		"a": 0.3,
		"b": 0.8,
		"c": 0.5,
	}

	names := ModulesByComplexity(scores)
	if len(names) != 3 {
		t.Fatalf("len = %d, want 3", len(names))
	}
	if names[0] != "b" {
		t.Errorf("first = %q, want %q", names[0], "b")
	}
	if names[2] != "a" {
		t.Errorf("last = %q, want %q", names[2], "a")
	}
}

package onboard

import (
	"sort"
	"time"

	"github.com/jenaiz/pcke/internal/analysis"
	"github.com/jenaiz/pcke/internal/output"
)

// minMax holds min/max values for normalization.
type minMax struct {
	min, max float64
}

// ScoreModules computes a composite complexity score for each module.
//
// Formula:
//
//	Score(module) = 0.30 × fan_in_norm
//	             + 0.25 × (1 - avg_stability)
//	             + 0.20 × churn_rate_norm
//	             + 0.15 × file_count_norm
//	             + 0.10 × entity_density_norm
//
// All metrics are normalized to [0, 1] via min-max across the module set.
// If entity density is unavailable (no deep scan), its weight is
// redistributed proportionally.
func ScoreModules(
	nodes []analysis.KnowledgeNode,
	relations []analysis.Relation,
	evolLogs []analysis.EvolutionLog,
) map[string]float64 {
	modules := output.GroupByModule(nodes)
	if len(modules) == 0 {
		return map[string]float64{}
	}

	fileToModule := buildFileToModuleMap(nodes)
	fanIn := computeFanIn(modules, relations, fileToModule)
	churn := computeChurn(nodes, evolLogs)
	raw, anyEntities := computeRawMetrics(modules, fanIn, churn)

	return computeScores(raw, anyEntities)
}

// buildFileToModuleMap maps file paths to their module names.
func buildFileToModuleMap(nodes []analysis.KnowledgeNode) map[string]string {
	m := make(map[string]string, len(nodes))
	for _, n := range nodes {
		m[n.FilePath] = n.Module
	}
	return m
}

// computeFanIn counts distinct modules importing each module.
func computeFanIn(
	modules map[string][]analysis.KnowledgeNode,
	relations []analysis.Relation,
	fileToModule map[string]string,
) map[string]int {
	fanIn := make(map[string]int, len(modules))
	importers := make(map[string]map[string]bool, len(modules))
	for mod := range modules {
		importers[mod] = make(map[string]bool)
	}
	for _, rel := range relations {
		if rel.Type != "imports" {
			continue
		}
		srcMod := fileToModule[rel.SourceNodeID]
		tgtMod := fileToModule[rel.TargetNodeID]
		if srcMod != "" && tgtMod != "" && srcMod != tgtMod {
			if _, ok := importers[tgtMod]; ok {
				importers[tgtMod][srcMod] = true
			}
		}
	}
	for mod, imp := range importers {
		fanIn[mod] = len(imp)
	}
	return fanIn
}

// computeChurn counts evolution log entries per module in a 90-day window.
func computeChurn(nodes []analysis.KnowledgeNode, evolLogs []analysis.EvolutionLog) map[string]int {
	cutoff := time.Now().AddDate(0, 0, -90)
	churn := make(map[string]int)
	nodeToModule := make(map[string]string, len(nodes)*3)
	for _, n := range nodes {
		nodeToModule["kn:"+n.FilePath] = n.Module
		nodeToModule[n.ID] = n.Module
		nodeToModule[n.FilePath] = n.Module
	}
	for _, log := range evolLogs {
		if log.Timestamp.Before(cutoff) {
			continue
		}
		mod := nodeToModule[log.NodeID]
		if mod != "" {
			churn[mod]++
		}
	}
	return churn
}

// moduleMetrics holds raw per-module complexity metrics.
type moduleMetrics struct {
	fanIn         float64
	instability   float64
	churnRate     float64
	fileCount     float64
	entityDensity float64
}

// computeRawMetrics gathers unscaled metrics for each module.
func computeRawMetrics(
	modules map[string][]analysis.KnowledgeNode,
	fanIn map[string]int,
	churn map[string]int,
) (map[string]moduleMetrics, bool) {
	raw := make(map[string]moduleMetrics, len(modules))
	anyEntities := false

	for mod, modNodes := range modules {
		var totalStability float64
		var totalEntities int
		for _, n := range modNodes {
			totalStability += n.Stability
			totalEntities += len(n.Entities)
		}
		fc := float64(len(modNodes))
		avgStab := float64(0)
		if fc > 0 {
			avgStab = totalStability / fc
		}
		ed := float64(0)
		if totalEntities > 0 {
			ed = float64(totalEntities) / fc
			anyEntities = true
		}

		raw[mod] = moduleMetrics{
			fanIn:         float64(fanIn[mod]),
			instability:   1 - avgStab,
			churnRate:     float64(churn[mod]),
			fileCount:     fc,
			entityDensity: ed,
		}
	}
	return raw, anyEntities
}

// computeScores normalizes and weights raw metrics into final scores.
func computeScores(raw map[string]moduleMetrics, anyEntities bool) map[string]float64 {
	fanInMM := minMax{min: 1e18, max: -1e18}
	churnMM := minMax{min: 1e18, max: -1e18}
	fileMM := minMax{min: 1e18, max: -1e18}
	entityMM := minMax{min: 1e18, max: -1e18}

	for _, m := range raw {
		fanInMM = updateMM(fanInMM, m.fanIn)
		churnMM = updateMM(churnMM, m.churnRate)
		fileMM = updateMM(fileMM, m.fileCount)
		if anyEntities {
			entityMM = updateMM(entityMM, m.entityDensity)
		}
	}

	wFanIn := 0.30
	wInstab := 0.25
	wChurn := 0.20
	wFile := 0.15
	wEntity := 0.10

	if !anyEntities {
		total := wFanIn + wInstab + wChurn + wFile
		wFanIn /= total
		wInstab /= total
		wChurn /= total
		wFile /= total
		wEntity = 0
	}

	scores := make(map[string]float64, len(raw))
	for mod, m := range raw {
		score := wFanIn*normalize(m.fanIn, fanInMM) +
			wInstab*m.instability +
			wChurn*normalize(m.churnRate, churnMM) +
			wFile*normalize(m.fileCount, fileMM) +
			wEntity*normalize(m.entityDensity, entityMM)
		scores[mod] = score
	}

	return scores
}

// ModulesByComplexity returns module names sorted by their complexity score
// (highest first).
func ModulesByComplexity(scores map[string]float64) []string {
	type entry struct {
		name  string
		score float64
	}
	entries := make([]entry, 0, len(scores))
	for name, score := range scores {
		entries = append(entries, entry{name, score})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].score > entries[j].score
	})
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.name
	}
	return names
}

func normalize(val float64, mm minMax) float64 {
	if mm.max == mm.min {
		return 0
	}
	return (val - mm.min) / (mm.max - mm.min)
}

func updateMM(mm minMax, val float64) minMax {
	if val < mm.min {
		mm.min = val
	}
	if val > mm.max {
		mm.max = val
	}
	return mm
}

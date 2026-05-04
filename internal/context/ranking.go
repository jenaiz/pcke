package context

import (
	"math"
	"time"
)

// Weights controls how different scoring factors contribute to the final
// ranking score for a context item.
type Weights struct {
	Recency   float64 // Weight for time-based relevance (default 0.25)
	Severity  float64 // Weight for constraint severity (default 0.35)
	Proximity float64 // Weight for scope proximity (default 0.25)
	Novelty   float64 // Weight for session novelty (default 0.15)
}

// DefaultWeights returns the default scoring weights.
func DefaultWeights() Weights {
	return Weights{
		Recency:   0.25,
		Severity:  0.35,
		Proximity: 0.25,
		Novelty:   0.15,
	}
}

// RecencyScore computes a normalized recency score in [0, 1].
// Items updated within the last day score 1.0; items older than 30 days score 0.0.
func RecencyScore(updatedAt time.Time) float64 {
	days := time.Since(updatedAt).Hours() / 24
	score := 1.0 - math.Min(days/30.0, 1.0)
	if score < 0 {
		return 0
	}
	return score
}

// SeverityScore maps a severity string to a normalized score in [0, 1].
func SeverityScore(severity string) float64 {
	switch severity {
	case "must":
		return 1.0
	case "should":
		return 0.6
	case "may":
		return 0.3
	default:
		return 0.3
	}
}

// ProximityScore maps a scope relationship to the target file/module.
// "file" means the item directly applies to the target file.
// "module" means it applies to the same module.
// "global" means it applies project-wide.
func ProximityScore(scope string) float64 {
	switch scope {
	case "file":
		return 1.0
	case "module":
		return 0.5
	case "global":
		return 0.2
	default:
		return 0.2
	}
}

// ComputeScore calculates the weighted ranking score for an item.
func ComputeScore(w Weights, recency, severity, proximity, novelty float64) float64 {
	return w.Recency*recency +
		w.Severity*severity +
		w.Proximity*proximity +
		w.Novelty*novelty
}

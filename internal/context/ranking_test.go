package context

import (
	"testing"
	"time"
)

func TestRecencyScore_Recent(t *testing.T) {
	score := RecencyScore(time.Now().Add(-1 * time.Hour))
	if score < 0.99 {
		t.Fatalf("expected ~1.0 for 1-hour-old item, got %f", score)
	}
}

func TestRecencyScore_Old(t *testing.T) {
	score := RecencyScore(time.Now().Add(-31 * 24 * time.Hour))
	if score != 0.0 {
		t.Fatalf("expected 0.0 for 31-day-old item, got %f", score)
	}
}

func TestRecencyScore_HalfLife(t *testing.T) {
	score := RecencyScore(time.Now().Add(-15 * 24 * time.Hour))
	if score < 0.45 || score > 0.55 {
		t.Fatalf("expected ~0.5 for 15-day-old item, got %f", score)
	}
}

func TestSeverityScore(t *testing.T) {
	tests := []struct {
		severity string
		expected float64
	}{
		{"must", 1.0},
		{"should", 0.6},
		{"may", 0.3},
		{"unknown", 0.3},
	}
	for _, tt := range tests {
		score := SeverityScore(tt.severity)
		if score != tt.expected {
			t.Errorf("SeverityScore(%q) = %f, want %f", tt.severity, score, tt.expected)
		}
	}
}

func TestProximityScore(t *testing.T) {
	tests := []struct {
		scope    string
		expected float64
	}{
		{"file", 1.0},
		{"module", 0.5},
		{"global", 0.2},
		{"other", 0.2},
	}
	for _, tt := range tests {
		score := ProximityScore(tt.scope)
		if score != tt.expected {
			t.Errorf("ProximityScore(%q) = %f, want %f", tt.scope, score, tt.expected)
		}
	}
}

func TestComputeScore(t *testing.T) {
	w := DefaultWeights()
	score := ComputeScore(w, 1.0, 1.0, 1.0, 1.0)
	expected := w.Recency + w.Severity + w.Proximity + w.Novelty
	if score != expected {
		t.Fatalf("expected %f, got %f", expected, score)
	}
}

func TestComputeScore_Zero(t *testing.T) {
	w := DefaultWeights()
	score := ComputeScore(w, 0, 0, 0, 0)
	if score != 0.0 {
		t.Fatalf("expected 0.0, got %f", score)
	}
}

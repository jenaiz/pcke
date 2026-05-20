package main

import (
	"context"
	"testing"
	"time"
)

func TestStats_CountsAcrossSeededSessions(t *testing.T) {
	dir := t.TempDir()
	seedSessions(t, dir)
	withWorkingDir(t, dir)

	db, store, closeFn, err := openSessionDB()
	if err != nil {
		t.Fatalf("openSessionDB: %v", err)
	}
	defer closeFn()

	report, err := collectStats(context.Background(), db, store, time.Time{}, 5)
	if err != nil {
		t.Fatalf("collectStats: %v", err)
	}
	if report.Sessions == 0 {
		t.Errorf("Sessions = 0, want > 0")
	}
	if report.ToolCalls == 0 {
		t.Errorf("ToolCalls = 0, want > 0")
	}
	// seedSessions wrote e:internal/kdb/db.go, e:internal/log/logger.go, and d:adr-0008.
	if report.Files == 0 || report.Decisions == 0 {
		t.Errorf("Files=%d Decisions=%d, want both > 0", report.Files, report.Decisions)
	}
}

func TestStats_SinceTrimsOldSessions(t *testing.T) {
	dir := t.TempDir()
	seedSessions(t, dir)
	withWorkingDir(t, dir)

	db, store, closeFn, err := openSessionDB()
	if err != nil {
		t.Fatalf("openSessionDB: %v", err)
	}
	defer closeFn()

	// seedSessions backdates one session to ~60 days ago; a 7-day window
	// should exclude it from the session count.
	cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour)
	report, err := collectStats(context.Background(), db, store, cutoff, 5)
	if err != nil {
		t.Fatalf("collectStats: %v", err)
	}
	if report.Sessions != 1 {
		t.Errorf("Sessions in 7d window = %d, want 1", report.Sessions)
	}
}

func TestStats_TopByToolIsRanked(t *testing.T) {
	dir := t.TempDir()
	seedSessions(t, dir)
	withWorkingDir(t, dir)

	db, store, closeFn, err := openSessionDB()
	if err != nil {
		t.Fatalf("openSessionDB: %v", err)
	}
	defer closeFn()

	report, err := collectStats(context.Background(), db, store, time.Time{}, 5)
	if err != nil {
		t.Fatalf("collectStats: %v", err)
	}
	if len(report.ByTool) == 0 {
		t.Fatalf("ByTool is empty, want at least one tool")
	}
	// Both seeded calls use the "recall" tool.
	if report.ByTool[0].Name != "recall" {
		t.Errorf("ByTool[0] = %q, want \"recall\"", report.ByTool[0].Name)
	}
}

func TestTopCounts_RanksAndCaps(t *testing.T) {
	t.Parallel()
	in := map[string]int{
		"alpha":   3,
		"beta":    5,
		"gamma":   5,
		"delta":   1,
		"epsilon": 2,
	}
	got := topCounts(in, 3)
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3 (cap)", len(got))
	}
	// Tie at 5: alphabetical → beta then gamma; then alpha at 3.
	want := []string{"beta", "gamma", "alpha"}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("got[%d].Name = %q, want %q", i, got[i].Name, w)
		}
	}
}

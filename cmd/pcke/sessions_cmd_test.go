package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/observe"
	"github.com/jenaiz/pcke/internal/retrieval/session"
)

func TestParseHumanDuration(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		want    time.Duration
		wantErr bool
	}{
		"":     {0, true},
		"7d":   {7 * 24 * time.Hour, false},
		"30D":  {30 * 24 * time.Hour, false},
		"1w":   {7 * 24 * time.Hour, false},
		"2W":   {14 * 24 * time.Hour, false},
		"24h":  {24 * time.Hour, false},
		"1m":   {time.Minute, false},
		"junk": {0, true},
	}
	for in, tc := range cases {
		in, tc := in, tc
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			got, err := parseHumanDuration(in)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseHumanDuration(%q): want error, got nil", in)
				}
				return
			}
			if err != nil {
				t.Errorf("parseHumanDuration(%q): unexpected error: %v", in, err)
			}
			if got != tc.want {
				t.Errorf("parseHumanDuration(%q) = %v, want %v", in, got, tc.want)
			}
		})
	}
}

func TestParseSinceFlag(t *testing.T) {
	t.Parallel()
	if got, err := parseSinceFlag(""); err != nil || !got.IsZero() {
		t.Errorf("parseSinceFlag(\"\") = (%v, %v), want (zero, nil)", got, err)
	}
	got, err := parseSinceFlag("24h")
	if err != nil {
		t.Fatalf("parseSinceFlag(24h): %v", err)
	}
	delta := time.Since(got)
	if delta < 23*time.Hour || delta > 25*time.Hour {
		t.Errorf("parseSinceFlag(24h) delta = %v, want ~24h", delta)
	}
}

// seedSessions populates a kdb at dir with two sessions, one fresh and
// one stale, each containing a single ToolCall that served two refs.
// Returns the absolute paths so tests can chdir before running CLI fns.
func seedSessions(t *testing.T, dir string) {
	t.Helper()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	col := observe.New(db, event.New(db), observe.Options{
		FlushInterval: 5 * time.Millisecond,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ps := session.NewPersistentStore(db, col)

	fresh := ps.Get("fresh-uuid").(*session.PersistentSession)
	fresh.Note(session.Observation{
		Refs: []string{"e:internal/kdb/db.go", "d:adr-0008"},
		At:   time.Now().UTC(),
		Tool: "recall",
	})

	stale := ps.Get("stale-uuid").(*session.PersistentSession)
	stale.Note(session.Observation{
		Refs: []string{"e:internal/log/logger.go"},
		At:   time.Now().UTC().Add(-90 * 24 * time.Hour),
		Tool: "recall",
	})
	_ = stale // suppress unused if Note is no-op

	if err := col.Close(); err != nil {
		t.Fatalf("collector close: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db close: %v", err)
	}

	// Backdate the stale session's CreatedAt by re-opening the DB and
	// rewriting the observation with an old timestamp. The collector
	// uses time.Now() when At is zero on the inner SessionStart, so we
	// override here.
	db2, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open #2: %v", err)
	}
	defer func() { _ = db2.Close() }()
	store := event.New(db2)
	if _, err := store.Append(context.Background(), &event.Observation{
		OID:    event.SessionOID("stale-uuid"),
		Action: event.ActionSession,
		Hdr:    event.Header{CreatedAt: time.Now().UTC().Add(-60 * 24 * time.Hour)},
	}); err != nil {
		t.Fatalf("append backdated session: %v", err)
	}
}

// withWorkingDir runs fn after chdir to dir, restoring the prior cwd
// on return (via t.Cleanup).
func withWorkingDir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func TestSessionsList_ReturnsBothSessions(t *testing.T) {
	dir := t.TempDir()
	seedSessions(t, dir)
	withWorkingDir(t, dir)

	db, store, closeFn, err := openSessionDB()
	if err != nil {
		t.Fatalf("openSessionDB: %v", err)
	}
	defer closeFn()

	rows, err := collectSessions(context.Background(), db, store, time.Time{})
	if err != nil {
		t.Fatalf("collectSessions: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("collectSessions returned %d rows, want 2", len(rows))
	}
	ids := map[string]bool{}
	for _, r := range rows {
		ids[r.uuid] = true
	}
	if !ids["fresh-uuid"] || !ids["stale-uuid"] {
		t.Errorf("missing one of fresh/stale: %v", ids)
	}
}

func TestSessionsList_SinceFiltersOldSessions(t *testing.T) {
	dir := t.TempDir()
	seedSessions(t, dir)
	withWorkingDir(t, dir)

	db, store, closeFn, err := openSessionDB()
	if err != nil {
		t.Fatalf("openSessionDB: %v", err)
	}
	defer closeFn()

	cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour)
	rows, err := collectSessions(context.Background(), db, store, cutoff)
	if err != nil {
		t.Fatalf("collectSessions: %v", err)
	}
	if len(rows) != 1 || rows[0].uuid != "fresh-uuid" {
		t.Errorf("rows = %+v, want only fresh-uuid", rows)
	}
}

func TestSessionsClear_All_RemovesAllSessions(t *testing.T) {
	dir := t.TempDir()
	seedSessions(t, dir)
	withWorkingDir(t, dir)

	if err := runSessionsClear(true, time.Time{}); err != nil {
		t.Fatalf("clear --all: %v", err)
	}

	db, store, closeFn, err := openSessionDB()
	if err != nil {
		t.Fatalf("openSessionDB: %v", err)
	}
	defer closeFn()

	rows, err := collectSessions(context.Background(), db, store, time.Time{})
	if err != nil {
		t.Fatalf("collectSessions: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("after clear --all, sessions = %d, want 0", len(rows))
	}
}

func TestSessionsClear_OlderThan_DropsOnlyStale(t *testing.T) {
	dir := t.TempDir()
	seedSessions(t, dir)
	withWorkingDir(t, dir)

	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	if err := runSessionsClear(false, cutoff); err != nil {
		t.Fatalf("clear --older-than: %v", err)
	}

	db, store, closeFn, err := openSessionDB()
	if err != nil {
		t.Fatalf("openSessionDB: %v", err)
	}
	defer closeFn()

	rows, err := collectSessions(context.Background(), db, store, time.Time{})
	if err != nil {
		t.Fatalf("collectSessions: %v", err)
	}
	if len(rows) != 1 || rows[0].uuid != "fresh-uuid" {
		t.Errorf("rows after retention = %+v, want only fresh-uuid", rows)
	}
}

func TestSessionsShow_ReportsNotFound(t *testing.T) {
	dir := t.TempDir()
	seedSessions(t, dir)
	withWorkingDir(t, dir)

	err := runSessionsShow("nonexistent")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("runSessionsShow(nonexistent) err = %v, want \"not found\"", err)
	}
}

// writeConfigToml writes a minimal repo-level config so config.Load
// picks up our retention override.
func writeConfigToml(t *testing.T, dir, body string) {
	t.Helper()
	cfgDir := dir + "/.pcke"
	if err := os.MkdirAll(cfgDir, 0o750); err != nil {
		t.Fatalf("mkdir .pcke: %v", err)
	}
	if err := os.WriteFile(cfgDir+"/config.toml", []byte(body), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
}

func TestRetentionPrune_DropsStaleSessionsOnStartup(t *testing.T) {
	dir := t.TempDir()
	seedSessions(t, dir)
	writeConfigToml(t, dir, "[telemetry]\nretention_days = 7\n")

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	runRetentionPrune(dir, db)
	// The prune runs in a goroutine; wait for it deterministically by
	// polling collectSessions until either the stale one is gone or we
	// hit a timeout.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := collectSessions(context.Background(), db, event.New(db), time.Time{})
		if err != nil {
			t.Fatalf("collectSessions: %v", err)
		}
		if len(rows) == 1 && rows[0].uuid == "fresh-uuid" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	rows, _ := collectSessions(context.Background(), db, event.New(db), time.Time{})
	t.Fatalf("retention prune did not drop stale session within deadline; remaining=%+v", rows)
}

func TestRetentionPrune_DisabledKeepsSessions(t *testing.T) {
	dir := t.TempDir()
	seedSessions(t, dir)
	writeConfigToml(t, dir, "[telemetry]\nretention_days = 0\n")

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	runRetentionPrune(dir, db)
	// Give any goroutine a moment to misbehave; then verify both
	// sessions are still present.
	time.Sleep(200 * time.Millisecond)
	rows, err := collectSessions(context.Background(), db, event.New(db), time.Time{})
	if err != nil {
		t.Fatalf("collectSessions: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("after disabled retention, sessions = %d, want 2", len(rows))
	}
}

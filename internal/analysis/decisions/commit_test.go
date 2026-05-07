package decisions_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/analysis/decisions"
	"github.com/jenaiz/pcke/internal/kdb/event"
)

func TestBackfillFromCommits_EmptyInput(t *testing.T) {
	t.Parallel()
	db := openBlankDB(t)
	n, err := decisions.BackfillFromCommits(context.Background(), db, nil)
	if err != nil {
		t.Fatalf("BackfillFromCommits: %v", err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
}

func TestBackfillFromCommits_MatchesPatterns(t *testing.T) {
	t.Parallel()
	db := openBlankDB(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	commits := []decisions.CommitInfo{
		{Hash: "aaaaaaaaaaaaaaaaaaaa", Author: "alice", Time: now, Message: "decision: switch to event-log schema\n\nRationale here."},
		{Hash: "bbbbbbbbbbbbbbbbbbbb", Author: "bob", Time: now, Message: "ADR: deprecate federation"},
		{Hash: "ccccccccccccccccccc1", Author: "carol", Time: now, Message: "RFC: opt-in vector reranker"},
		{Hash: "dddddddddddddddddddd", Author: "dan", Time: now, Message: "feat: not a decision"},
		{Hash: "eeeeeeeeeeeeeeeeeeee", Author: "eve", Time: now, Message: "decisional: not a real prefix"},
	}
	n, err := decisions.BackfillFromCommits(context.Background(), db, commits)
	if err != nil {
		t.Fatalf("BackfillFromCommits: %v", err)
	}
	if n != 3 {
		t.Errorf("wrote %d, want 3 (3 commits match the prefix)", n)
	}

	store := event.New(db)
	for _, want := range []string{"commit:aaaaaaaaaaaa", "commit:bbbbbbbbbbbb", "commit:cccccccccccc"} {
		if _, err := store.Latest(context.Background(), event.KindDecision, want); err != nil {
			t.Errorf("Latest(%s): %v", want, err)
		}
	}
	if _, err := store.Latest(context.Background(), event.KindDecision, "commit:dddddddddddd"); !errors.Is(err, event.ErrNotFound) {
		t.Errorf("non-decision commit was stored: got %v, want ErrNotFound", err)
	}
}

func TestBackfillFromCommits_TrimsPrefixFromTitle(t *testing.T) {
	t.Parallel()
	db := openBlankDB(t)
	commits := []decisions.CommitInfo{
		{
			Hash:    "abcdef0123456789abcd",
			Author:  "x",
			Time:    time.Unix(1_700_000_000, 0).UTC(),
			Message: "decision: pivot to typed-event log\n\nlong rationale...",
		},
	}
	if _, err := decisions.BackfillFromCommits(context.Background(), db, commits); err != nil {
		t.Fatalf("BackfillFromCommits: %v", err)
	}
	store := event.New(db)
	got, err := store.Latest(context.Background(), event.KindDecision, "commit:abcdef012345")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	dec := got.(*event.Decision)
	if dec.Title != "pivot to typed-event log" {
		t.Errorf("Title = %q, want %q (prefix should be stripped)", dec.Title, "pivot to typed-event log")
	}
	if dec.Body != commits[0].Message {
		t.Errorf("Body lost full message: %q", dec.Body)
	}
}

func TestBackfillFromCommits_PreservesAuthorTime(t *testing.T) {
	t.Parallel()
	db := openBlankDB(t)
	when := time.Date(2026, 4, 1, 12, 30, 0, 0, time.UTC)
	commits := []decisions.CommitInfo{
		{Hash: "1111111111111111", Author: "x", Time: when, Message: "decision: foo"},
	}
	if _, err := decisions.BackfillFromCommits(context.Background(), db, commits); err != nil {
		t.Fatalf("BackfillFromCommits: %v", err)
	}
	store := event.New(db)
	got, _ := store.Latest(context.Background(), event.KindDecision, "commit:111111111111")
	if !got.Header().CreatedAt.Equal(when) {
		t.Errorf("CreatedAt = %v, want %v", got.Header().CreatedAt, when)
	}
}

func TestBackfillFromCommits_Idempotent(t *testing.T) {
	t.Parallel()
	db := openBlankDB(t)
	commits := []decisions.CommitInfo{
		{Hash: "deadbeefcafebabe1234", Author: "x", Time: time.Unix(1_700_000_000, 0).UTC(), Message: "decision: foo"},
	}
	if n, err := decisions.BackfillFromCommits(context.Background(), db, commits); err != nil || n != 1 {
		t.Fatalf("first run: n=%d err=%v", n, err)
	}
	n, err := decisions.BackfillFromCommits(context.Background(), db, commits)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if n != 0 {
		t.Errorf("re-run wrote %d, want 0", n)
	}
}

func TestBackfillFromCommits_CaseInsensitive(t *testing.T) {
	t.Parallel()
	db := openBlankDB(t)
	commits := []decisions.CommitInfo{
		{Hash: "1111111111111111", Author: "x", Time: time.Now().UTC(), Message: "DECISION: shouty case"},
		{Hash: "2222222222222222", Author: "x", Time: time.Now().UTC(), Message: "Adr: mixed case"},
	}
	n, err := decisions.BackfillFromCommits(context.Background(), db, commits)
	if err != nil {
		t.Fatalf("BackfillFromCommits: %v", err)
	}
	if n != 2 {
		t.Errorf("wrote %d, want 2 (case-insensitive match)", n)
	}
}

func TestBackfillFromCommits_SkipsEmptyHash(t *testing.T) {
	t.Parallel()
	db := openBlankDB(t)
	commits := []decisions.CommitInfo{
		{Hash: "", Author: "x", Time: time.Now().UTC(), Message: "decision: foo"},
		{Hash: "good1234567890abcdef", Author: "x", Time: time.Now().UTC(), Message: "decision: bar"},
	}
	n, err := decisions.BackfillFromCommits(context.Background(), db, commits)
	if err != nil {
		t.Fatalf("BackfillFromCommits: %v", err)
	}
	if n != 1 {
		t.Errorf("wrote %d, want 1 (empty hash skipped)", n)
	}
}

func TestBackfillFromCommits_ShortHashHandled(t *testing.T) {
	t.Parallel()
	db := openBlankDB(t)
	commits := []decisions.CommitInfo{
		{Hash: "abc123", Author: "x", Time: time.Now().UTC(), Message: "decision: short hash"},
	}
	if _, err := decisions.BackfillFromCommits(context.Background(), db, commits); err != nil {
		t.Fatalf("BackfillFromCommits: %v", err)
	}
	store := event.New(db)
	if _, err := store.Latest(context.Background(), event.KindDecision, "commit:abc123"); err != nil {
		t.Errorf("short-hash record missing: %v", err)
	}
}

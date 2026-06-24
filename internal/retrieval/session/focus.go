package session

import (
	"context"
	"sort"
	"strings"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/graph"
)

// FocusEntry is one ref an agent accessed during a session, paired with
// how many tool calls served it. Count is the focus weight: a ref
// served by more calls is more central to what the agent is doing.
type FocusEntry struct {
	// Ref is the served reference, e.g. "e:internal/kdb/db.go".
	Ref string
	// Count is the number of distinct tool calls that served Ref.
	Count int
}

// FocusMap is the ranked set of refs an agent focused on in a session,
// derived purely from the persisted o:session subgraph
// (session →contains→ call →served→ ref) — not from heuristics
// (PRD v5.2 §6.2 F15.T3).
//
// Entries are ordered by descending Count, then ascending Ref, so the
// map is deterministic for a given subgraph.
type FocusMap struct {
	// SessionID is the session the map was built from.
	SessionID string
	// Entries are the focused refs in descending focus order.
	Entries []FocusEntry
}

// Files returns the focused refs in focus order. The slice is a copy;
// mutating it does not affect the map.
func (m FocusMap) Files() []string {
	out := make([]string, len(m.Entries))
	for i, e := range m.Entries {
		out[i] = e.Ref
	}
	return out
}

// EntityFiles returns only the entity refs (those with the "e:" prefix),
// stripped of the prefix to yield worktree-relative paths. Decisions and
// observation refs are excluded, since focus drives file-scoped retrieval.
func (m FocusMap) EntityFiles() []string {
	var out []string
	for _, e := range m.Entries {
		if path, ok := strings.CutPrefix(e.Ref, "e:"); ok {
			out = append(out, path)
		}
	}
	return out
}

// Top returns the n highest-weighted refs (focus order). n <= 0 returns
// nil; n larger than the map returns all refs.
func (m FocusMap) Top(n int) []string {
	if n <= 0 {
		return nil
	}
	files := m.Files()
	if n > len(files) {
		return files
	}
	return files[:n]
}

// BuildFocusMap derives the focus map for session id from the persisted
// o:session subgraph. It walks session →(contains)→ call →(served)→ ref,
// tallying how many calls served each ref, and returns the refs ranked
// by that count.
//
// The derivation is pure graph traversal: no branch-name or filename
// heuristics participate. An unknown session, or one with no calls,
// yields an empty (non-nil) FocusMap with no error.
func BuildFocusMap(ctx context.Context, db *kdb.DB, id string) (FocusMap, error) {
	fm := FocusMap{SessionID: id}
	if id == "" {
		return fm, nil
	}

	callRefs, err := graph.Neighbors(ctx, db, graph.Ref(event.SessionRef(id)),
		graph.TraversalOptions{
			Direction: graph.Forward,
			EdgeTypes: []string{event.EdgeContains},
		})
	if err != nil {
		return fm, err
	}

	counts := make(map[string]int)
	for _, callRef := range callRefs {
		served, sErr := graph.Neighbors(ctx, db, callRef, graph.TraversalOptions{
			Direction: graph.Forward,
			EdgeTypes: []string{event.EdgeServed},
		})
		if sErr != nil {
			return fm, sErr
		}
		// A single call may serve the same ref more than once via repeated
		// edges; dedup per call so Count means "calls that touched ref".
		seen := make(map[string]struct{}, len(served))
		for _, sr := range served {
			ref := string(sr)
			if _, dup := seen[ref]; dup {
				continue
			}
			seen[ref] = struct{}{}
			counts[ref]++
		}
	}

	fm.Entries = rankFocus(counts)
	return fm, nil
}

// rankFocus turns a ref→count tally into the deterministic descending
// order used by FocusMap: higher count first, ties broken by ref asc.
func rankFocus(counts map[string]int) []FocusEntry {
	if len(counts) == 0 {
		return nil
	}
	entries := make([]FocusEntry, 0, len(counts))
	for ref, c := range counts {
		entries = append(entries, FocusEntry{Ref: ref, Count: c})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Count != entries[j].Count {
			return entries[i].Count > entries[j].Count
		}
		return entries[i].Ref < entries[j].Ref
	})
	return entries
}

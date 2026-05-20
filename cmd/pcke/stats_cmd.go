package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/graph"
)

// newStatsCmd implements `pcke stats` (Phase 14 F14.T5).
//
// Output is intentionally raw counts: tool calls, files served,
// decisions surfaced, and the top-N most-served decisions. No derived
// "quality score" — that is post-1.0 (PRD v5.2 §9).
func newStatsCmd() *cobra.Command {
	var sinceFlag string
	var topN int
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Raw counts of observed MCP activity",
		Long: `Walks the Phase 14 observation graph and prints counts:

  sessions      total session observations in scope
  tool_calls    total ToolCall observations
  files         distinct e: refs served across all calls
  decisions     distinct d: refs served across all calls
  by_tool       top tools by call count
  top_decisions top-N decisions by served-count

No derived score is computed; the PRD defers that to post-1.0.

Examples:
  pcke stats
  pcke stats --since 7d
  pcke stats --top 10`,
		RunE: func(_ *cobra.Command, _ []string) error {
			since, err := parseSinceFlag(sinceFlag)
			if err != nil {
				return err
			}
			if topN <= 0 {
				topN = 5
			}
			return runStats(since, topN)
		},
	}
	cmd.Flags().StringVar(&sinceFlag, "since", "", "Only count observations newer than this (e.g. 24h, 7d, 30d)")
	cmd.Flags().IntVar(&topN, "top", 5, "How many entries to show in the top-tools and top-decisions tables")
	return cmd
}

// statsReport is the value object printed by runStats. Surfaced as its
// own type so the test suite can assert on counts directly without
// re-parsing stdout.
type statsReport struct {
	Sessions     int
	ToolCalls    int
	Files        int
	Decisions    int
	ByTool       []countedString
	TopDecisions []countedString
}

type countedString struct {
	Name  string
	Count int
}

func runStats(since time.Time, topN int) error {
	db, store, closeFn, err := openSessionDB()
	if err != nil {
		return err
	}
	defer closeFn()

	report, err := collectStats(context.Background(), db, store, since, topN)
	if err != nil {
		return fmt.Errorf("stats: %w", err)
	}

	printStats(os.Stdout, report)
	return nil
}

// collectStats walks every Observation and tallies the counts the
// report needs. Made package-private so tests can build a report
// without spinning up the cobra command.
func collectStats(ctx context.Context, db *kdb.DB, store *event.Store, since time.Time, topN int) (statsReport, error) {
	var (
		sessions  int
		calls     int
		toolCount = map[string]int{}
		fileSet   = map[string]struct{}{}
		decSet    = map[string]struct{}{}
		decCount  = map[string]int{}
	)

	err := store.IterateKind(ctx, event.KindObservation, func(e event.Event) error {
		obs, ok := e.(*event.Observation)
		if !ok {
			return nil
		}
		if !since.IsZero() && obs.Header().CreatedAt.Before(since) {
			return nil
		}
		switch obs.Action {
		case event.ActionSession:
			sessions++
		case event.ActionCall:
			calls++
			if obs.Subject != "" {
				toolCount[obs.Subject]++
			}
			uuid, ok := event.CallUUID(obs.OID)
			if !ok {
				return nil
			}
			served, gerr := graph.Neighbors(ctx, db, graph.Ref(event.CallRef(uuid)),
				graph.TraversalOptions{Direction: graph.Forward, EdgeTypes: []string{event.EdgeServed}})
			if gerr != nil {
				return nil
			}
			for _, dst := range served {
				s := string(dst)
				switch {
				case strings.HasPrefix(s, "e:"):
					fileSet[s] = struct{}{}
				case strings.HasPrefix(s, "d:"):
					decSet[s] = struct{}{}
					decCount[s]++
				}
			}
		}
		return nil
	})
	if err != nil {
		return statsReport{}, err
	}

	return statsReport{
		Sessions:     sessions,
		ToolCalls:    calls,
		Files:        len(fileSet),
		Decisions:    len(decSet),
		ByTool:       topCounts(toolCount, topN),
		TopDecisions: topCounts(decCount, topN),
	}, nil
}

// topCounts ranks counts descending by value (ties broken alphabetically)
// and returns the first n entries.
func topCounts(m map[string]int, n int) []countedString {
	out := make([]countedString, 0, len(m))
	for k, v := range m {
		out = append(out, countedString{Name: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if n > 0 && n < len(out) {
		out = out[:n]
	}
	return out
}

// printStats renders the report to w in the tabular form documented in
// the command's Long description.
func printStats(w *os.File, r statsReport) {
	_, _ = fmt.Fprintf(w, "sessions     %d\n", r.Sessions)
	_, _ = fmt.Fprintf(w, "tool_calls   %d\n", r.ToolCalls)
	_, _ = fmt.Fprintf(w, "files        %d\n", r.Files)
	_, _ = fmt.Fprintf(w, "decisions    %d\n", r.Decisions)
	if len(r.ByTool) > 0 {
		_, _ = fmt.Fprintln(w, "\nby_tool:")
		for _, c := range r.ByTool {
			_, _ = fmt.Fprintf(w, "  %-32s %d\n", c.Name, c.Count)
		}
	}
	if len(r.TopDecisions) > 0 {
		_, _ = fmt.Fprintln(w, "\ntop_decisions:")
		for _, c := range r.TopDecisions {
			_, _ = fmt.Fprintf(w, "  %-48s %d\n", c.Name, c.Count)
		}
	}
}

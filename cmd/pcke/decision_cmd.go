package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/graph"
)

// newDecisionCmd builds the `pcke decision` subtree:
//
//	pcke decision list   list every decision in the event log
//	pcke decision show   show one decision by id
func newDecisionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decision",
		Short: "Inspect decisions stored in the typed-event log",
		Long: `Decisions are typed assertions about code with severity (must/should/may)
and scope (file/module/global). They come from three sources:

  - docs/adr/*.md files           (severity=must,   scope=global)
  - @pcke-rule annotations        (severity=should, scope=file)
  - decision: / adr: / rfc:       (severity=should, scope=global)
    commit messages

The 'pcke scan' command backfills decisions automatically; this
subcommand just reads them back.

Examples:
  pcke decision list
  pcke decision list --source=adr
  pcke decision list --severity=must
  pcke decision list --file=internal/kdb/db.go
  pcke decision show adr:0008-context-graph-pivot`,
	}
	cmd.AddCommand(newDecisionListCmd(), newDecisionShowCmd())
	return cmd
}

func newDecisionListCmd() *cobra.Command {
	var sourceFilter, severityFilter, fileFilter string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List decisions",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runDecisionList(sourceFilter, severityFilter, fileFilter)
		},
	}
	cmd.Flags().StringVar(&sourceFilter, "source", "", "Filter by source: adr | annotation | commit | doc | manual")
	cmd.Flags().StringVar(&severityFilter, "severity", "", "Filter by severity: must | should | may")
	cmd.Flags().StringVar(&fileFilter, "file", "", "Filter by repository-relative file path linked to the decision")
	return cmd
}

func newDecisionShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show one decision by id (e.g. adr:0008-context-graph-pivot)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runDecisionShow(args[0])
		},
	}
}

func runDecisionList(sourceFilter, severityFilter, fileFilter string) error {
	db, store, closeFn, err := openEventStoreDB()
	if err != nil {
		return err
	}
	defer closeFn()

	wantSeverity, err := parseSeverityFilter(severityFilter)
	if err != nil {
		return err
	}
	sourceFilter = strings.ToLower(strings.TrimSpace(sourceFilter))

	var allowedDIDs map[string]struct{}
	if fileFilter = strings.TrimSpace(fileFilter); fileFilter != "" {
		allowedDIDs, err = decisionsLinkedToFile(db, fileFilter)
		if err != nil {
			return err
		}
	}

	type row struct {
		id       string
		title    string
		severity string
		source   string
	}
	var rows []row
	err = store.IterateKind(context.Background(), event.KindDecision, func(e event.Event) error {
		d, ok := e.(*event.Decision)
		if !ok {
			return nil
		}
		if d.Header().Lifecycle == event.LifecycleSuperseded {
			return nil
		}
		if sourceFilter != "" && strings.ToLower(d.Source) != sourceFilter {
			return nil
		}
		if wantSeverity != 0 && d.Severity != wantSeverity {
			return nil
		}
		if allowedDIDs != nil {
			if _, ok := allowedDIDs[d.DID]; !ok {
				return nil
			}
		}
		rows = append(rows, row{
			id:       d.DID,
			title:    d.Title,
			severity: severityName(d.Severity),
			source:   d.Source,
		})
		return nil
	})
	if err != nil {
		return fmt.Errorf("decision list: %w", err)
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	for _, r := range rows {
		fmt.Printf("%-44s %-7s %-10s %s\n", r.id, r.severity, r.source, r.title)
	}
	fmt.Fprintf(os.Stderr, "\n%d decision(s)\n", len(rows))
	return nil
}

func runDecisionShow(id string) error {
	store, closeFn, err := openEventStore()
	if err != nil {
		return err
	}
	defer closeFn()

	got, err := store.Latest(context.Background(), event.KindDecision, id)
	if err != nil {
		if errors.Is(err, event.ErrNotFound) {
			return fmt.Errorf("decision %q not found", id)
		}
		return fmt.Errorf("decision show: %w", err)
	}
	d, ok := got.(*event.Decision)
	if !ok {
		return fmt.Errorf("decision show: %q is not a Decision (got %T)", id, got)
	}

	fmt.Printf("ID:        %s\n", d.DID)
	fmt.Printf("Title:     %s\n", d.Title)
	fmt.Printf("Severity:  %s\n", severityName(d.Severity))
	fmt.Printf("Scope:     %s\n", scopeName(d.Scope))
	fmt.Printf("Source:    %s\n", d.Source)
	fmt.Printf("Lifecycle: %s\n", lifecycleName(d.Header().Lifecycle))
	fmt.Printf("Version:   %d\n", d.Header().Version)
	fmt.Printf("CreatedAt: %s\n", d.Header().CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
	fmt.Println()
	fmt.Println(d.Body)
	return nil
}

// openEventStore opens the kdb at the current working directory and
// returns an event.Store plus a close function. Common to the
// decision and history subcommands.
func openEventStore() (*event.Store, func(), error) {
	_, store, closeFn, err := openEventStoreDB()
	if err != nil {
		return nil, nil, err
	}
	return store, closeFn, nil
}

// openEventStoreDB is like openEventStore but also exposes the
// underlying *kdb.DB for callers that need graph traversal.
func openEventStoreDB() (*kdb.DB, *event.Store, func(), error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get working directory: %w", err)
	}
	db, err := kdb.Open(cwd, nil)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open database: %w", err)
	}
	return db, event.New(db), func() { _ = db.Close() }, nil
}

// decisionsLinkedToFile returns the set of decision ids (DID) reachable
// from the file's e: entity via forward decision_link edges.
func decisionsLinkedToFile(db *kdb.DB, filePath string) (map[string]struct{}, error) {
	start := graph.Ref("e:" + filePath)
	refs, err := graph.Neighbors(context.Background(), db, start, graph.TraversalOptions{
		EdgeTypes: []string{"decision_link"},
		Direction: graph.Forward,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve decisions for file %q: %w", filePath, err)
	}
	dids := make(map[string]struct{}, len(refs))
	for _, r := range refs {
		dids[strings.TrimPrefix(string(r), "d:")] = struct{}{}
	}
	return dids, nil
}

func parseSeverityFilter(s string) (event.Severity, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return 0, nil
	case "must":
		return event.SeverityMust, nil
	case "should":
		return event.SeverityShould, nil
	case "may":
		return event.SeverityMay, nil
	default:
		return 0, fmt.Errorf("invalid --severity %q (want must|should|may)", s)
	}
}

func severityName(s event.Severity) string {
	switch s {
	case event.SeverityMust:
		return "must"
	case event.SeverityShould:
		return "should"
	case event.SeverityMay:
		return "may"
	default:
		return "?"
	}
}

func scopeName(s event.Scope) string {
	switch s {
	case event.ScopeFile:
		return "file"
	case event.ScopeModule:
		return "module"
	case event.ScopeGlobal:
		return "global"
	default:
		return "?"
	}
}

func lifecycleName(l event.Lifecycle) string {
	switch l {
	case event.LifecycleActive:
		return "active"
	case event.LifecycleSuperseded:
		return "superseded"
	case event.LifecycleHistorical:
		return "historical"
	default:
		return "?"
	}
}

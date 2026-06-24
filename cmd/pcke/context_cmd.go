package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jenaiz/pcke/internal/analysis"
	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/retrieval"
)

// newContextCmd implements `pcke context` (Phase 15 F15.T6).
//
// It assembles the ranked, budget-bounded context package for a file —
// the same engine the MCP get_context_for_file tool drives — and prints
// it for human inspection. When --workflow is omitted the workflow is
// auto-detected from git signals (branch name, changed files).
func newContextCmd() *cobra.Command {
	var workflowFlag string
	var budget int
	cmd := &cobra.Command{
		Use:   "context <file>",
		Short: "Show the ranked context package for a file",
		Long: `Assembles the ranked, budget-bounded context subgraph for a file:
the entities in its 2-hop neighborhood, applicable decisions, and the
1-hop anticipatory pre-load.

The ranking workflow is auto-detected from git (branch name + changed
files) unless --workflow is given. Recognised workflows:

  explore | bugfix | feature | review | refactor | test

Examples:
  pcke context internal/kdb/db.go
  pcke context internal/kdb/db.go --workflow review
  pcke context internal/kdb/db.go --budget 4000`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("context: get working directory: %w", err)
			}
			return runContext(os.Stdout, cwd, args[0], workflowFlag, budget)
		},
	}
	cmd.Flags().StringVar(&workflowFlag, "workflow", "",
		"Ranking workflow: explore|bugfix|feature|review|refactor|test (default: auto-detect from git)")
	cmd.Flags().IntVar(&budget, "budget", 0, "Approximate token budget (0 = engine default)")
	return cmd
}

// runContext resolves the workflow, assembles the context package for
// file, and writes a human-readable report to w. Separated from the
// cobra wiring so tests can drive it with an explicit writer and cwd.
func runContext(w io.Writer, cwd, file, workflowFlag string, budget int) error {
	if strings.TrimSpace(file) == "" {
		return fmt.Errorf("context: file argument is required")
	}

	wf, detected := resolveContextWorkflow(cwd, workflowFlag)

	db, err := kdb.Open(cwd, nil)
	if err != nil {
		return fmt.Errorf("context: open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	engine := retrieval.New(db)
	pkg, err := engine.Assemble(context.Background(), retrieval.Request{
		FilePath: file,
		Budget:   budget,
		Workflow: wf,
	})
	if err != nil {
		return fmt.Errorf("context: assemble: %w", err)
	}

	printContextReport(w, file, wf, detected, pkg)
	return nil
}

// resolveContextWorkflow returns the effective workflow and whether it
// was auto-detected. A non-empty flag is taken verbatim (lower-cased);
// otherwise git signals drive DetectWorkflow. Detection failures (not a
// git repo) fall back to WorkflowExplore.
func resolveContextWorkflow(cwd, flag string) (retrieval.Workflow, bool) {
	if strings.TrimSpace(flag) != "" {
		return retrieval.Workflow(strings.ToLower(strings.TrimSpace(flag))), false
	}
	dc := gitDetectionContext(cwd)
	wf, _ := retrieval.DetectWorkflow(dc)
	return wf, true
}

// gitDetectionContext builds a DetectionContext from the repo at cwd.
// A missing or unreadable repo yields a zero context (DetectWorkflow
// then returns explore at low confidence).
func gitDetectionContext(cwd string) retrieval.DetectionContext {
	gi, err := analysis.NewGitIntel(cwd)
	if err != nil {
		return retrieval.DetectionContext{}
	}
	branch := gi.CurrentBranch()
	changed, _ := gi.ChangedFiles()
	return retrieval.DetectionContext{
		BranchName:     branch,
		ChangedFiles:   changed,
		HasUncommitted: len(changed) > 0,
		IsMainBranch:   isTrunkBranch(branch),
	}
}

// isTrunkBranch reports whether name is a conventional trunk branch.
func isTrunkBranch(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "main", "master", "develop":
		return true
	default:
		return false
	}
}

// printContextReport renders the package as a human-readable report.
func printContextReport(w io.Writer, file string, wf retrieval.Workflow, detected bool, pkg *retrieval.ContextPackage) {
	origin := "explicit"
	if detected {
		origin = "auto-detected"
	}
	_, _ = fmt.Fprintf(w, "Context for %s\n", file)
	_, _ = fmt.Fprintf(w, "  workflow: %s (%s)\n", workflowOrExplore(wf), origin)
	_, _ = fmt.Fprintf(w, "  budget:   %d tokens used / %d limit%s\n",
		pkg.TokensUsed, pkg.BudgetLimit, truncatedSuffix(pkg.Truncated))

	for _, warn := range pkg.Warnings {
		_, _ = fmt.Fprintf(w, "  warning:  %s\n", warn)
	}

	_, _ = fmt.Fprintf(w, "\nSections (%d):\n", len(pkg.Sections))
	for _, sec := range pkg.Sections {
		_, _ = fmt.Fprintf(w, "  [%.2f] %s  %s\n", sec.Score, sec.Ref, sec.Title)
	}

	if len(pkg.Anticipated) > 0 {
		_, _ = fmt.Fprintf(w, "\nAnticipated (1-hop neighbours, %d):\n", len(pkg.Anticipated))
		for _, ref := range pkg.Anticipated {
			_, _ = fmt.Fprintf(w, "  %s\n", ref)
		}
	}
}

// workflowOrExplore renders an empty workflow as "explore".
func workflowOrExplore(wf retrieval.Workflow) retrieval.Workflow {
	if wf == "" {
		return retrieval.WorkflowExplore
	}
	return wf
}

// truncatedSuffix returns a " (truncated)" marker when t is true.
func truncatedSuffix(t bool) string {
	if t {
		return " (truncated)"
	}
	return ""
}

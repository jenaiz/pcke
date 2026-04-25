package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jenaiz/pcke/internal/analysis"
	"github.com/jenaiz/pcke/internal/config"
	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/index/fts"
	"github.com/jenaiz/pcke/internal/kdb/query"
	"github.com/jenaiz/pcke/internal/kdb/tx"
	"github.com/jenaiz/pcke/internal/output"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialise pcke in the current repository",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println("pcke init: not yet implemented (Phase 2)")
			return nil
		},
	}
}

// warnBranchMismatch prints a warning to stderr if the current branch differs
// from the branch recorded during the last scan.
func warnBranchMismatch(db *kdb.DB, cwd string) {
	if msg := analysis.CheckBranchMismatch(context.Background(), db, cwd); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
}

func newScanCmd() *cobra.Command {
	var full bool

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan the repository and update the knowledge base",
		RunE: func(_ *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("scan: get working directory: %w", err)
			}

			db, err := kdb.Open(cwd, nil)
			if err != nil {
				return fmt.Errorf("scan: open database: %w", err)
			}
			defer func() { _ = db.Close() }()

			cfg := config.Defaults().Scan
			scanner, err := analysis.NewScanner(cwd, db, cfg)
			if err != nil {
				return fmt.Errorf("scan: init scanner: %w", err)
			}

			result, err := scanner.Scan(context.Background(), full)
			if err != nil {
				return fmt.Errorf("scan: %w", err)
			}

			fmt.Printf("scan complete: %d created, %d updated, %d deleted (%d files in %s)\n",
				result.NodesCreated, result.NodesUpdated, result.NodesDeleted,
				result.FilesScanned, result.Duration.Round(1e6))
			return nil
		},
	}

	cmd.Flags().BoolVar(&full, "full", false, "Force a full scan (rebuild all nodes)")

	return cmd
}

func newSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Generate output files (.context/, copilot-instructions, etc.)",
		RunE: func(_ *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("sync: get working directory: %w", err)
			}

			db, err := kdb.Open(cwd, nil)
			if err != nil {
				return fmt.Errorf("sync: open database: %w", err)
			}
			defer func() { _ = db.Close() }()
			warnBranchMismatch(db, cwd)

			renderer := output.NewRenderer(cwd, db)
			result, err := renderer.Sync(context.Background())
			if err != nil {
				return fmt.Errorf("sync: %w", err)
			}

			fmt.Printf("sync complete: %d files written\n", result.FilesWritten)
			return nil
		},
	}
}

func newRuleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rule",
		Short: "Manage project rules",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println("pcke rule: not yet implemented (Phase 2)")
			return nil
		},
	}
}

func newNoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "note",
		Short: "Manage project notes",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println("pcke note: not yet implemented (Phase 2)")
			return nil
		},
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show knowledge base status",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println("pcke status: not yet implemented (Phase 2)")
			return nil
		},
	}
}

func newModulesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "modules",
		Short: "List detected modules",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println("pcke modules: not yet implemented (Phase 2)")
			return nil
		},
	}
}

func newDiagnosticsCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "diagnostics",
		Short: "Show database diagnostics",
		RunE: func(_ *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("diagnostics: get working directory: %w", err)
			}

			db, err := kdb.Open(cwd, nil)
			if err != nil {
				return fmt.Errorf("diagnostics: open database: %w", err)
			}
			defer func() { _ = db.Close() }()
			warnBranchMismatch(db, cwd)

			stats, err := db.Stats()
			if err != nil {
				return fmt.Errorf("diagnostics: gather stats: %w", err)
			}

			switch format {
			case "json":
				data, err := stats.JSON()
				if err != nil {
					return fmt.Errorf("diagnostics: marshal JSON: %w", err)
				}
				fmt.Println(string(data))
			default:
				fmt.Print(stats.Human())
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json")

	return cmd
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View and manage configuration",
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "get [key]",
			Short: "Get a configuration value",
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, _ []string) error {
				fmt.Println("pcke config get: not yet implemented (Phase 2)")
				return nil
			},
		},
		&cobra.Command{
			Use:   "set [key] [value]",
			Short: "Set a configuration value",
			Args:  cobra.ExactArgs(2),
			RunE: func(_ *cobra.Command, _ []string) error {
				fmt.Println("pcke config set: not yet implemented (Phase 2)")
				return nil
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List all configuration values",
			RunE: func(_ *cobra.Command, _ []string) error {
				fmt.Println("pcke config list: not yet implemented (Phase 2)")
				return nil
			},
		},
	)

	return cmd
}

func newRecallCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "recall [query]",
		Short: "Search the knowledge base using full-text search",
		Long: `Search the knowledge base for nodes matching a natural language query.
Results are ranked using BM25 relevance scoring.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			queryStr := strings.Join(args, " ")

			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("recall: get working directory: %w", err)
			}

			db, err := kdb.Open(cwd, nil)
			if err != nil {
				return fmt.Errorf("recall: open database: %w", err)
			}
			defer func() { _ = db.Close() }()
			warnBranchMismatch(db, cwd)

			// Build FTS index from the DB.
			idx := fts.NewIndex()
			if err := db.View(context.Background(), func(rtx *tx.ReadTx) error {
				c := rtx.Cursor()
				if !c.First() {
					return nil
				}
				for c.Valid() {
					idx.AddDocument(string(c.Value()))
					c.Next()
				}
				return nil
			}); err != nil {
				return fmt.Errorf("recall: index documents: %w", err)
			}
			idx.Commit()

			planner := query.NewPlanner(idx)
			results := planner.Search(queryStr, limit)

			if len(results) == 0 {
				fmt.Println("No results found.")
				return nil
			}

			for i, r := range results {
				fmt.Printf("%d. [doc:%d] score=%.4f\n", i+1, r.DocID, r.Score)
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 10, "Maximum number of results to return")

	return cmd
}

func newCompactCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "compact",
		Short: "Compact the database to reclaim space",
		Long: `Offline compaction copies all live key-value pairs into a fresh database
file, reclaiming free pages and pruning soft-deleted data.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("compact: get working directory: %w", err)
			}

			db, err := kdb.Open(cwd, nil)
			if err != nil {
				return fmt.Errorf("compact: open database: %w", err)
			}
			defer func() { _ = db.Close() }()

			result, err := db.Compact(context.Background())
			if err != nil {
				return fmt.Errorf("compact: %w", err)
			}

			reduction := float64(result.OldSize-result.NewSize) / float64(result.OldSize) * 100
			fmt.Printf("compact complete: %d keys copied, %d → %d bytes (%.1f%% reduction)\n",
				result.KeysCopied, result.OldSize, result.NewSize, reduction)
			return nil
		},
	}
}

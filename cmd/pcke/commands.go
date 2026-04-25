package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jenaiz/pcke/internal/kdb"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialise pcke in the current repository",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println("pcke init: not yet implemented (Phase 0)")
			return nil
		},
	}
}

func newScanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scan",
		Short: "Scan the repository and update the knowledge base",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println("pcke scan: not yet implemented (Phase 0)")
			return nil
		},
	}
}

func newSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Generate output files (.context/, copilot-instructions, etc.)",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println("pcke sync: not yet implemented (Phase 0)")
			return nil
		},
	}
}

func newRuleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rule",
		Short: "Manage project rules",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println("pcke rule: not yet implemented (Phase 0)")
			return nil
		},
	}
}

func newNoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "note",
		Short: "Manage project notes",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println("pcke note: not yet implemented (Phase 0)")
			return nil
		},
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show knowledge base status",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println("pcke status: not yet implemented (Phase 0)")
			return nil
		},
	}
}

func newModulesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "modules",
		Short: "List detected modules",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println("pcke modules: not yet implemented (Phase 0)")
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
				fmt.Println("pcke config get: not yet implemented (Phase 0)")
				return nil
			},
		},
		&cobra.Command{
			Use:   "set [key] [value]",
			Short: "Set a configuration value",
			Args:  cobra.ExactArgs(2),
			RunE: func(_ *cobra.Command, _ []string) error {
				fmt.Println("pcke config set: not yet implemented (Phase 0)")
				return nil
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List all configuration values",
			RunE: func(_ *cobra.Command, _ []string) error {
				fmt.Println("pcke config list: not yet implemented (Phase 0)")
				return nil
			},
		},
	)

	return cmd
}

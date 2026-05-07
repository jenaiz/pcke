// Package main provides the root command and sub-command stubs for the pcke CLI.
//
// Phase 0 — Task T12: CLI skeleton (Cobra) + config loader + slog wiring.
package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"

	plog "github.com/jenaiz/pcke/internal/log"
)

// version is injected at build time via `-ldflags "-X main.version=..."`.
var version = "dev"

func init() {
	if version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "pcke",
		Short: "Project Context & Knowledge Engine",
		Long:  "pcke indexes your codebase into a local knowledge base for AI assistants.",
		// Silence Cobra's default error/usage printing; we handle it ourselves.
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRun: func(_ *cobra.Command, _ []string) {
			// Wire up slog from PCKE_LOG_LEVEL env.
			_ = plog.Logger("pcke.cli")
		},
	}

	root.SetVersionTemplate(fmt.Sprintf("pcke %s\n", version))
	root.Version = version

	// Register sub-commands (stubs for Phase 0).
	root.AddCommand(
		newInitCmd(),
		newScanCmd(),
		newSyncCmd(),
		newRuleCmd(),
		newNoteCmd(),
		newStatusCmd(),
		newModulesCmd(),
		newDiagnosticsCmd(),
		newConfigCmd(),
		newRecallCmd(),
		newCompactCmd(),
		newQueryCmd(),
		newExplainCmd(),
		newExportCmd(),
		newMigrateCmd(),
		newSchemaCmd(),
		newServeCmd(),
		newRelationsCmd(),
		newCleanCmd(),
		newWatchCmd(),
		newShellCmd(),
		newOnboardCmd(),
		newFederationCmd(),
		newGraphCmd(),
		newDecisionCmd(),
		newHistoryCmd(),
	)

	return root
}

func main() {
	cmd := newRootCmd()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(ExitUserError)
	}
}

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/jenaiz/pcke/internal/analysis"
	"github.com/jenaiz/pcke/internal/config"
	//nolint:staticcheck // SA1019: federation is intentionally retained while frozen; CLI surface is the legitimate caller (PRD v5.2 §2).
	"github.com/jenaiz/pcke/internal/federation"
	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/index/fts"
	"github.com/jenaiz/pcke/internal/kdb/migrate"
	kdbquery "github.com/jenaiz/pcke/internal/kdb/query"
	"github.com/jenaiz/pcke/internal/kdb/tx"
	"github.com/jenaiz/pcke/internal/mcp"
	"github.com/jenaiz/pcke/internal/onboard"
	"github.com/jenaiz/pcke/internal/output"
	"github.com/jenaiz/pcke/internal/query"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialise pcke in the current repository",
		Long: `Initialise the pcke knowledge base in the current repository.

Creates a .pcke/ directory containing the database file. Safe to run
multiple times (idempotent).

Examples:
  pcke init
  cd /path/to/project && pcke init`,
		RunE: func(_ *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("init: get working directory: %w", err)
			}

			// Check if already initialised.
			pckeDir := cwd + "/.pcke"
			if _, err := os.Stat(pckeDir); err == nil {
				fmt.Println("pcke is already initialised in this repository.")
				return nil
			}

			db, err := kdb.Open(cwd, nil)
			if err != nil {
				return fmt.Errorf("init: create database: %w", err)
			}
			// Stamp a fresh database at the current schema version so a
			// later scan/serve doesn't report a run of no-op migrations.
			if _, err := applyPendingMigrations(context.Background(), db); err != nil {
				_ = db.Close()
				return fmt.Errorf("init: migrate: %w", err)
			}
			_ = db.Close()

			fmt.Println("Initialised pcke knowledge base in .pcke/")
			fmt.Println("Next steps:")
			fmt.Println("  pcke scan       Scan the repository")
			fmt.Println("  pcke sync       Generate context files")
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
	var (
		full bool
		deep bool
	)

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan the repository and update the knowledge base",
		Long: `Scan the repository for source files and update the knowledge base.

Incrementally detects new, modified, and deleted files. Use --full to
force a complete rebuild of all knowledge nodes. Use --deep to extract
AST-level entities (functions, types) and import relations into the
typed-event graph (requires a C compiler for tree-sitter).

Examples:
  pcke scan              Incremental scan (fast)
  pcke scan --full       Full rebuild of knowledge base
  pcke scan --deep       Incremental scan with AST entity + import extraction
  pcke scan --full --deep  Full rebuild with deep analysis`,
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

			// Upgrade a database written by an older pcke before scanning
			// so legacy kn:/rel:/nt: records become typed-event records.
			if applied, mErr := applyPendingMigrations(context.Background(), db); mErr != nil {
				return fmt.Errorf("scan: migrate: %w", mErr)
			} else if applied > 0 {
				fmt.Printf("applied %d schema migration(s) before scanning\n", applied)
			}

			cfg := config.Defaults().Scan
			var opts []analysis.ScanOption
			if deep {
				opts = append(opts, analysis.WithDeep())
			}
			scanner, err := analysis.NewScanner(cwd, db, cfg, opts...)
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
			if deep {
				fmt.Printf("deep analysis: %d entities, %d relations extracted\n",
					result.EntitiesExtracted, result.RelationsCreated)
			} else {
				fmt.Println("hint: run 'pcke scan --deep' to extract import relations for richer " +
					"context (deep analysis supports Go, Java, JavaScript, and Python)")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&full, "full", false, "Force a full scan (rebuild all nodes)")
	cmd.Flags().BoolVar(&deep, "deep", false, "Extract AST entities and import relations (tree-sitter)")

	return cmd
}

func newSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Generate output files (.context/, copilot-instructions, etc.)",
		Long: `Generate context output files from the knowledge base.

Renders architecture, conventions, constraints, and module context into
files that AI coding agents can consume directly.

Examples:
  pcke sync                    Generate all output files
  pcke scan && pcke sync       Scan then sync in one go`,
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
	cmd := &cobra.Command{
		Use:   "rule",
		Short: "Manage project rules",
		Long: `Manage project rules extracted from @pcke-rule annotations in source code
and manually added rules.

Rules are discovered during scan from in-code annotations like:
  // @pcke-rule no-raw-sql: Never execute raw SQL; always use parameterized queries

Manual rules can be added with: pcke rule add "No hardcoded secrets"`,
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List rules extracted from source annotations and manual rules",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runRuleList()
		},
	})

	// rule add
	var scopeFlag, severityFlag string
	addCmd := &cobra.Command{
		Use:   "add [content]",
		Short: "Add a manual rule to the knowledge base",
		Long: `Add a rule manually. Rules added this way coexist with those
discovered from @pcke-rule annotations. They are distinguished by source=manual.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runRuleAdd(args[0], scopeFlag, severityFlag)
		},
	}
	addCmd.Flags().StringVar(&scopeFlag, "scope", "global", "Rule scope (global, module)")
	addCmd.Flags().StringVar(&severityFlag, "severity", "must", "Rule severity (must, should, may)")
	cmd.AddCommand(addCmd)

	// rule remove
	cmd.AddCommand(&cobra.Command{
		Use:   "remove [id]",
		Short: "Remove a manual rule from the knowledge base",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runRuleRemove(args[0])
		},
	})

	return cmd
}

func runRuleList() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("rule list: get working directory: %w", err)
	}

	db, err := kdb.Open(cwd, nil)
	if err != nil {
		return fmt.Errorf("rule list: open database: %w", err)
	}
	defer func() { _ = db.Close() }()
	warnBranchMismatch(db, cwd)

	nodes, err := output.LoadNodes(context.Background(), db)
	if err != nil {
		return fmt.Errorf("rule list: load nodes: %w", err)
	}

	var count int
	for _, n := range nodes {
		if n.Type == "rule" {
			count++
			source := "annotation"
			if n.Source == "manual" {
				source = "manual"
			}
			fmt.Printf("  [%s] %s  (%s)\n", source, n.Name, n.FilePath)
		}
	}
	if count == 0 {
		fmt.Println("No rules found. Add @pcke-rule annotations to your source files and run pcke scan.")
	} else {
		fmt.Printf("\n%d rule(s)\n", count)
	}
	return nil
}

func runRuleAdd(content, scope, severity string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("rule add: get working directory: %w", err)
	}

	db, err := kdb.Open(cwd, nil)
	if err != nil {
		return fmt.Errorf("rule add: open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	id := generateUUID()
	now := time.Now().UTC()

	node := map[string]any{
		"id":         id,
		"name":       content,
		"type":       "rule",
		"class":      "constraint",
		"source":     "manual",
		"scope":      scope,
		"severity":   severity,
		"status":     "active",
		"module":     "",
		"file_path":  "",
		"language":   "",
		"created_at": now.Format(time.RFC3339),
		"updated_at": now.Format(time.RFC3339),
	}

	data, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("rule add: marshal: %w", err)
	}

	if err := db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		return wtx.Put([]byte("kn:"+id), data)
	}); err != nil {
		return fmt.Errorf("rule add: %w", err)
	}

	fmt.Printf("Added rule %s\n", id)
	return nil
}

func runRuleRemove(id string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("rule remove: get working directory: %w", err)
	}

	db, err := kdb.Open(cwd, nil)
	if err != nil {
		return fmt.Errorf("rule remove: open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	key := []byte("kn:" + id)

	// Verify it exists and is a manual rule.
	var exists bool
	if err := db.View(context.Background(), func(rtx *tx.ReadTx) error {
		val, err := rtx.Get(key)
		if err != nil {
			return nil
		}
		var m map[string]any
		if err := json.Unmarshal(val, &m); err != nil {
			return nil
		}
		if m["type"] == "rule" && m["source"] == "manual" {
			exists = true
		}
		return nil
	}); err != nil {
		return fmt.Errorf("rule remove: %w", err)
	}

	if !exists {
		return fmt.Errorf("rule remove: manual rule %q not found", id)
	}

	if err := db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		return wtx.Delete(key)
	}); err != nil {
		return fmt.Errorf("rule remove: %w", err)
	}

	fmt.Printf("Removed rule %s\n", id)
	return nil
}

func newNoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "note",
		Short: "Manage project notes",
		Long: `Manage project notes stored in the knowledge base.

Notes can be queried with: pcke query "notes where tags contains 'decision'"`,
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List notes in the knowledge base",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runNoteList()
		},
	})

	// note add
	var tagsFlag string
	addCmd := &cobra.Command{
		Use:   "add [content]",
		Short: "Add a note to the knowledge base",
		Long:  `Add a note with optional tags. Content is required as positional argument.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runNoteAdd(args[0], tagsFlag)
		},
	}
	addCmd.Flags().StringVar(&tagsFlag, "tags", "", "Comma-separated tags (e.g. decision,arch)")
	cmd.AddCommand(addCmd)

	// note remove
	cmd.AddCommand(&cobra.Command{
		Use:   "remove [id]",
		Short: "Remove a note from the knowledge base",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runNoteRemove(args[0])
		},
	})

	return cmd
}

func runNoteList() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("note list: get working directory: %w", err)
	}

	db, err := kdb.Open(cwd, nil)
	if err != nil {
		return fmt.Errorf("note list: open database: %w", err)
	}
	defer func() { _ = db.Close() }()
	warnBranchMismatch(db, cwd)

	var count int
	if err := db.View(context.Background(), func(rtx *tx.ReadTx) error {
		prefix := []byte("nt:")
		c := rtx.Cursor()
		for ok := c.Seek(prefix); ok; ok = c.Next() {
			if !strings.HasPrefix(string(c.Key()), "nt:") {
				break
			}
			var m map[string]any
			if err := json.Unmarshal(c.Value(), &m); err != nil {
				continue
			}
			count++
			id, _ := m["id"].(string)
			content, _ := m["content"].(string)
			if len(content) > 80 {
				content = content[:80] + "..."
			}
			fmt.Printf("  %s: %s\n", id, content)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("note list: %w", err)
	}

	if count == 0 {
		fmt.Println("No notes found.")
	} else {
		fmt.Printf("\n%d note(s)\n", count)
	}
	return nil
}

func runNoteAdd(content, tagsFlag string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("note add: get working directory: %w", err)
	}

	db, err := kdb.Open(cwd, nil)
	if err != nil {
		return fmt.Errorf("note add: open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	id := generateUUID()
	now := time.Now().UTC()

	tags := []string{}
	if tagsFlag != "" {
		tags = strings.Split(tagsFlag, ",")
		for i := range tags {
			tags[i] = strings.TrimSpace(tags[i])
		}
	}

	note := map[string]any{
		"id":         id,
		"content":    content,
		"tags":       tags,
		"created_at": now.Format(time.RFC3339),
		"updated_at": now.Format(time.RFC3339),
	}

	data, err := json.Marshal(note)
	if err != nil {
		return fmt.Errorf("note add: marshal: %w", err)
	}

	if err := db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		return wtx.Put([]byte("nt:"+id), data)
	}); err != nil {
		return fmt.Errorf("note add: %w", err)
	}

	fmt.Printf("Added note %s\n", id)
	return nil
}

func runNoteRemove(id string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("note remove: get working directory: %w", err)
	}

	db, err := kdb.Open(cwd, nil)
	if err != nil {
		return fmt.Errorf("note remove: open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	key := []byte("nt:" + id)

	// Verify note exists before deleting.
	var exists bool
	if err := db.View(context.Background(), func(rtx *tx.ReadTx) error {
		_, err := rtx.Get(key)
		exists = err == nil
		return nil
	}); err != nil {
		return fmt.Errorf("note remove: %w", err)
	}

	if !exists {
		return fmt.Errorf("note remove: note %q not found", id)
	}

	if err := db.Update(context.Background(), func(wtx *tx.WriteTx) error {
		return wtx.Delete(key)
	}); err != nil {
		return fmt.Errorf("note remove: %w", err)
	}

	fmt.Printf("Removed note %s\n", id)
	return nil
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show knowledge base status",
		Long: `Show summary information about the knowledge base.

Displays schema version, total keys, knowledge node count, file size,
and tree depth.

Examples:
  pcke status`,
		RunE: func(_ *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("status: get working directory: %w", err)
			}

			db, err := kdb.Open(cwd, nil)
			if err != nil {
				return fmt.Errorf("status: open database: %w", err)
			}
			defer func() { _ = db.Close() }()
			warnBranchMismatch(db, cwd)

			stats, err := db.Stats()
			if err != nil {
				return fmt.Errorf("status: gather stats: %w", err)
			}

			nodes, err := output.LoadNodes(context.Background(), db)
			if err != nil {
				return fmt.Errorf("status: load nodes: %w", err)
			}

			fmt.Printf("Knowledge base: .pcke/data.kdb\n")
			fmt.Printf("  Schema version:  %d\n", stats.SchemaVersion)
			fmt.Printf("  Total keys:      %d\n", stats.KeyCount)
			fmt.Printf("  Knowledge nodes: %d\n", len(nodes))
			fmt.Printf("  Data file size:  %d bytes\n", stats.DataFileBytes)
			fmt.Printf("  Tree depth:      %d\n", stats.TreeDepth)
			return nil
		},
	}
}

func newModulesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "modules",
		Short: "List detected modules",
		Long: `List all modules detected in the knowledge base.

Modules are directory-level groupings inferred during scan. Each module
shows the number of source files it contains.

Examples:
  pcke modules
  pcke modules | grep internal`,
		RunE: func(_ *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("modules: get working directory: %w", err)
			}

			db, err := kdb.Open(cwd, nil)
			if err != nil {
				return fmt.Errorf("modules: open database: %w", err)
			}
			defer func() { _ = db.Close() }()
			warnBranchMismatch(db, cwd)

			nodes, err := output.LoadNodes(context.Background(), db)
			if err != nil {
				return fmt.Errorf("modules: load nodes: %w", err)
			}

			modules := make(map[string]int)
			for _, n := range nodes {
				if n.Module != "" {
					modules[n.Module]++
				}
			}

			if len(modules) == 0 {
				fmt.Println("No modules detected. Run pcke scan first.")
				return nil
			}

			// Collect and sort module names.
			names := make([]string, 0, len(modules))
			for name := range modules {
				names = append(names, name)
			}
			sort.Strings(names)

			for _, name := range names {
				fmt.Printf("  %-40s %d file(s)\n", name, modules[name])
			}
			fmt.Printf("\n%d module(s)\n", len(modules))
			return nil
		},
	}
}

func newDiagnosticsCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "diagnostics",
		Short: "Show database diagnostics",
		Long: `Show detailed database diagnostics including page stats, WAL state,
buffer pool metrics, and free space information.

Examples:
  pcke diagnostics                 Human-readable output
  pcke diagnostics --format=json   Machine-readable JSON`,
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
		Long: `View and manage pcke configuration.

Configuration is stored in .pcke/config.toml. Use get/set/list subcommands
to inspect and modify settings.

Examples:
  pcke config list
  pcke config get scan.max_file_bytes
  pcke config set kdb.buffer_pool_mb 64`,
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "get [key]",
			Short: "Get a configuration value",
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("config get: get working directory: %w", err)
				}

				cfg, err := config.Load(cwd)
				if err != nil {
					return fmt.Errorf("config get: %w", err)
				}

				val, ok := configGet(cfg, args[0])
				if !ok {
					return fmt.Errorf("config get: unknown key %q", args[0])
				}
				fmt.Println(val)
				return nil
			},
		},
		&cobra.Command{
			Use:   "set [key] [value]",
			Short: "Set a configuration value",
			Args:  cobra.ExactArgs(2),
			RunE: func(_ *cobra.Command, args []string) error {
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("config set: get working directory: %w", err)
				}

				cfg, err := config.Load(cwd)
				if err != nil {
					return fmt.Errorf("config set: %w", err)
				}

				if err := configSet(&cfg, args[0], args[1]); err != nil {
					return fmt.Errorf("config set: %w", err)
				}

				path := cwd + "/.pcke/config.toml"
				if err := os.MkdirAll(cwd+"/.pcke", 0o700); err != nil {
					return fmt.Errorf("config set: create .pcke dir: %w", err)
				}

				f, err := os.Create(path) //nolint:gosec // G304: path is constructed from cwd.
				if err != nil {
					return fmt.Errorf("config set: create config file: %w", err)
				}
				defer func() { _ = f.Close() }()

				if err := toml.NewEncoder(f).Encode(cfg); err != nil {
					return fmt.Errorf("config set: write config: %w", err)
				}

				fmt.Printf("%s = %s\n", args[0], args[1])
				return nil
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List all configuration values",
			RunE: func(_ *cobra.Command, _ []string) error {
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("config list: get working directory: %w", err)
				}

				cfg, err := config.Load(cwd)
				if err != nil {
					return fmt.Errorf("config list: %w", err)
				}

				return toml.NewEncoder(os.Stdout).Encode(cfg)
			},
		},
	)

	return cmd
}

func newRecallCmd() *cobra.Command {
	var limit int
	var format string
	var verbose bool

	cmd := &cobra.Command{
		Use:   "recall [query]",
		Short: "Search the knowledge base using full-text search",
		Long: `Search the knowledge base for nodes matching a natural language query.
Results are ranked using BM25 relevance scoring.

Examples:
  pcke recall "error handling"
  pcke recall --format=json "database connection"
  pcke recall --verbose "authentication"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			queryStr := strings.Join(args, " ")
			return runRecall(queryStr, limit, format, verbose)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 10, "Maximum number of results to return")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Show all node fields in results")

	return cmd
}

func runRecall(queryStr string, limit int, format string, verbose bool) error {
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

	// Build FTS index from knowledge nodes only.
	idx := fts.NewIndex()
	var docs []json.RawMessage // docID → raw JSON
	if err := db.View(context.Background(), func(rtx *tx.ReadTx) error {
		prefix := []byte("kn:")
		c := rtx.Cursor()
		for ok := c.Seek(prefix); ok; ok = c.Next() {
			if !strings.HasPrefix(string(c.Key()), "kn:") {
				break
			}
			raw := make([]byte, len(c.Value()))
			copy(raw, c.Value())
			idx.AddDocument(string(raw))
			docs = append(docs, json.RawMessage(raw))
		}
		return nil
	}); err != nil {
		return fmt.Errorf("recall: index documents: %w", err)
	}
	idx.Commit()

	planner := kdbquery.NewPlanner(idx)
	results := planner.Search(queryStr, limit)

	if len(results) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	if format == "json" {
		return printRecallJSON(results, docs)
	}
	return printRecallText(results, docs, verbose)
}

func printRecallText(results []kdbquery.Result, docs []json.RawMessage, verbose bool) error {
	for i, r := range results {
		if int(r.DocID) >= len(docs) { //nolint:gosec // G115: DocID is bounded by index size
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(docs[r.DocID], &m); err != nil {
			fmt.Printf("%d. [score=%.4f] (unparseable)\n", i+1, r.Score)
			continue
		}

		name, _ := m["name"].(string)
		filePath, _ := m["file_path"].(string)
		module, _ := m["module"].(string)
		nodeType, _ := m["type"].(string)

		fmt.Printf("%d. %s  [%s]  score=%.4f\n", i+1, name, nodeType, r.Score)
		if filePath != "" {
			fmt.Printf("   file: %s\n", filePath)
		}
		if module != "" {
			fmt.Printf("   module: %s\n", module)
		}
		if verbose {
			for k, v := range m {
				if k == "name" || k == "file_path" || k == "module" || k == "type" {
					continue
				}
				fmt.Printf("   %s: %v\n", k, v)
			}
		}
		if i < len(results)-1 {
			fmt.Println()
		}
	}
	fmt.Printf("\n%d result(s)\n", len(results))
	return nil
}

func printRecallJSON(results []kdbquery.Result, docs []json.RawMessage) error {
	type recallResult struct {
		Score float64         `json:"score"`
		Node  json.RawMessage `json:"node"`
	}
	out := make([]recallResult, 0, len(results))
	for _, r := range results {
		if int(r.DocID) >= len(docs) { //nolint:gosec // G115: DocID is bounded by index size
			continue
		}
		out = append(out, recallResult{Score: r.Score, Node: docs[r.DocID]})
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("recall: marshal JSON: %w", err)
	}
	fmt.Println(string(data))
	return nil
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

func newQueryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "query [dsl]",
		Short: "Query the knowledge base using the pcke DSL",
		Long: `Execute a structured query against the knowledge base.

Examples:
  pcke query "nodes where type = 'module' and stability > 0.7"
  pcke query "evolution where author = 'jesus' order by timestamp desc limit 10"
  pcke query "notes where tags contains 'decision'"`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runQuery(args[0], "text")
		},
	}
}

func newExplainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "explain [dsl]",
		Short: "Show the execution plan for a query",
		Long: `Parse and plan a query, then print the chosen execution strategy
without actually running the query.

Example:
  pcke explain "nodes where module = 'api' order by updated_at desc"`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			q, err := query.Parse(args[0])
			if err != nil {
				return fmt.Errorf("explain: %w", err)
			}
			if err := query.TypeCheck(q); err != nil {
				return fmt.Errorf("explain: %w", err)
			}
			plan := query.BuildPlan(q)
			fmt.Print(query.Explain(plan))
			return nil
		},
	}
}

func newExportCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "export [dsl]",
		Short: "Export query results as JSON or YAML",
		Long: `Execute a query and export the results in the specified format.

Examples:
  pcke export --format=json "constraints where scope = 'global'"
  pcke export --format=yaml "nodes where type = 'module'"`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runQuery(args[0], format)
		},
	}

	cmd.Flags().StringVar(&format, "format", "json", "Output format: json or yaml")

	return cmd
}

// runQuery parses, validates, plans, and executes a DSL query, then prints
// results in the specified format (text, json, yaml).
func runQuery(dsl, format string) error {
	q, err := query.Parse(dsl)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	if err := query.TypeCheck(q); err != nil {
		return fmt.Errorf("query: %w", err)
	}

	plan := query.BuildPlan(q)

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("query: get working directory: %w", err)
	}

	db, err := kdb.Open(cwd, nil)
	if err != nil {
		return fmt.Errorf("query: open database: %w", err)
	}
	defer func() { _ = db.Close() }()
	warnBranchMismatch(db, cwd)

	rs, err := query.Execute(context.Background(), db, plan)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}

	switch format {
	case "json":
		return printJSON(rs)
	case "yaml":
		return printYAML(rs)
	default:
		return printText(rs)
	}
}

func printJSON(rs *query.ResultSet) error {
	data, err := json.MarshalIndent(rs.Rows, "", "  ")
	if err != nil {
		return fmt.Errorf("query: marshal JSON: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func printYAML(rs *query.ResultSet) error {
	for i, row := range rs.Rows {
		if i > 0 {
			fmt.Println("---")
		}
		for k, v := range row {
			fmt.Printf("%s: %v\n", k, yamlValue(v))
		}
	}
	return nil
}

// yamlValue formats a value for simple YAML output.
func yamlValue(v any) string {
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("%q", val)
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val)) //nolint:gosec // G115: value range is safe.
		}
		return fmt.Sprintf("%g", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case []any:
		parts := make([]string, len(val))
		for i, elem := range val {
			parts[i] = fmt.Sprintf("  - %v", elem)
		}
		return "\n" + strings.Join(parts, "\n")
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%v", val)
	}
}

func printText(rs *query.ResultSet) error {
	if len(rs.Rows) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	for i, row := range rs.Rows {
		fmt.Printf("--- result %d ---\n", i+1)
		for k, v := range row {
			fmt.Printf("  %s: %v\n", k, v)
		}
	}
	fmt.Printf("\n%d result(s)\n", len(rs.Rows))
	return nil
}

func newSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Inspect the knowledge base schema",
		Long: `Show available collections and their fields.

Examples:
  pcke schema collections              List all queryable collections
  pcke schema describe nodes           Show fields for the nodes collection`,
	}

	cmd.AddCommand(&cobra.Command{
		Use:     "collections",
		Short:   "List all queryable collections",
		Aliases: []string{"list"},
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			for _, name := range query.Collections() {
				schema := query.CollectionSchema(name)
				fmt.Printf("  %-14s (%d fields)\n", name, len(schema))
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "describe [collection]",
		Short: "Show fields and types for a collection",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			schema := query.CollectionSchema(args[0])
			if schema == nil {
				return fmt.Errorf("unknown collection %q; run 'pcke schema collections' to see available ones", args[0])
			}
			fmt.Printf("Collection: %s\n\n", args[0])
			for _, field := range schema.FieldNames() {
				fmt.Printf("  %-16s %s\n", field, schema[field])
			}
			return nil
		},
	})

	cmd.AddCommand(newAlterCmd())

	return cmd
}

func newMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Run schema migrations on the knowledge base",
		Long: `Run schema migrations on the knowledge base.

Migrations are versioned, chunked (safe for large databases), and idempotent
(running twice has the same effect as running once).`,
		RunE: func(_ *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("migrate: get working directory: %w", err)
			}

			db, err := kdb.Open(cwd, nil)
			if err != nil {
				return fmt.Errorf("migrate: open database: %w", err)
			}
			defer func() { _ = db.Close() }()

			ctx := context.Background()

			before := db.SchemaVersion()
			applied, err := applyPendingMigrations(ctx, db)
			if err != nil {
				return fmt.Errorf("migrate: %w", err)
			}

			if applied == 0 {
				fmt.Printf("Database is up to date (schema version %d).\n", before)
			} else {
				fmt.Printf("Applied %d migration(s): version %d → %d.\n",
					applied, before, db.SchemaVersion())
			}

			return nil
		},
	}
}

// registerMigrations adds all known schema migrations to the engine.
// New migrations should be appended here as pcke evolves.
func registerMigrations(e *migrate.Engine) {
	// v10 — F12.T1 typed-event log baseline. Schema-version bump only;
	// data translation lands in F12.T6 as migrations 0011–0014.
	e.Register(migrate.V0010EventBaseline())
	// v11 — F12.T6: translate legacy kn: knowledge-node records into
	// typed Entity (e:) events. Idempotent; legacy records preserved.
	e.Register(migrate.V0011MigrateKnToE())
	// v12 — F12.T6: translate legacy rel: relation records into typed
	// Link (l:) events with paired reverse-index (lr:) records.
	e.Register(migrate.V0012MigrateRelToL())
	// v13 — F12.T6: translate legacy nt: note records into typed
	// Decision (d:) events with severity=should, scope=global, source=manual.
	e.Register(migrate.V0013MigrateNtToD())
	// v14 — F12.T6: retire legacy el: evolution-log records (regenerated
	// by the next scan from git history; new schema captures evolution
	// as the version chain on Entity events).
	e.Register(migrate.V0014RetireEvolutionLog())
	// v15 — F14.T1: register Phase 14 Observation sub-types
	// (o:session:<uuid>, o:call:<uuid>) and the edge types
	// contains/served/belongs_to. Pure version marker; sub-type writers
	// land in F14.T2.
	e.Register(migrate.V0015SessionBaseline())
}

// applyPendingMigrations runs any pending schema migrations on db. It is
// idempotent and cheap when the schema is current (no pending work =>
// returns 0). Wired into scan and serve so a database written by an older
// pcke (legacy kn:/rel:/nt: records, empty typed-event log) is upgraded
// automatically, without the user having to know about `pcke migrate`.
// Read-only commands deliberately do not call this.
func applyPendingMigrations(ctx context.Context, db *kdb.DB) (int, error) {
	engine := migrate.New()
	registerMigrations(engine)
	applied, err := engine.Run(ctx, db)
	if err != nil {
		return applied, err
	}
	// engine.Run bumps the schema version in memory; a checkpoint forces
	// the meta page to disk even when the migrations wrote no records
	// (e.g. an empty database), so the version isn't re-run on next open.
	if applied > 0 {
		if cErr := db.Checkpoint(ctx); cErr != nil {
			return applied, fmt.Errorf("persist schema version: %w", cErr)
		}
	}
	return applied, nil
}

// errFoundEntity short-circuits the eventLogEmpty scan on the first entity.
var errFoundEntity = fmt.Errorf("found entity")

// eventLogEmpty reports whether the typed-event log has no Entity records.
// It stops at the first entity, so the cost is one cursor seek on a
// populated database. Any scan error is treated as "not empty" to avoid a
// misleading warning.
func eventLogEmpty(ctx context.Context, db *kdb.DB) bool {
	store := event.New(db)
	err := store.IterateKind(ctx, event.KindEntity, func(event.Event) error {
		return errFoundEntity
	})
	return err == nil
}

// configGet returns the value of a dotted config key (e.g. "scan.redact_secrets").
func configGet(cfg config.Config, key string) (string, bool) {
	getters := configGetters()
	fn, ok := getters[key]
	if !ok {
		return "", false
	}
	return fn(cfg), true
}

func configGetters() map[string]func(config.Config) string {
	return map[string]func(config.Config) string{
		"scan.redact_secrets":         func(c config.Config) string { return strconv.FormatBool(c.Scan.RedactSecrets) },
		"scan.include_ignored":        func(c config.Config) string { return strconv.FormatBool(c.Scan.IncludeIgnored) },
		"scan.max_file_bytes":         func(c config.Config) string { return strconv.FormatInt(c.Scan.MaxFileBytes, 10) },
		"scan.exclude_globs":          func(c config.Config) string { return strings.Join(c.Scan.ExcludeGlobs, ", ") },
		"kdb.buffer_pool_mb":          func(c config.Config) string { return strconv.Itoa(c.KDB.BufferPoolMB) },
		"kdb.wal_segment_mb":          func(c config.Config) string { return strconv.Itoa(c.KDB.WALSegmentMB) },
		"kdb.checkpoint_wal_mb":       func(c config.Config) string { return strconv.Itoa(c.KDB.CheckpointWALMB) },
		"kdb.checkpoint_interval_sec": func(c config.Config) string { return strconv.Itoa(c.KDB.CheckpointIntervalS) },
		"kdb.graceful_shutdown_sec":   func(c config.Config) string { return strconv.Itoa(c.KDB.GracefulShutdownS) },
		"fts.tokenizer_cjk_mode":      func(c config.Config) string { return c.FTS.TokenizerCJKMode },
		"fts.merge_tier_threshold":    func(c config.Config) string { return strconv.Itoa(c.FTS.MergeTierThreshold) },
		"mcp.read_timeout_sec":        func(c config.Config) string { return strconv.Itoa(c.MCP.ReadTimeoutS) },
		"mcp.proactive_context":       func(c config.Config) string { return strconv.FormatBool(c.MCP.ProactiveContext) },
		"mcp.stream_threshold":        func(c config.Config) string { return strconv.Itoa(c.MCP.StreamThreshold) },
		"mcp.chunk_size":              func(c config.Config) string { return strconv.Itoa(c.MCP.ChunkSize) },
	}
}

// configSet updates a dotted config key in the given Config struct.
func configSet(cfg *config.Config, key, value string) error {
	setters := configSetters()
	fn, ok := setters[key]
	if !ok {
		return fmt.Errorf("unknown key %q", key)
	}
	return fn(cfg, value)
}

func configSetters() map[string]func(*config.Config, string) error {
	parseInt := func(v string) (int, error) { return strconv.Atoi(v) }
	return map[string]func(*config.Config, string) error{
		"scan.redact_secrets":  func(c *config.Config, v string) error { c.Scan.RedactSecrets = parseBool(v); return nil },
		"scan.include_ignored": func(c *config.Config, v string) error { c.Scan.IncludeIgnored = parseBool(v); return nil },
		"scan.max_file_bytes": func(c *config.Config, v string) error {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return err
			}
			c.Scan.MaxFileBytes = n
			return nil
		},
		"kdb.buffer_pool_mb": func(c *config.Config, v string) error {
			n, err := parseInt(v)
			if err != nil {
				return err
			}
			c.KDB.BufferPoolMB = n
			return nil
		},
		"kdb.wal_segment_mb": func(c *config.Config, v string) error {
			n, err := parseInt(v)
			if err != nil {
				return err
			}
			c.KDB.WALSegmentMB = n
			return nil
		},
		"kdb.checkpoint_wal_mb": func(c *config.Config, v string) error {
			n, err := parseInt(v)
			if err != nil {
				return err
			}
			c.KDB.CheckpointWALMB = n
			return nil
		},
		"kdb.checkpoint_interval_sec": func(c *config.Config, v string) error {
			n, err := parseInt(v)
			if err != nil {
				return err
			}
			c.KDB.CheckpointIntervalS = n
			return nil
		},
		"kdb.graceful_shutdown_sec": func(c *config.Config, v string) error {
			n, err := parseInt(v)
			if err != nil {
				return err
			}
			c.KDB.GracefulShutdownS = n
			return nil
		},
		"fts.tokenizer_cjk_mode": func(c *config.Config, v string) error { c.FTS.TokenizerCJKMode = v; return nil },
		"fts.merge_tier_threshold": func(c *config.Config, v string) error {
			n, err := parseInt(v)
			if err != nil {
				return err
			}
			c.FTS.MergeTierThreshold = n
			return nil
		},
		"mcp.read_timeout_sec": func(c *config.Config, v string) error {
			n, err := parseInt(v)
			if err != nil {
				return err
			}
			c.MCP.ReadTimeoutS = n
			return nil
		},
		"mcp.proactive_context": func(c *config.Config, v string) error { c.MCP.ProactiveContext = parseBool(v); return nil },
		"mcp.stream_threshold": func(c *config.Config, v string) error {
			n, err := parseInt(v)
			if err != nil {
				return err
			}
			c.MCP.StreamThreshold = n
			return nil
		},
		"mcp.chunk_size": func(c *config.Config, v string) error {
			n, err := parseInt(v)
			if err != nil {
				return err
			}
			c.MCP.ChunkSize = n
			return nil
		},
	}
}

func parseBool(v string) bool {
	switch strings.ToLower(v) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the MCP server on stdio",
		Long: `Start the MCP (Model Context Protocol) server on stdio transport.

The server exposes pcke's knowledge base to AI agents via the MCP protocol.
It blocks until stdin closes or a termination signal is received.

Tools:     recall, get_module_context, get_constraints, get_history
Resources: pcke://architecture, pcke://constraints, pcke://decisions
Prompts:   onboarding, review, debug, refactor

Examples:
  pcke serve                     Start MCP server on stdio
  echo '{}' | pcke serve        Test connectivity`,
		RunE: func(_ *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("serve: get working directory: %w", err)
			}

			if _, err := os.Stat(cwd + "/.pcke"); os.IsNotExist(err) {
				return fmt.Errorf("serve: no knowledge base found; run 'pcke init' first")
			}

			db, err := kdb.Open(cwd, nil)
			if err != nil {
				return fmt.Errorf("serve: open database: %w", err)
			}
			defer func() { _ = db.Close() }()

			// Upgrade an older database so the typed-event tools have data.
			// A migration failure is non-fatal: log and serve what we have.
			if applied, mErr := applyPendingMigrations(context.Background(), db); mErr != nil {
				fmt.Fprintf(os.Stderr, "pcke serve: migration warning: %v\n", mErr)
			} else if applied > 0 {
				fmt.Fprintf(os.Stderr, "pcke serve: applied %d schema migration(s)\n", applied)
			}

			// Warn (don't block) if the typed-event log is empty: every
			// context/graph/history tool would return nothing until a scan.
			if eventLogEmpty(context.Background(), db) {
				fmt.Fprintln(os.Stderr,
					"pcke serve: typed-event log is empty — run 'pcke scan' so tools like "+
						"get_context_for_file return results (use 'pcke scan --deep' for import relations)")
			}

			// Phase 14 F14.T6 — run the retention prune in the background
			// on startup. Failures are logged but do not block serving.
			runRetentionPrune(cwd, db)

			srv := mcp.New(db, cwd)
			return srv.Serve()
		},
	}
}

func newRelationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "relations",
		Short: "Explore module and node relationships",
		Long: `Explore the dependency graph between modules and nodes in the knowledge base.

Examples:
  pcke relations list                         List all relations
  pcke relations list --module=internal/kdb   Filter by module
  pcke relations list --type=imports          Filter by relation type
  pcke relations graph                        Show module dependency graph`,
	}

	// relations list
	var moduleFilter, typeFilter string
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List relations in the knowledge base",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runRelationsList(moduleFilter, typeFilter)
		},
	}
	listCmd.Flags().StringVar(&moduleFilter, "module", "", "Filter by module name")
	listCmd.Flags().StringVar(&typeFilter, "type", "", "Filter by relation type")

	// relations graph
	graphCmd := &cobra.Command{
		Use:   "graph",
		Short: "Show module dependency graph as text art",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runRelationsGraph()
		},
	}

	cmd.AddCommand(listCmd, graphCmd)
	return cmd
}

func runRelationsList(moduleFilter, typeFilter string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("relations list: get working directory: %w", err)
	}

	db, err := kdb.Open(cwd, nil)
	if err != nil {
		return fmt.Errorf("relations list: open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.View(context.Background(), func(rtx *tx.ReadTx) error {
		prefix := []byte("rel:")
		c := rtx.Cursor()
		for ok := c.Seek(prefix); ok; ok = c.Next() {
			if !strings.HasPrefix(string(c.Key()), "rel:") {
				break
			}
			var m map[string]any
			if err := json.Unmarshal(c.Value(), &m); err != nil {
				continue
			}

			// Apply filters.
			if moduleFilter != "" {
				src, _ := m["source_node_id"].(string)
				tgt, _ := m["target_node_id"].(string)
				if !strings.Contains(src, moduleFilter) && !strings.Contains(tgt, moduleFilter) {
					continue
				}
			}
			if typeFilter != "" {
				relType, _ := m["type"].(string)
				if relType != typeFilter {
					continue
				}
			}

			count++
			relType, _ := m["type"].(string)
			src, _ := m["source_node_id"].(string)
			tgt, _ := m["target_node_id"].(string)
			fmt.Printf("  %s → %s  [%s]\n", src, tgt, relType)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("relations list: %w", err)
	}

	if count == 0 {
		fmt.Println("No relations found. Run pcke scan to discover dependencies.")
	} else {
		fmt.Printf("\n%d relation(s)\n", count)
	}
	return nil
}

func runRelationsGraph() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("relations graph: get working directory: %w", err)
	}

	db, err := kdb.Open(cwd, nil)
	if err != nil {
		return fmt.Errorf("relations graph: open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Build adjacency map: module → [dependencies]
	edges := make(map[string][]string)
	if err := db.View(context.Background(), func(rtx *tx.ReadTx) error {
		prefix := []byte("rel:")
		c := rtx.Cursor()
		for ok := c.Seek(prefix); ok; ok = c.Next() {
			if !strings.HasPrefix(string(c.Key()), "rel:") {
				break
			}
			var m map[string]any
			if err := json.Unmarshal(c.Value(), &m); err != nil {
				continue
			}
			src, _ := m["source_node_id"].(string)
			tgt, _ := m["target_node_id"].(string)
			relType, _ := m["type"].(string)
			if relType == "imports" || relType == "depends_on" {
				edges[src] = append(edges[src], tgt)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("relations graph: %w", err)
	}

	if len(edges) == 0 {
		fmt.Println("No module dependencies found.")
		return nil
	}

	// Print text art graph sorted by source.
	modules := make([]string, 0, len(edges))
	for k := range edges {
		modules = append(modules, k)
	}
	sort.Strings(modules)

	for _, mod := range modules {
		deps := edges[mod]
		sort.Strings(deps)
		fmt.Printf("%s\n", mod)
		for i, dep := range deps {
			if i == len(deps)-1 {
				fmt.Printf("  └── %s\n", dep)
			} else {
				fmt.Printf("  ├── %s\n", dep)
			}
		}
	}
	return nil
}

func newCleanCmd() *cobra.Command {
	var forceFlag bool
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Remove the knowledge base",
		Long: `Remove the .pcke/ directory and all knowledge base data.

Requires confirmation unless --force is passed. Refuses to run outside a git repository.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("clean: get working directory: %w", err)
			}

			// Safety: refuse outside git repo.
			if _, err := os.Stat(cwd + "/.git"); os.IsNotExist(err) {
				return fmt.Errorf("clean: not a git repository; refusing to delete .pcke/")
			}

			pckeDir := cwd + "/.pcke"
			if _, err := os.Stat(pckeDir); os.IsNotExist(err) {
				fmt.Println("Nothing to clean: .pcke/ does not exist.")
				return nil
			}

			if !forceFlag {
				fmt.Print("Remove .pcke/ and all knowledge base data? [y/N] ")
				var answer string
				fmt.Scanln(&answer) //nolint:errcheck // Best-effort interactive prompt.
				if strings.ToLower(answer) != "y" {
					fmt.Println("Aborted.")
					return nil
				}
			}

			if err := os.RemoveAll(pckeDir); err != nil {
				return fmt.Errorf("clean: remove .pcke/: %w", err)
			}

			fmt.Println("Removed .pcke/")
			return nil
		},
	}
	cmd.Flags().BoolVar(&forceFlag, "force", false, "Skip confirmation prompt")
	return cmd
}

// generateUUID returns a new random UUID v4 string.
func generateUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 2
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func newWatchCmd() *cobra.Command {
	var syncFlag, verbose bool

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch for file changes and auto-scan",
		Long: `Watch the repository for file changes and automatically run incremental scans.

Changes are debounced (500ms) to avoid redundant scans during batch edits.
The watcher respects .gitignore patterns and skips hidden directories.

Press Ctrl+C to stop.

Examples:
  pcke watch                  Watch and scan on changes
  pcke watch --sync           Also regenerate output files after each scan
  pcke watch --verbose        Print all scan results (even no-ops)`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runWatch(syncFlag, verbose)
		},
	}

	cmd.Flags().BoolVar(&syncFlag, "sync", false, "Run sync after each scan")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Print scan results even when nothing changed")

	return cmd
}

func runWatch(syncFlag, verbose bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("watch: get working directory: %w", err)
	}

	db, err := kdb.Open(cwd, nil)
	if err != nil {
		return fmt.Errorf("watch: open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	cfg := config.Defaults().Scan
	opts := analysis.WatcherOpts{
		Verbose: verbose,
	}

	if syncFlag {
		opts.OnScan = func(_ *analysis.ScanResult) {
			renderer := output.NewRenderer(cwd, db)
			syncResult, syncErr := renderer.Sync(context.Background())
			if syncErr != nil {
				fmt.Fprintf(os.Stderr, "watch: sync error: %v\n", syncErr)
				return
			}
			if verbose || syncResult.FilesWritten > 0 {
				fmt.Printf("[watch] sync: %d files written\n", syncResult.FilesWritten)
			}
		}
	}

	w, err := analysis.NewWatcher(cwd, db, cfg, opts)
	if err != nil {
		return fmt.Errorf("watch: %w", err)
	}

	fmt.Println("Watching for changes... (Ctrl+C to stop)")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	return w.Run(ctx)
}

func newShellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell",
		Short: "Interactive query shell",
		Long: `Start an interactive REPL for querying the knowledge base.

Built-in commands:
  .collections          List queryable collections
  .describe <name>      Show fields for a collection
  .export json          Export last query results as JSON
  .help                 Show this help
  .quit                 Exit the shell

Any other input is parsed as a DSL query and executed.

Examples:
  pcke shell
  pcke> nodes where type = 'module'
  pcke> .describe nodes
  pcke> .quit`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runShell()
		},
	}
}

func runShell() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("shell: get working directory: %w", err)
	}

	db, err := kdb.Open(cwd, nil)
	if err != nil {
		return fmt.Errorf("shell: open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	fmt.Println("pcke interactive shell. Type .help for commands, .quit to exit.")

	sc := bufio.NewScanner(os.Stdin)
	var lastResults *query.ResultSet

	for {
		fmt.Print("pcke> ")
		if !sc.Scan() {
			break
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		rs, quit := shellDispatch(db, line, lastResults)
		if quit {
			return nil
		}
		if rs != nil {
			lastResults = rs
		}
	}
	return nil
}

func shellDispatch(db *kdb.DB, line string, lastResults *query.ResultSet) (*query.ResultSet, bool) {
	switch {
	case line == ".quit" || line == ".exit":
		return nil, true
	case line == ".help":
		printShellHelp()
	case line == ".collections":
		shellListCollections()
	case strings.HasPrefix(line, ".describe "):
		shellDescribe(strings.TrimSpace(strings.TrimPrefix(line, ".describe ")))
	case line == ".export json":
		shellExportJSON(lastResults)
	default:
		return shellExecQuery(db, line), false
	}
	return nil, false
}

func shellListCollections() {
	for _, name := range query.Collections() {
		schema := query.CollectionSchema(name)
		fmt.Printf("  %-14s (%d fields)\n", name, len(schema))
	}
}

func shellDescribe(name string) {
	schema := query.CollectionSchema(name)
	if schema == nil {
		fmt.Printf("Unknown collection %q. Use .collections to list.\n", name)
		return
	}
	fmt.Printf("Collection: %s\n", name)
	for _, field := range schema.FieldNames() {
		fmt.Printf("  %-16s %s\n", field, schema[field])
	}
}

func shellExportJSON(lastResults *query.ResultSet) {
	if lastResults == nil || len(lastResults.Rows) == 0 {
		fmt.Println("No results to export. Run a query first.")
		return
	}
	data, err := json.MarshalIndent(lastResults.Rows, "", "  ")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Println(string(data))
}

func shellExecQuery(db *kdb.DB, line string) *query.ResultSet {
	start := time.Now()
	rs, err := executeShellQuery(db, line)
	elapsed := time.Since(start)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return nil
	}
	printShellResults(rs, elapsed)
	return rs
}

func executeShellQuery(db *kdb.DB, dsl string) (*query.ResultSet, error) {
	q, err := query.Parse(dsl)
	if err != nil {
		return nil, err
	}
	if err := query.TypeCheck(q); err != nil {
		return nil, err
	}
	plan := query.BuildPlan(q)
	return query.Execute(context.Background(), db, plan)
}

func printShellResults(rs *query.ResultSet, elapsed time.Duration) {
	if len(rs.Rows) == 0 {
		fmt.Printf("No results. (%s)\n", elapsed.Round(time.Microsecond))
		return
	}
	for i, row := range rs.Rows {
		fmt.Printf("--- result %d ---\n", i+1)
		for k, v := range row {
			fmt.Printf("  %s: %v\n", k, v)
		}
	}
	fmt.Printf("\n%d result(s) in %s\n", len(rs.Rows), elapsed.Round(time.Microsecond))
}

func printShellHelp() {
	fmt.Println(`Built-in commands:
  .collections          List queryable collections
  .describe <name>      Show fields for a collection
  .export json          Export last query results as JSON
  .help                 Show this help
  .quit                 Exit the shell

Any other input is executed as a DSL query.`)
}

func newOnboardCmd() *cobra.Command {
	var (
		format     string
		module     string
		depth      string
		outputFile string
	)

	cmd := &cobra.Command{
		Use:   "onboard",
		Short: "Generate a project walkthrough for new developers",
		Long: `Generate an interactive, auto-guided walkthrough combining architecture,
key modules, conventions, constraints, and entry points.

Examples:
  pcke onboard
  pcke onboard --format=markdown
  pcke onboard --module=internal/kdb
  pcke onboard --output=file
  pcke onboard --depth=shallow`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runOnboard(format, module, depth, outputFile)
		},
	}

	cmd.Flags().StringVar(&format, "format", "text", "Output format: text, json, or markdown")
	cmd.Flags().StringVar(&module, "module", "", "Scope walkthrough to a specific module")
	cmd.Flags().StringVar(&depth, "depth", "full", "Walkthrough depth: shallow or full")
	cmd.Flags().StringVar(&outputFile, "output", "", "Write to file (use 'file' for ONBOARDING.md)")

	return cmd
}

func runOnboard(format, module, depth, outputFile string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("onboard: get working directory: %w", err)
	}

	db, err := kdb.Open(cwd, nil)
	if err != nil {
		return fmt.Errorf("onboard: open database: %w", err)
	}
	defer func() { _ = db.Close() }()
	warnBranchMismatch(db, cwd)

	ctx := context.Background()
	nodes, err := output.LoadNodes(ctx, db)
	if err != nil {
		return fmt.Errorf("onboard: load nodes: %w", err)
	}

	rels, err := loadRelationsFromDB(ctx, db)
	if err != nil {
		return fmt.Errorf("onboard: load relations: %w", err)
	}

	logs, err := loadEvolutionLogsFromDB(ctx, db)
	if err != nil {
		return fmt.Errorf("onboard: load evolution logs: %w", err)
	}

	decisions, err := output.LoadDecisions(ctx, db)
	if err != nil {
		return fmt.Errorf("onboard: load decisions: %w", err)
	}

	cfg, err := onboard.LoadConfig(cwd)
	if err != nil {
		return fmt.Errorf("onboard: load config: %w", err)
	}

	engine := &onboard.Engine{
		Nodes:     nodes,
		Relations: rels,
		EvolLogs:  logs,
		Decisions: decisions,
		RepoPath:  cwd,
		Config:    cfg,
	}

	var w *onboard.Walkthrough
	if module != "" {
		w, err = engine.GenerateForModule(ctx, module)
	} else {
		w, err = engine.Generate(ctx)
	}
	if err != nil {
		return fmt.Errorf("onboard: generate walkthrough: %w", err)
	}

	if depth == "shallow" && len(w.Sections) > 3 {
		w.Sections = w.Sections[:3]
	}

	return outputWalkthrough(w, format, outputFile)
}

func outputWalkthrough(w *onboard.Walkthrough, format, outputFile string) error {
	var rendered string
	var err error
	switch format {
	case "json":
		rendered, err = onboard.RenderJSON(w)
		if err != nil {
			return err
		}
	case "markdown":
		rendered = onboard.RenderMarkdown(w)
	default:
		rendered = onboard.RenderText(w)
	}

	if outputFile == "file" {
		outputFile = "ONBOARDING.md"
	}
	if outputFile != "" {
		outContent := rendered
		if format == "text" {
			outContent = onboard.RenderMarkdown(w)
		}
		if err := os.WriteFile(outputFile, []byte(outContent), 0o600); err != nil {
			return fmt.Errorf("onboard: write file: %w", err)
		}
		fmt.Printf("Walkthrough written to %s\n", outputFile)
		return nil
	}

	fmt.Print(rendered)
	return nil
}

func loadRelationsFromDB(ctx context.Context, db *kdb.DB) ([]analysis.Relation, error) {
	var rels []analysis.Relation
	if err := db.View(ctx, func(rtx *tx.ReadTx) error {
		prefix := []byte("rel:")
		cursor := rtx.Cursor()
		for ok := cursor.Seek(prefix); ok; ok = cursor.Next() {
			k := cursor.Key()
			if !strings.HasPrefix(string(k), "rel:") {
				break
			}
			var rel analysis.Relation
			if err := json.Unmarshal(cursor.Value(), &rel); err != nil {
				continue
			}
			rels = append(rels, rel)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return rels, nil
}

func loadEvolutionLogsFromDB(ctx context.Context, db *kdb.DB) ([]analysis.EvolutionLog, error) {
	var logs []analysis.EvolutionLog
	if err := db.View(ctx, func(rtx *tx.ReadTx) error {
		prefix := []byte("el:")
		cursor := rtx.Cursor()
		for ok := cursor.Seek(prefix); ok; ok = cursor.Next() {
			k := cursor.Key()
			if !strings.HasPrefix(string(k), "el:") {
				break
			}
			var el analysis.EvolutionLog
			if err := json.Unmarshal(cursor.Value(), &el); err != nil {
				continue
			}
			logs = append(logs, el)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return logs, nil
}

// ── ALTER subcommand ──────────────────────────────────────────────────

func newAlterCmd() *cobra.Command {
	var (
		addField      string
		addCollection string
		fields        string
		defaultVal    string
		indexed       bool
		dryRun        bool
	)

	cmd := &cobra.Command{
		Use:   "alter",
		Short: "Online schema evolution: add fields or collections",
		Long: `Apply additive schema changes without requiring offline migrations.

  Supported operations:
    --add-field <collection>.<field>:<type>    Add a field to an existing collection
    --add-collection <name> --fields f1:t1,f2:t2,...  Add a new collection

  Types: string, number, int, float, bool, time, []string

  Examples:
    pcke schema alter --add-field nodes.priority:number
    pcke schema alter --add-field nodes.priority:number --default=0 --indexed
    pcke schema alter --add-field nodes.priority:number --dry-run
    pcke schema alter --add-collection metrics --fields id:string,value:number`,
		RunE: func(_ *cobra.Command, _ []string) error {
			if addField == "" && addCollection == "" {
				return fmt.Errorf("specify --add-field or --add-collection")
			}
			if addField != "" && addCollection != "" {
				return fmt.Errorf("specify only one of --add-field or --add-collection")
			}

			if addField != "" {
				return runAlterAddField(addField, defaultVal, indexed, dryRun)
			}
			return runAlterAddCollection(addCollection, fields, dryRun)
		},
	}

	cmd.Flags().StringVar(&addField, "add-field", "", "Add field: collection.field:type")
	cmd.Flags().StringVar(&addCollection, "add-collection", "", "Add a new collection")
	cmd.Flags().StringVar(&fields, "fields", "", "Fields for new collection: f1:t1,f2:t2,...")
	cmd.Flags().StringVar(&defaultVal, "default", "", "Default value for new field")
	cmd.Flags().BoolVar(&indexed, "indexed", false, "Create index for new field")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show impact without mutating")

	return cmd
}

func runAlterAddField(spec, defaultVal string, indexed, dryRun bool) error {
	// Parse "collection.field:type".
	dotIdx := strings.IndexByte(spec, '.')
	if dotIdx < 0 {
		return fmt.Errorf("invalid --add-field format: expected collection.field:type, got %q", spec)
	}
	collection := spec[:dotIdx]
	rest := spec[dotIdx+1:]

	colonIdx := strings.IndexByte(rest, ':')
	if colonIdx < 0 {
		return fmt.Errorf("invalid --add-field format: expected collection.field:type, got %q", spec)
	}
	field := rest[:colonIdx]
	typeName := rest[colonIdx+1:]

	ft, err := query.ParseFieldType(typeName)
	if err != nil {
		return err
	}

	op := &migrate.AlterOp{
		Type:       migrate.AddField,
		Collection: collection,
		Field:      field,
		FieldType:  ft,
		Indexed:    indexed,
	}

	if defaultVal != "" {
		op.Default = parseDefault(defaultVal, ft)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("alter: get working directory: %w", err)
	}

	db, err := kdb.Open(cwd, nil)
	if err != nil {
		return fmt.Errorf("alter: open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	if dryRun {
		report, err := migrate.AnalyzeImpact(context.Background(), db, op)
		if err != nil {
			return fmt.Errorf("alter: dry-run: %w", err)
		}
		printImpactReport(report, op)
		return nil
	}

	if err := migrate.Apply(context.Background(), db, op); err != nil {
		return fmt.Errorf("alter: %w", err)
	}
	fmt.Printf("ALTER ADD FIELD %s.%s:%s applied (schema version → %d)\n",
		collection, field, typeName, db.SchemaVersion())

	// Run backfill if there are records.
	count, err := migrate.Backfill(context.Background(), db, op, 500)
	if err != nil {
		return fmt.Errorf("alter: backfill: %w", err)
	}
	if count > 0 {
		fmt.Printf("Backfilled %d records\n", count)
	}

	return nil
}

func runAlterAddCollection(name, fieldsSpec string, dryRun bool) error {
	if fieldsSpec == "" {
		return fmt.Errorf("--fields required for --add-collection")
	}

	schema, err := parseFieldsSpec(fieldsSpec)
	if err != nil {
		return err
	}

	// Generate prefix from first 2 chars + ":"
	prefix := name
	if len(prefix) > 3 {
		prefix = prefix[:3]
	}
	prefix += ":"

	op := &migrate.AlterOp{
		Type:       migrate.AddCollection,
		Collection: name,
		Fields:     schema,
		Prefix:     prefix,
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("alter: get working directory: %w", err)
	}

	db, err := kdb.Open(cwd, nil)
	if err != nil {
		return fmt.Errorf("alter: open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	if dryRun {
		report, err := migrate.AnalyzeImpact(context.Background(), db, op)
		if err != nil {
			return fmt.Errorf("alter: dry-run: %w", err)
		}
		printImpactReport(report, op)
		return nil
	}

	if err := migrate.Apply(context.Background(), db, op); err != nil {
		return fmt.Errorf("alter: %w", err)
	}
	fmt.Printf("ALTER ADD COLLECTION %s applied (schema version → %d)\n",
		name, db.SchemaVersion())

	return nil
}

func parseFieldsSpec(spec string) (query.Schema, error) {
	schema := make(query.Schema)
	parts := strings.Split(spec, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		colonIdx := strings.IndexByte(p, ':')
		if colonIdx < 0 {
			return nil, fmt.Errorf("invalid field spec %q: expected field:type", p)
		}
		field := p[:colonIdx]
		typeName := p[colonIdx+1:]
		ft, err := query.ParseFieldType(typeName)
		if err != nil {
			return nil, err
		}
		schema[field] = ft
	}
	if len(schema) == 0 {
		return nil, fmt.Errorf("no fields specified")
	}
	return schema, nil
}

func parseDefault(val string, ft query.FieldType) any {
	switch ft {
	case query.FieldNumber:
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
		return float64(0)
	case query.FieldBool:
		return val == "true" || val == "1"
	default:
		return val
	}
}

func printImpactReport(report *migrate.ImpactReport, op *migrate.AlterOp) {
	fmt.Printf("=== Dry-Run Impact Report ===\n")
	fmt.Printf("Operation:         %s\n", op.Type)
	fmt.Printf("Collection:        %s\n", op.Collection)
	if op.Field != "" {
		fmt.Printf("Field:             %s (%s)\n", op.Field, op.FieldType)
	}
	fmt.Printf("Schema version:    %d → %d\n", report.SchemaVersionFrom, report.SchemaVersionTo)
	fmt.Printf("Affected records:  %d\n", report.AffectedRecords)
	fmt.Printf("Est. backfill:     %s\n", report.EstimatedBackfill)
	if len(report.IndexRebuildScope) > 0 {
		fmt.Printf("Index rebuild:     %s\n", strings.Join(report.IndexRebuildScope, ", "))
	}
	if report.IsIdempotent {
		fmt.Printf("Status:            IDEMPOTENT (already applied)\n")
	}
}

// --- Federation commands (Phase 8) ---

func newFederationCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "federation",
		Short: "Manage multi-repo federation (DEPRECATED)",
		Long: `Manage the pcke multi-repo federation.

DEPRECATED: federation is frozen and receives no new features. It will not be
ported to the v1.0 graph model. See ADR-0008 §4.1 and PRD v5.2 for the rationale.

Federate knowledge bases from multiple repositories into a unified view.
Enable cross-repo queries, dependency detection, and shared constraints.

Examples:
  pcke federation add backend-api /path/to/backend
  pcke federation list
  pcke federation query "FROM nodes WHERE module = \"internal/kdb\""
  pcke federation deps --module=internal/kdb
  pcke federation constraints`,
		PersistentPreRun: func(_ *cobra.Command, _ []string) {
			fmt.Fprintln(os.Stderr, "warning: pcke federation is deprecated and will not be ported to the v1.0 graph model (ADR-0008 §4.1).")
		},
	}

	cmd.PersistentFlags().StringVar(&format, "format", "text", "Output format: text or json")

	cmd.AddCommand(
		newFederationAddCmd(&format),
		newFederationRemoveCmd(&format),
		newFederationListCmd(&format),
		newFederationQueryCmd(&format),
		newFederationDepsCmd(&format),
		newFederationConstraintsCmd(&format),
	)

	return cmd
}

func newFederationAddCmd(format *string) *cobra.Command {
	return &cobra.Command{
		Use:   "add <name> <path>",
		Short: "Add a repository to the federation",
		Long: `Add a repository to the federation manifest.

The repo will be included in cross-repo queries and dependency detection.
Idempotent: if the repo name exists, its path is updated.

Examples:
  pcke federation add backend-api /path/to/backend
  pcke federation add shared-lib ~/projects/shared-lib`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			name, path := args[0], args[1]

			m, err := federation.LoadManifest()
			if err != nil {
				return fmt.Errorf("federation add: %w", err)
			}

			federation.AddRepo(m, name, path)

			if err := federation.SaveManifest(m); err != nil {
				return fmt.Errorf("federation add: %w", err)
			}

			if *format == "json" {
				out, _ := json.Marshal(map[string]string{"status": "added", "name": name, "path": path})
				fmt.Println(string(out))
			} else {
				fmt.Printf("Added %q → %s\n", name, path)
			}
			return nil
		},
	}
}

func newFederationRemoveCmd(format *string) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a repository from the federation",
		Long: `Remove a repository from the federation manifest.

Examples:
  pcke federation remove backend-api`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]

			m, err := federation.LoadManifest()
			if err != nil {
				return fmt.Errorf("federation remove: %w", err)
			}

			federation.RemoveRepo(m, name)

			if err := federation.SaveManifest(m); err != nil {
				return fmt.Errorf("federation remove: %w", err)
			}

			if *format == "json" {
				out, _ := json.Marshal(map[string]string{"status": "removed", "name": name})
				fmt.Println(string(out))
			} else {
				fmt.Printf("Removed %q from federation.\n", name)
			}
			return nil
		},
	}
}

func newFederationListCmd(format *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List federated repositories and their health status",
		Long: `List all repositories in the federation manifest with health status.

Examples:
  pcke federation list
  pcke federation list --format=json`,
		RunE: func(_ *cobra.Command, _ []string) error {
			m, err := federation.LoadManifest()
			if err != nil {
				return fmt.Errorf("federation list: %w", err)
			}

			if len(m.Repos) == 0 {
				if *format == "json" {
					fmt.Println("[]")
				} else {
					fmt.Println("No federated repos configured.")
				}
				return nil
			}

			health := federation.ValidateRepos(m)

			if *format == "json" {
				out, _ := json.MarshalIndent(health, "", "  ")
				fmt.Println(string(out))
			} else {
				if m.Federation.Name != "" {
					fmt.Printf("Federation: %s\n\n", m.Federation.Name)
				}
				for _, h := range health {
					status := "✓"
					if !h.Valid {
						status = "✗"
					}
					fmt.Printf("  %s %s\n", status, h.Name)
					fmt.Printf("    Path: %s\n", h.Path)
					if !h.Valid {
						fmt.Printf("    Problem: %s\n", h.Problem)
					}
				}
			}
			return nil
		},
	}
}

func newFederationQueryCmd(format *string) *cobra.Command {
	var timeout time.Duration
	var concurrency int

	cmd := &cobra.Command{
		Use:   "query <dsl>",
		Short: "Execute a cross-repo query",
		Long: `Execute a pcke DSL query across all federated repositories.

Results are merged and annotated with their source repository.
Partial failures (e.g., a repo that can't be opened) don't block results
from healthy repos.

Examples:
  pcke federation query "FROM nodes WHERE module = \"internal/kdb\""
  pcke federation query "FROM nodes WHERE type = \"function\"" --timeout=5s
  pcke federation query "FROM constraints" --format=json`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			dsl := args[0]

			m, err := federation.LoadManifest()
			if err != nil {
				return fmt.Errorf("federation query: %w", err)
			}

			if len(m.Repos) == 0 {
				fmt.Println("No federated repos configured. Use `pcke federation add` first.")
				return nil
			}

			ctx := context.Background()
			rs, err := federation.QueryFederation(ctx, m, dsl, federation.QueryOpts{
				Timeout:     timeout,
				Concurrency: concurrency,
			})
			if err != nil {
				return fmt.Errorf("federation query: %w", err)
			}

			if *format == "json" {
				out, _ := json.MarshalIndent(rs, "", "  ")
				fmt.Println(string(out))
			} else {
				fmt.Printf("Queried %d repos, %d results", len(rs.Repos)+len(rs.Errors), len(rs.Results))
				if len(rs.Errors) > 0 {
					fmt.Printf(" (%d partial failures)", len(rs.Errors))
				}
				fmt.Println()
				fmt.Println()
				for _, r := range rs.Results {
					name, _ := r.Row["name"].(string)
					module, _ := r.Row["module"].(string)
					fmt.Printf("  [%s] %s", r.Repo, name)
					if module != "" {
						fmt.Printf(" (%s)", module)
					}
					fmt.Println()
				}
				if len(rs.Errors) > 0 {
					fmt.Println()
					fmt.Println("Errors:")
					for _, e := range rs.Errors {
						fmt.Printf("  %s: %v\n", e.Repo, e.Error)
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Second, "Per-repo query timeout")
	cmd.Flags().IntVar(&concurrency, "concurrency", 4, "Max concurrent repo queries")
	return cmd
}

func newFederationDepsCmd(format *string) *cobra.Command {
	var module string

	cmd := &cobra.Command{
		Use:   "deps",
		Short: "Show cross-repo dependencies",
		Long: `Detect and display cross-repo dependencies.

Analyzes Go import statements to find packages imported from other
federated repositories.

Examples:
  pcke federation deps
  pcke federation deps --module=internal/kdb
  pcke federation deps --format=json`,
		RunE: func(_ *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("federation deps: %w", err)
			}

			m, err := federation.LoadManifest()
			if err != nil {
				return fmt.Errorf("federation deps: %w", err)
			}

			ctx := context.Background()
			deps, err := federation.DetectCrossRepoDeps(ctx, m, cwd)
			if err != nil {
				return fmt.Errorf("federation deps: %w", err)
			}

			if module != "" {
				var filtered []federation.CrossRepoDep
				for _, d := range deps {
					if d.TargetModule == module || strings.Contains(d.SourceNodeID, module) {
						filtered = append(filtered, d)
					}
				}
				deps = filtered
			}

			if *format == "json" {
				out, _ := json.MarshalIndent(deps, "", "  ")
				fmt.Println(string(out))
			} else {
				if len(deps) == 0 {
					fmt.Println("No cross-repo dependencies detected.")
					return nil
				}
				fmt.Printf("Cross-repo dependencies (%d):\n\n", len(deps))
				for _, d := range deps {
					fmt.Printf("  %s → %s/%s\n", d.SourceNodeID, d.TargetRepo, d.TargetModule)
					fmt.Printf("    import: %s (via %s)\n", d.ImportPath, d.DetectedVia)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&module, "module", "", "Filter by module name")
	return cmd
}

func newFederationConstraintsCmd(format *string) *cobra.Command {
	return &cobra.Command{
		Use:   "constraints",
		Short: "Show org-wide constraints and violations",
		Long: `Display org-wide constraints from the federation manifest and check
for violations in the current repository.

Examples:
  pcke federation constraints
  pcke federation constraints --format=json`,
		RunE: func(_ *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("federation constraints: %w", err)
			}

			m, err := federation.LoadManifest()
			if err != nil {
				return fmt.Errorf("federation constraints: %w", err)
			}

			if len(m.Constraints.Rules) == 0 {
				if *format == "json" {
					fmt.Println(`{"rules":[],"violations":[]}`)
				} else {
					fmt.Println("No org-wide constraints configured.")
				}
				return nil
			}

			db, err := kdb.Open(cwd, nil)
			if err != nil {
				return fmt.Errorf("federation constraints: open db: %w", err)
			}
			defer db.Close() //nolint:errcheck // best-effort close

			ctx := context.Background()
			violations, err := federation.CheckOrgConstraints(ctx, db, m)
			if err != nil {
				return fmt.Errorf("federation constraints: %w", err)
			}

			if *format == "json" {
				result := map[string]any{
					"rules":      m.Constraints.Rules,
					"violations": violations,
				}
				out, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(out))
			} else {
				fmt.Printf("Org-wide constraints (%d):\n\n", len(m.Constraints.Rules))
				for _, r := range m.Constraints.Rules {
					fmt.Printf("  [%s] %s — %s\n", r.Severity, r.Scope, r.Description)
				}
				if len(violations) > 0 {
					fmt.Printf("\nViolations (%d):\n\n", len(violations))
					for _, v := range violations {
						fmt.Printf("  ⚠ %s: %s\n", v.NodeID, v.Description)
					}
				} else {
					fmt.Println("\nNo violations detected. ✓")
				}
			}
			return nil
		},
	}
}

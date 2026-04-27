package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/jenaiz/pcke/internal/analysis"
	"github.com/jenaiz/pcke/internal/config"
	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/index/fts"
	"github.com/jenaiz/pcke/internal/kdb/migrate"
	kdbquery "github.com/jenaiz/pcke/internal/kdb/query"
	"github.com/jenaiz/pcke/internal/kdb/tx"
	"github.com/jenaiz/pcke/internal/output"
	"github.com/jenaiz/pcke/internal/query"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialise pcke in the current repository",
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
	cmd := &cobra.Command{
		Use:   "rule",
		Short: "Manage project rules",
		Long: `Manage project rules extracted from @pcke-rule annotations in source code.

Rules are discovered during scan from in-code annotations like:
  // @pcke-rule no-raw-sql: Never execute raw SQL; always use parameterized queries`,
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List rules extracted from source annotations",
		RunE: func(_ *cobra.Command, _ []string) error {
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
					fmt.Printf("  %s  (%s)\n", n.Name, n.FilePath)
				}
			}
			if count == 0 {
				fmt.Println("No rules found. Add @pcke-rule annotations to your source files and run pcke scan.")
			} else {
				fmt.Printf("\n%d rule(s)\n", count)
			}
			return nil
		},
	})

	return cmd
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
				prefix := []byte("note:")
				c := rtx.Cursor()
				for ok := c.Seek(prefix); ok; ok = c.Next() {
					if !strings.HasPrefix(string(c.Key()), "note:") {
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
		},
	})

	return cmd
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show knowledge base status",
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

			planner := kdbquery.NewPlanner(idx)
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
			engine := migrate.New()
			registerMigrations(engine)

			before := db.SchemaVersion()
			applied, err := engine.Run(ctx, db)
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
	// No migrations yet — the initial schema (version 0) is the baseline.
	// Future migrations will be registered here:
	//
	// e.Register(migrate.Migration{
	//     Version:     1,
	//     Description: "add foo index",
	//     Migrate:     migrateV1AddFooIndex,
	// })
	_ = e
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

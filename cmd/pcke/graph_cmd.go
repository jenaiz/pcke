package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/graph"
)

// newGraphCmd builds the `pcke graph` command tree:
//
//	pcke graph neighbors <ref>   1-hop refs from <ref>
//	pcke graph impact <ref>      reverse-reachable refs (transitive deps)
//
// Both subcommands accept --depth, --edge-type (repeatable), --direction
// (graph neighbors only; impact is implicitly Reverse), and --as-of.
func newGraphCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Traverse the typed-event graph",
		Long: `Traverse the typed-event graph (entities, decisions, links).

The graph is populated by 'pcke scan' (file scan + decision backfill)
and by the migrations that run on first start of v0.10.0 over a
v0.9.x database.

Examples:
  pcke graph neighbors e:internal/kdb/db.go
  pcke graph neighbors e:internal/kdb/db.go --depth=2 --edge-type=imports
  pcke graph impact e:internal/kdb/btree --depth=3
  pcke graph neighbors e:foo --direction=both
  pcke graph neighbors e:foo --as-of=2026-04-01`,
	}

	cmd.AddCommand(newGraphNeighborsCmd(), newGraphImpactCmd())
	return cmd
}

// graphTraversalFlags is the flag set shared by neighbors + impact.
type graphTraversalFlags struct {
	depth     int
	edgeTypes []string
	direction string // forward | reverse | both
	asOf      string // RFC3339 or YYYY-MM-DD
}

func (f *graphTraversalFlags) bind(cmd *cobra.Command, withDirection bool) {
	cmd.Flags().IntVar(&f.depth, "depth", 1, "Maximum hops to traverse (default 1 for neighbors)")
	cmd.Flags().StringSliceVar(&f.edgeTypes, "edge-type", nil, "Restrict to listed edge types (repeatable; empty = any)")
	if withDirection {
		cmd.Flags().StringVar(&f.direction, "direction", "forward", "forward | reverse | both")
	}
	cmd.Flags().StringVar(&f.asOf, "as-of", "", "Pin traversal to a point in time (RFC3339 or YYYY-MM-DD)")
}

// resolve maps the raw flag values into graph.TraversalOptions plus a
// resolved start ref. Returns ErrUserError-shaped errors that the
// caller can propagate.
func (f *graphTraversalFlags) resolve(forceDir graph.Direction, useFlagDir bool) (graph.TraversalOptions, error) {
	opts := graph.TraversalOptions{
		MaxDepth:  f.depth,
		EdgeTypes: f.edgeTypes,
	}
	if useFlagDir {
		switch strings.ToLower(f.direction) {
		case "forward", "":
			opts.Direction = graph.Forward
		case "reverse":
			opts.Direction = graph.Reverse
		case "both":
			opts.Direction = graph.Both
		default:
			return graph.TraversalOptions{}, fmt.Errorf("invalid --direction %q (want forward|reverse|both)", f.direction)
		}
	} else {
		opts.Direction = forceDir
	}
	if f.asOf != "" {
		t, err := parseAsOfFlag(f.asOf)
		if err != nil {
			return graph.TraversalOptions{}, err
		}
		opts.AsOf = &t
	}
	return opts, nil
}

func parseAsOfFlag(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid --as-of %q (want RFC3339 or YYYY-MM-DD)", s)
}

func newGraphNeighborsCmd() *cobra.Command {
	flags := &graphTraversalFlags{}
	cmd := &cobra.Command{
		Use:   "neighbors <ref>",
		Short: "List refs reachable from <ref>",
		Long: `Walk the graph from <ref> and print every reachable ref, one per line.

The default --depth is 1 (immediate neighbors). Increase to surface
multi-hop reach.

<ref> is a typed reference like "e:internal/kdb/db.go" or "d:adr-0008".`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			opts, err := flags.resolve(graph.Forward, true)
			if err != nil {
				return err
			}
			return runGraphTraversal(args[0], opts)
		},
	}
	flags.bind(cmd, true)
	return cmd
}

func newGraphImpactCmd() *cobra.Command {
	flags := &graphTraversalFlags{}
	cmd := &cobra.Command{
		Use:   "impact <ref>",
		Short: "List refs that transitively reach <ref>",
		Long: `Walk the graph in reverse from <ref> — answering "what depends on this?".

Equivalent to 'pcke graph neighbors --direction=reverse', but with
--depth defaulting to a higher value (3) since impact analysis is
typically about transitive reach.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if flags.depth == 1 {
				flags.depth = 3
			}
			opts, err := flags.resolve(graph.Reverse, false)
			if err != nil {
				return err
			}
			return runGraphTraversal(args[0], opts)
		},
	}
	flags.bind(cmd, false)
	return cmd
}

// runGraphTraversal opens the kdb at cwd, runs graph.Reachable, and
// prints sorted refs to stdout.
func runGraphTraversal(start string, opts graph.TraversalOptions) error {
	if start == "" {
		return fmt.Errorf("start reference cannot be empty")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("graph: get working directory: %w", err)
	}
	db, err := kdb.Open(cwd, nil)
	if err != nil {
		return fmt.Errorf("graph: open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	refs, err := graph.Reachable(context.Background(), db, graph.Ref(start), opts)
	if err != nil && !errors.Is(err, graph.ErrVisitedCapExceeded) {
		return fmt.Errorf("graph: traverse: %w", err)
	}

	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = string(r)
	}
	sort.Strings(out)
	for _, r := range out {
		fmt.Println(r)
	}
	fmt.Fprintf(os.Stderr, "\n%d result(s)\n", len(out))
	if errors.Is(err, graph.ErrVisitedCapExceeded) {
		fmt.Fprintln(os.Stderr, "warning: visited cap exceeded; result is partial. retry with --depth or contact the team to bump MaxVisited.")
	}
	return nil
}

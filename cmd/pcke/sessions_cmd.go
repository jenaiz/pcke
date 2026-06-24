package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jenaiz/pcke/internal/config"
	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/graph"
	"github.com/jenaiz/pcke/internal/kdb/tx"
	pckelog "github.com/jenaiz/pcke/internal/log"
)

// newSessionsCmd builds the `pcke sessions` subtree (Phase 14 F14.T4):
//
//	pcke sessions list  [--since 7d]
//	pcke sessions show  <id>
//	pcke sessions clear [--older-than 30d] [--all]
//
// All three read the same observation sub-types (`o:session:*` /
// `o:call:*`) written by the MCP server.
func newSessionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "Inspect persisted MCP session observations",
		Long: `Sessions and their tool calls are recorded as Observation events under
the "o:session:<uuid>" and "o:call:<uuid>" keys. This subcommand reads
them back so you can audit what the agent saw.

Examples:
  pcke sessions list                       # all sessions
  pcke sessions list --since 24h           # last 24 hours
  pcke sessions show <uuid>                # one session: calls + refs served
  pcke sessions clear --older-than 30d     # prune
  pcke sessions clear --all                # privacy reset`,
	}
	cmd.AddCommand(newSessionsListCmd(), newSessionsShowCmd(), newSessionsClearCmd())
	return cmd
}

func newSessionsListCmd() *cobra.Command {
	var sinceFlag string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent sessions with call counts",
		RunE: func(_ *cobra.Command, _ []string) error {
			since, err := parseSinceFlag(sinceFlag)
			if err != nil {
				return err
			}
			return runSessionsList(since)
		},
	}
	cmd.Flags().StringVar(&sinceFlag, "since", "", "Only sessions newer than this (e.g. 24h, 7d, 30d)")
	return cmd
}

func newSessionsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <uuid>",
		Short: "Show one session: calls, served refs, paths",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runSessionsShow(args[0])
		},
	}
}

func newSessionsClearCmd() *cobra.Command {
	var olderThanFlag string
	var all bool
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Delete session/call observations and their edges",
		RunE: func(_ *cobra.Command, _ []string) error {
			if !all && olderThanFlag == "" {
				return fmt.Errorf("sessions clear: pass --all or --older-than")
			}
			if all && olderThanFlag != "" {
				return fmt.Errorf("sessions clear: --all and --older-than are mutually exclusive")
			}
			var cutoff time.Time
			if olderThanFlag != "" {
				d, err := parseHumanDuration(olderThanFlag)
				if err != nil {
					return err
				}
				cutoff = time.Now().UTC().Add(-d)
			}
			return runSessionsClear(all, cutoff)
		},
	}
	cmd.Flags().StringVar(&olderThanFlag, "older-than", "", "Drop sessions older than this (e.g. 30d)")
	cmd.Flags().BoolVar(&all, "all", false, "Drop every session/call observation and edge")
	return cmd
}

// sessionRow is one row in `sessions list` output.
type sessionRow struct {
	uuid    string
	label   string
	created time.Time
	calls   int
}

func runSessionsList(since time.Time) error {
	db, store, closeFn, err := openSessionDB()
	if err != nil {
		return err
	}
	defer closeFn()

	rows, err := collectSessions(context.Background(), db, store, since)
	if err != nil {
		return fmt.Errorf("sessions list: %w", err)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].created.After(rows[j].created) })

	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "(no sessions)")
		return nil
	}
	for _, r := range rows {
		ts := r.created.Local().Format("2006-01-02 15:04")
		label := r.label
		if label == "" {
			label = "-"
		}
		fmt.Printf("%-36s  %s  calls=%-4d  %s\n", r.uuid, ts, r.calls, label)
	}
	fmt.Fprintf(os.Stderr, "\n%d session(s)\n", len(rows))
	return nil
}

func runSessionsShow(uuid string) error {
	db, store, closeFn, err := openSessionDB()
	if err != nil {
		return err
	}
	defer closeFn()

	ctx := context.Background()
	sessOID := event.SessionOID(uuid)
	sess, err := store.Latest(ctx, event.KindObservation, sessOID)
	if err != nil {
		if errors.Is(err, event.ErrNotFound) {
			return fmt.Errorf("sessions show: session %q not found", uuid)
		}
		return fmt.Errorf("sessions show: %w", err)
	}
	sObs, ok := sess.(*event.Observation)
	if !ok {
		return fmt.Errorf("sessions show: %q is not an observation", sessOID)
	}

	fmt.Printf("Session %s\n", uuid)
	fmt.Printf("  started: %s\n", sObs.Header().CreatedAt.Local().Format(time.RFC3339))
	if sObs.Subject != "" {
		fmt.Printf("  label:   %s\n", sObs.Subject)
	}

	// Walk contains-edges to enumerate calls; for each, walk served-edges
	// to surface the refs it returned.
	callRefs, err := graph.Neighbors(ctx, db, graph.Ref(event.SessionRef(uuid)), graph.TraversalOptions{
		Direction: graph.Forward,
		EdgeTypes: []string{event.EdgeContains},
	})
	if err != nil {
		return fmt.Errorf("sessions show: neighbors: %w", err)
	}
	if len(callRefs) == 0 {
		fmt.Println("  (no recorded tool calls)")
		return nil
	}

	type callRow struct {
		ref    graph.Ref
		tool   string
		at     time.Time
		served []graph.Ref
	}
	rows := make([]callRow, 0, len(callRefs))
	for _, callRef := range callRefs {
		oid, ok := stripObservationPrefix(string(callRef))
		if !ok {
			continue
		}
		ev, gerr := store.Latest(ctx, event.KindObservation, oid)
		if gerr != nil {
			continue
		}
		obs, ok := ev.(*event.Observation)
		if !ok {
			continue
		}
		served, gerr := graph.Neighbors(ctx, db, callRef, graph.TraversalOptions{
			Direction: graph.Forward,
			EdgeTypes: []string{event.EdgeServed},
		})
		if gerr != nil {
			continue
		}
		rows = append(rows, callRow{
			ref:    callRef,
			tool:   obs.Subject,
			at:     obs.Header().CreatedAt,
			served: served,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].at.Before(rows[j].at) })

	fmt.Printf("  calls: %d\n", len(rows))
	for i, r := range rows {
		fmt.Printf("  [%d] %s  tool=%s  served=%d\n",
			i+1, r.at.Local().Format("15:04:05"), nonEmpty(r.tool, "-"), len(r.served))
		for _, dst := range r.served {
			fmt.Printf("        - %s\n", dst)
		}
	}
	return nil
}

// sessionVictim captures the data needed to delete one session and its
// reachable subgraph in a separate write tx.
type sessionVictim struct {
	sessUUID string
	callRefs []graph.Ref
}

func runSessionsClear(all bool, cutoff time.Time) error {
	db, _, closeFn, err := openSessionDB()
	if err != nil {
		return err
	}
	defer closeFn()

	ctx := context.Background()
	victims, err := collectSessionVictims(ctx, db, all, cutoff)
	if err != nil {
		return fmt.Errorf("sessions clear: scan: %w", err)
	}
	if len(victims) == 0 {
		fmt.Fprintln(os.Stderr, "(no sessions matched)")
		return nil
	}

	if err := deleteSessionVictims(ctx, db, victims); err != nil {
		return fmt.Errorf("sessions clear: delete: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Cleared %d session(s)\n", len(victims))
	return nil
}

// collectSessionVictims walks the `o:session:*` prefix once and returns
// every session whose CreatedAt qualifies for deletion. Reading before
// writing ensures the cursor scan doesn't see in-flight deletes.
func collectSessionVictims(ctx context.Context, db *kdb.DB, all bool, cutoff time.Time) ([]sessionVictim, error) {
	prefix := []byte("o:" + event.EscapeID("session:"))
	store := event.New(db)
	var victims []sessionVictim
	err := db.View(ctx, func(rtx *tx.ReadTx) error {
		cursor := rtx.Cursor()
		if !cursor.Seek(prefix) {
			return nil
		}
		seen := map[string]struct{}{}
		for cursor.Valid() {
			key := cursor.Key()
			if !bytes.HasPrefix(key, prefix) {
				return nil
			}
			cursor.Next()
			pk, perr := event.ParseKey(key)
			if perr != nil {
				continue
			}
			if _, dup := seen[pk.ID]; dup {
				continue
			}
			seen[pk.ID] = struct{}{}
			ev, gerr := store.Latest(ctx, event.KindObservation, pk.ID)
			if gerr != nil {
				continue
			}
			if !all && !ev.Header().CreatedAt.Before(cutoff) {
				continue
			}
			uuid, _ := event.SessionUUID(pk.ID)
			callRefs, _ := graph.Neighbors(ctx, db, graph.Ref(event.SessionRef(uuid)),
				graph.TraversalOptions{Direction: graph.Forward, EdgeTypes: []string{event.EdgeContains}})
			victims = append(victims, sessionVictim{sessUUID: uuid, callRefs: callRefs})
		}
		return nil
	})
	return victims, err
}

// deleteSessionVictims drops every observation chain (session + calls)
// and the link records between them. All deletes run in one Update so
// a crash mid-clear leaves the graph consistent.
func deleteSessionVictims(ctx context.Context, db *kdb.DB, victims []sessionVictim) error {
	return db.Update(ctx, func(wtx *tx.WriteTx) error {
		for _, v := range victims {
			if err := dropChain(wtx, event.KindObservation, event.SessionOID(v.sessUUID)); err != nil {
				return err
			}
			for _, callRef := range v.callRefs {
				oid, ok := stripObservationPrefix(string(callRef))
				if !ok {
					continue
				}
				if err := dropChain(wtx, event.KindObservation, oid); err != nil {
					return err
				}
			}
			if err := dropLinksTouching(wtx, v.sessUUID, v.callRefs); err != nil {
				return err
			}
		}
		return nil
	})
}

// collectSessions walks every o:session:<uuid> chain, returning one row
// per session with the call count derived from the "contains" edge.
func collectSessions(ctx context.Context, db *kdb.DB, store *event.Store, since time.Time) ([]sessionRow, error) {
	var rows []sessionRow
	err := store.IterateKind(ctx, event.KindObservation, func(e event.Event) error {
		obs, ok := e.(*event.Observation)
		if !ok || obs.Action != event.ActionSession {
			return nil
		}
		uuid, ok := event.SessionUUID(obs.OID)
		if !ok {
			return nil
		}
		if !since.IsZero() && obs.Header().CreatedAt.Before(since) {
			return nil
		}
		callRefs, _ := graph.Neighbors(ctx, db, graph.Ref(event.SessionRef(uuid)),
			graph.TraversalOptions{Direction: graph.Forward, EdgeTypes: []string{event.EdgeContains}})
		rows = append(rows, sessionRow{
			uuid:    uuid,
			label:   obs.Subject,
			created: obs.Header().CreatedAt,
			calls:   len(callRefs),
		})
		return nil
	})
	return rows, err
}

// openSessionDB opens the kdb at the current working directory and
// returns the DB + an event.Store + a close fn.
func openSessionDB() (*kdb.DB, *event.Store, func(), error) {
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

// dropChain deletes every version key in the (kind, id) chain. Cursor
// is positioned with chainPrefix(kind, id) so unrelated records are
// untouched.
func dropChain(wtx *tx.WriteTx, kind event.Kind, id string) error {
	prefix := []byte(kind.Prefix() + event.EscapeID(id) + ":v")
	cursor := wtx.Cursor()
	if !cursor.Seek(prefix) {
		return nil
	}
	var keys [][]byte
	for cursor.Valid() {
		k := cursor.Key()
		if !bytes.HasPrefix(k, prefix) {
			break
		}
		keys = append(keys, append([]byte(nil), k...))
		cursor.Next()
	}
	for _, k := range keys {
		if err := wtx.Delete(k); err != nil {
			return fmt.Errorf("delete %q: %w", k, err)
		}
	}
	return nil
}

// dropLinksTouching deletes every l: / lr: record whose src or dst is
// the session or any of its calls. Iterates the link prefix once and
// matches refs in-place.
func dropLinksTouching(wtx *tx.WriteTx, sessUUID string, callRefs []graph.Ref) error {
	sessRef := event.SessionRef(sessUUID)
	hot := map[string]struct{}{sessRef: {}}
	for _, c := range callRefs {
		hot[string(c)] = struct{}{}
	}

	if err := dropLinkPrefix(wtx, []byte("l:"), hot); err != nil {
		return err
	}
	return dropLinkPrefix(wtx, []byte("lr:"), hot)
}

func dropLinkPrefix(wtx *tx.WriteTx, prefix []byte, hot map[string]struct{}) error {
	cursor := wtx.Cursor()
	if !cursor.Seek(prefix) {
		return nil
	}
	var matchKeys [][]byte
	for cursor.Valid() {
		k := cursor.Key()
		if !bytes.HasPrefix(k, prefix) {
			break
		}
		if linkKeyTouchesAny(k, prefix, hot) {
			matchKeys = append(matchKeys, append([]byte(nil), k...))
		}
		cursor.Next()
	}
	for _, k := range matchKeys {
		if err := wtx.Delete(k); err != nil {
			return fmt.Errorf("delete link %q: %w", k, err)
		}
	}
	return nil
}

// linkKeyTouchesAny reports whether the link key contains any ref in
// hot as a src or dst segment. Compares the unescaped components.
func linkKeyTouchesAny(key, prefix []byte, hot map[string]struct{}) bool {
	if bytes.Equal(prefix, []byte("l:")) {
		// Forward link key: "l:<src>:<edge>:<dst>:v<digits>"
		parsed, err := event.ParseKey(key)
		if err != nil {
			return false
		}
		parts, err := splitEscapedColons(parsed.ID, 3)
		if err != nil {
			return false
		}
		src, _ := event.UnescapeID(parts[0])
		dst, _ := event.UnescapeID(parts[2])
		_, srcHit := hot[src]
		_, dstHit := hot[dst]
		return srcHit || dstHit
	}
	// Reverse link key: "lr:<dst>:<edge>:<src>"
	rk, err := event.ParseReverseLinkKey(key)
	if err != nil {
		return false
	}
	_, dstHit := hot[rk.DstRef]
	_, srcHit := hot[rk.SrcRef]
	return srcHit || dstHit
}

// splitEscapedColons mirrors event.splitEscapedColons. Duplicated here
// because the helper is unexported; the inputs are small enough that
// a local copy is simpler than widening the package surface.
func splitEscapedColons(s string, want int) ([]string, error) {
	parts := make([]string, 0, want)
	var current strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			current.WriteByte(c)
			current.WriteByte(s[i+1])
			i++
			continue
		}
		if c == ':' {
			parts = append(parts, current.String())
			current.Reset()
			continue
		}
		current.WriteByte(c)
	}
	parts = append(parts, current.String())
	if len(parts) != want {
		return nil, fmt.Errorf("expected %d segments, got %d", want, len(parts))
	}
	return parts, nil
}

// stripObservationPrefix removes the "o:" prefix from a typed ref,
// returning the OID body. Returns ok=false if ref is not an o: ref.
func stripObservationPrefix(ref string) (string, bool) {
	const prefix = "o:"
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	return ref[len(prefix):], true
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// parseSinceFlag parses a "--since" flag into an absolute time. Empty
// string yields the zero time which downstream callers treat as "no
// filter". Accepts the same suffixes as time.ParseDuration plus "d".
func parseSinceFlag(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	d, err := parseHumanDuration(s)
	if err != nil {
		return time.Time{}, err
	}
	return time.Now().UTC().Add(-d), nil
}

// runRetentionPrune drops sessions older than [telemetry] retention_days.
// Invoked from `pcke serve` startup; runs in a goroutine so it does not
// delay the MCP listener. Errors are logged via slog and not surfaced
// to the user — retention is best-effort.
//
// retention_days = 0 (and `telemetry.disabled = true`) disables the prune
// entirely; users can still run `pcke sessions clear` manually.
func runRetentionPrune(cwd string, db *kdb.DB) {
	logger := pckelog.Logger("telemetry.retention")
	cfg, err := config.Load(cwd)
	if err != nil {
		logger.Warn("config load failed; skipping prune", "err", err)
		return
	}
	if cfg.Telemetry.Disabled || cfg.Telemetry.RetentionDays <= 0 {
		return
	}
	cutoff := time.Now().UTC().Add(-time.Duration(cfg.Telemetry.RetentionDays) * 24 * time.Hour)

	go func() {
		ctx := context.Background()
		victims, vErr := collectSessionVictims(ctx, db, false, cutoff)
		if vErr != nil {
			logger.Warn("retention scan failed", "err", vErr)
			return
		}
		if len(victims) == 0 {
			return
		}
		if dErr := deleteSessionVictims(ctx, db, victims); dErr != nil {
			logger.Warn("retention delete failed", "err", dErr, "victim_count", len(victims))
			return
		}
		logger.Info("retention prune complete",
			"victims", len(victims),
			"retention_days", cfg.Telemetry.RetentionDays)
	}()
}

// parseHumanDuration extends time.ParseDuration with a "d" (day) and
// "w" (week) suffix so retention flags can be written naturally.
func parseHumanDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	last := s[len(s)-1]
	switch last {
	case 'd', 'D':
		n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	case 'w', 'W':
		n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	return d, nil
}

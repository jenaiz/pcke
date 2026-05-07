package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jenaiz/pcke/internal/kdb/event"
)

// newHistoryCmd exposes the version chain of any event-log record:
//
//	pcke history e:internal/kdb/db.go
//	pcke history d:adr-0008
//
// It prints one line per version, oldest first, showing version
// number, timestamp, lifecycle, and a one-line excerpt of the payload.
func newHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history <ref>",
		Short: "Show version chain for an event-log ref",
		Long: `Walk the version chain of an entity or decision and print every version.

<ref> is a typed reference like "e:internal/kdb/db.go" or
"d:adr-0008". The trailing :v<N> suffix is NOT required; this command
walks the full chain regardless of which version you supply.

Output (one row per version, oldest first):

  v<N>  <created-at>  <lifecycle>  <one-line excerpt>

Examples:
  pcke history e:internal/kdb/db.go
  pcke history d:adr-0008-context-graph-pivot`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runHistory(args[0])
		},
	}
}

func runHistory(ref string) error {
	kind, id, err := parseTypedRef(ref)
	if err != nil {
		return err
	}

	store, closeFn, err := openEventStore()
	if err != nil {
		return err
	}
	defer closeFn()

	count := 0
	err = store.History(context.Background(), kind, id, func(e event.Event) error {
		hdr := e.Header()
		excerpt := excerptForEvent(e)
		fmt.Printf("v%-4d  %s  %-10s  %s\n",
			hdr.Version,
			hdr.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			lifecycleName(hdr.Lifecycle),
			excerpt,
		)
		count++
		return nil
	})
	if err != nil {
		if errors.Is(err, event.ErrNotFound) {
			return fmt.Errorf("history: no versions for %q", ref)
		}
		return fmt.Errorf("history: %w", err)
	}
	fmt.Fprintf(os.Stderr, "\n%d version(s)\n", count)
	return nil
}

// parseTypedRef splits a "<kind-prefix>:<id>" string into the kind enum
// and unescaped id. Returns ErrInvalidKind for unknown prefixes. The
// trailing ":v<N>" segment is stripped if present so users can paste a
// versioned key from elsewhere and still get the full chain.
func parseTypedRef(ref string) (event.Kind, string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return 0, "", fmt.Errorf("ref must not be empty")
	}
	// If the ref includes a ":v<digits>" suffix, drop it.
	if i := strings.LastIndex(ref, ":v"); i > 0 {
		tail := ref[i+2:]
		if isAllDigits(tail) {
			ref = ref[:i]
		}
	}
	colonIdx := strings.Index(ref, ":")
	if colonIdx < 1 {
		return 0, "", fmt.Errorf("invalid ref %q: must be '<kind>:<id>' (e.g. e:foo or d:adr-0008)", ref)
	}
	prefix := ref[:colonIdx+1]
	id := ref[colonIdx+1:]
	if id == "" {
		return 0, "", fmt.Errorf("invalid ref %q: id is empty", ref)
	}
	switch prefix {
	case "e:":
		return event.KindEntity, id, nil
	case "d:":
		return event.KindDecision, id, nil
	case "o:":
		return event.KindObservation, id, nil
	case "x:":
		return event.KindOutcome, id, nil
	case "l:":
		return event.KindLink, id, nil
	default:
		return 0, "", fmt.Errorf("invalid ref %q: unknown prefix %q (want e:|d:|o:|x:|l:)", ref, prefix)
	}
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// excerptForEvent returns a one-line summary of an event suitable for
// the rightmost column of `pcke history` output. Capped at 80 runes
// so the line stays readable in standard terminals.
func excerptForEvent(e event.Event) string {
	const maxLen = 80
	var s string
	switch v := e.(type) {
	case *event.Entity:
		s = v.Type
		if v.Name != "" {
			s += " " + v.Name
		}
		if v.Path != "" {
			s += " (" + v.Path + ")"
		}
	case *event.Decision:
		s = v.Title
		if s == "" {
			s = v.Body
		}
	case *event.Link:
		s = fmt.Sprintf("%s --%s--> %s", v.SrcRef, v.EdgeType, v.DstRef)
	case *event.Observation:
		s = v.Action
		if v.Subject != "" {
			s += " " + v.Subject
		}
	case *event.Outcome:
		s = v.Type
		if v.Subject != "" {
			s += " " + v.Subject
		}
	default:
		s = e.Kind().String()
	}
	s = strings.ReplaceAll(s, "\n", " ")
	if r := []rune(s); len(r) > maxLen {
		s = string(r[:maxLen])
	}
	return s
}

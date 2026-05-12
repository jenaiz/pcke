package output

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
)

// Topology summarises the project's typed-event graph in three lists
// the agent-instruction renderer uses to build an Architecture Quick
// Reference. All three are derived solely from event-log structure —
// no file counts, no language guessing.
//
//   - EntryPoints: entities with low import fan-in and high import
//     fan-out. These tend to be CLI roots, server `main` files, or
//     test harnesses — the places where a new contributor should start
//     reading.
//   - CoreModules: directory paths whose entities receive the most
//     import edges. High fan-in marks code that everything else depends
//     on; touching it is consequential.
//   - DecisionHotspots: entities targeted by ≥3 must-severity
//     decisions via `decision_link`. These files carry the most rules.
type Topology struct {
	EntryPoints      []string
	CoreModules      []string
	DecisionHotspots []string
}

// IsEmpty reports whether the topology yielded no insights. The
// renderer uses this to skip the section entirely when running against
// a legacy knowledge base that has not yet been migrated to the typed
// event log.
func (t *Topology) IsEmpty() bool {
	return len(t.EntryPoints) == 0 && len(t.CoreModules) == 0 && len(t.DecisionHotspots) == 0
}

// topoLimits caps each list at a length that fits the "quick reference"
// brief: enough to orient an agent without flooding the prompt budget.
const (
	maxEntryPoints      = 5
	maxCoreModules      = 5
	maxDecisionHotspots = 5

	entryPointMinFanOut = 3
	entryPointMaxFanIn  = 1
	hotspotMinMustCount = 3
)

// ComputeTopology builds the Topology by scanning the typed-event log
// in two passes:
//
//  1. Collect every active entity ref + every must-severity decision
//     id (so the second pass can tag decision_link edges cheaply).
//  2. Iterate links; per import-link, bump fan-in/out counters; per
//     decision_link to a must decision, bump the source's hotspot
//     counter.
//
// Two top-level View calls — no nested transactions — so this stays
// well-behaved under kdb's RW locking.
func ComputeTopology(ctx context.Context, db *kdb.DB) (*Topology, error) {
	store := event.New(db)

	bucket := map[string]*stats{}
	mustDecisions := map[string]bool{}

	if err := store.IterateKind(ctx, event.KindEntity, func(e event.Event) error {
		bucket["e:"+e.ID()] = &stats{}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("topology: iterate entities: %w", err)
	}

	if err := store.IterateKind(ctx, event.KindDecision, func(e event.Event) error {
		if d, ok := e.(*event.Decision); ok && d.Severity == event.SeverityMust {
			mustDecisions[d.DID] = true
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("topology: iterate decisions: %w", err)
	}

	if err := store.IterateKind(ctx, event.KindLink, func(e event.Event) error {
		l, ok := e.(*event.Link)
		if !ok {
			return nil
		}
		switch l.EdgeType {
		case "imports":
			if s, ok := bucket[l.SrcRef]; ok {
				s.fanOut++
			}
			if s, ok := bucket[l.DstRef]; ok {
				s.fanIn++
			}
		case "decision_link":
			did, ok := strings.CutPrefix(l.DstRef, "d:")
			if !ok || !mustDecisions[did] {
				return nil
			}
			if s, ok := bucket[l.SrcRef]; ok {
				s.mustCount++
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("topology: iterate links: %w", err)
	}

	return classify(bucket), nil
}

// stats is hoisted to package scope so classify can name it.
type stats struct {
	fanIn, fanOut, mustCount int
}

// entry is the flat snapshot per ref used by the three classifiers.
type topoEntry struct {
	ref       string
	fanIn     int
	fanOut    int
	mustCount int
}

// classify turns the per-entity stats map into the three ranked lists.
// Stable sorting: each classifier sorts by its primary metric desc,
// then breaks ties on the ref so the output is reproducible across runs.
func classify(bucket map[string]*stats) *Topology {
	all := make([]topoEntry, 0, len(bucket))
	for ref, s := range bucket {
		all = append(all, topoEntry{ref, s.fanIn, s.fanOut, s.mustCount})
	}
	return &Topology{
		EntryPoints:      classifyEntryPoints(all),
		CoreModules:      classifyCoreModules(all),
		DecisionHotspots: classifyDecisionHotspots(all),
	}
}

func classifyEntryPoints(all []topoEntry) []string {
	entries := append([]topoEntry(nil), all...)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].fanOut != entries[j].fanOut {
			return entries[i].fanOut > entries[j].fanOut
		}
		return entries[i].ref < entries[j].ref
	})
	var out []string
	for _, e := range entries {
		if e.fanIn > entryPointMaxFanIn || e.fanOut < entryPointMinFanOut {
			continue
		}
		out = append(out, refToPath(e.ref))
		if len(out) >= maxEntryPoints {
			break
		}
	}
	return out
}

func classifyCoreModules(all []topoEntry) []string {
	moduleFanIn := map[string]int{}
	for _, e := range all {
		if e.fanIn == 0 {
			continue
		}
		moduleFanIn[moduleOf(e.ref)] += e.fanIn
	}
	type modCount struct {
		name string
		n    int
	}
	mods := make([]modCount, 0, len(moduleFanIn))
	for m, n := range moduleFanIn {
		mods = append(mods, modCount{m, n})
	}
	sort.Slice(mods, func(i, j int) bool {
		if mods[i].n != mods[j].n {
			return mods[i].n > mods[j].n
		}
		return mods[i].name < mods[j].name
	})
	out := make([]string, 0, maxCoreModules)
	for i, m := range mods {
		if i >= maxCoreModules {
			break
		}
		out = append(out, fmt.Sprintf("%s (%d incoming)", m.name, m.n))
	}
	return out
}

func classifyDecisionHotspots(all []topoEntry) []string {
	hotspots := append([]topoEntry(nil), all...)
	sort.Slice(hotspots, func(i, j int) bool {
		if hotspots[i].mustCount != hotspots[j].mustCount {
			return hotspots[i].mustCount > hotspots[j].mustCount
		}
		return hotspots[i].ref < hotspots[j].ref
	})
	var out []string
	for _, e := range hotspots {
		if e.mustCount < hotspotMinMustCount {
			continue
		}
		out = append(out, fmt.Sprintf("%s (%d must-severity rules)", refToPath(e.ref), e.mustCount))
		if len(out) >= maxDecisionHotspots {
			break
		}
	}
	return out
}

func refToPath(ref string) string {
	return strings.TrimPrefix(ref, "e:")
}

func moduleOf(ref string) string {
	path := refToPath(ref)
	dir := filepath.Dir(path)
	if dir == "." || dir == "/" || dir == "" {
		return "(root)"
	}
	return dir
}

// renderArchQuickReference returns the Markdown block. Empty topology
// yields the empty string so callers can append unconditionally.
func renderArchQuickReference(t *Topology) string {
	if t == nil || t.IsEmpty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Architecture Quick Reference\n\n")
	b.WriteString("> Derived from the typed-event graph: import fan-in/out and decision_link counts. Updated by `pcke sync`.\n\n")

	if len(t.EntryPoints) > 0 {
		b.WriteString("### Entry points\n\nLow fan-in, high fan-out — start reading here.\n\n")
		for _, ep := range t.EntryPoints {
			fmt.Fprintf(&b, "- `%s`\n", ep)
		}
		b.WriteString("\n")
	}
	if len(t.CoreModules) > 0 {
		b.WriteString("### Core modules\n\nHigh fan-in — touching these is consequential.\n\n")
		for _, cm := range t.CoreModules {
			fmt.Fprintf(&b, "- `%s`\n", cm)
		}
		b.WriteString("\n")
	}
	if len(t.DecisionHotspots) > 0 {
		b.WriteString("### Decision hotspots\n\nFiles with the most binding rules.\n\n")
		for _, dh := range t.DecisionHotspots {
			fmt.Fprintf(&b, "- `%s`\n", dh)
		}
		b.WriteString("\n")
	}
	return b.String()
}

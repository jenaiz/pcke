package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jenaiz/pcke/internal/analysis"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/graph"
	"github.com/jenaiz/pcke/internal/output"
)

// ProactiveContext holds additional context that the server suggests when it
// detects the agent's query relates to a module with associated constraints or
// history.
//
// Proactive context is opt-in (disabled by default). When enabled via config
// `[mcp] proactive_context = true`, tool responses may include an extra
// "suggested_context" field alongside the primary result.
type ProactiveContext struct {
	// Module is the module that the suggestion relates to.
	Module string `json:"module,omitempty"`
	// Constraints contains relevant engineering rules for the module.
	Constraints string `json:"constraints,omitempty"`
	// History contains recent evolution history for the module.
	History string `json:"history,omitempty"`
	// Warnings is the set of must-severity decisions reachable from the
	// matched module's entities via decision_link. These are the
	// "thou-shalt-not" rules an agent editing this module needs to
	// know before writing code. Filled by F13.T5; empty when the
	// typed-event log has no must-severity decision links for any
	// file in the module.
	Warnings []DecisionWarning `json:"warnings,omitempty"`
}

// DecisionWarning is one binding rule attached to the matched module.
// Fields mirror event.Decision but are flattened for JSON consumers.
type DecisionWarning struct {
	DID      string `json:"did"`
	Title    string `json:"title"`
	Body     string `json:"body,omitempty"`
	Severity string `json:"severity"`
	Source   string `json:"source,omitempty"`
}

// SuggestContext analyzes a tool query and returns proactive context if a
// module can be inferred from the input. It returns nil when no relevant
// context is found or proactive context is disabled.
//
// The detection is intentionally simple: it checks whether any known module
// name appears in the query text. This avoids complex NLP while still
// providing value in the common case.
func (s *Server) SuggestContext(ctx context.Context, query string, enabled bool) (*ProactiveContext, error) {
	if !enabled {
		return nil, nil
	}

	nodes, err := s.loadNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp: suggest context: %w", err)
	}

	// Collect known modules.
	modules := map[string]bool{}
	for _, n := range nodes {
		if n.Module != "" {
			modules[n.Module] = true
		}
	}

	// Find the first module mentioned in the query.
	queryLower := strings.ToLower(query)
	var matched string
	for mod := range modules {
		if strings.Contains(queryLower, strings.ToLower(mod)) {
			if matched == "" || len(mod) > len(matched) {
				matched = mod // prefer longest match
			}
		}
	}

	if matched == "" {
		return nil, nil
	}

	// Filter nodes for the matched module.
	filtered := filterNodesByModule(nodes, matched)
	if len(filtered) == 0 {
		return nil, nil
	}

	pc := &ProactiveContext{
		Module:      matched,
		Constraints: output.RenderConstraints(filtered),
		History:     output.RenderHistory(filtered),
	}

	warnings, err := s.decisionWarningsForModule(ctx, filtered)
	if err != nil {
		return nil, fmt.Errorf("mcp: suggest decisions: %w", err)
	}
	pc.Warnings = warnings

	return pc, nil
}

// decisionWarningsForModule returns must-severity decisions reachable
// via `decision_link` from any entity in the matched module. It is a
// graceful no-op on a legacy KB that has no typed-event log: the
// graph traversal returns an empty result and warnings stays empty.
//
// Results are deduplicated by DID and sorted by DID for stable output.
// Only the latest version of each decision is returned, and only when
// its current Severity is Must — superseded `should` versions of a
// formerly-`must` rule are intentionally excluded.
func (s *Server) decisionWarningsForModule(
	ctx context.Context,
	nodes []analysis.KnowledgeNode,
) ([]DecisionWarning, error) {
	store := event.New(s.db)
	opts := graph.TraversalOptions{
		Direction: graph.Forward,
		MaxDepth:  1,
		EdgeTypes: []string{"decision_link"},
	}
	seen := map[string]struct{}{}
	var out []DecisionWarning
	for _, n := range nodes {
		if n.FilePath == "" {
			continue
		}
		refs, err := graph.Neighbors(ctx, s.db, graph.Ref("e:"+n.FilePath), opts)
		if err != nil {
			return nil, fmt.Errorf("neighbors %q: %w", n.FilePath, err)
		}
		for _, r := range refs {
			did, ok := strings.CutPrefix(string(r), "d:")
			if !ok {
				continue
			}
			if _, dup := seen[did]; dup {
				continue
			}
			seen[did] = struct{}{}
			evt, err := store.Latest(ctx, event.KindDecision, did)
			if err != nil {
				continue
			}
			d, ok := evt.(*event.Decision)
			if !ok || d.Severity != event.SeverityMust {
				continue
			}
			out = append(out, DecisionWarning{
				DID:      d.DID,
				Title:    d.Title,
				Body:     d.Body,
				Severity: "must",
				Source:   d.Source,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DID < out[j].DID })
	return out, nil
}

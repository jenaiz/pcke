package mcp

import (
	"context"
	"fmt"
	"strings"

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

	return pc, nil
}

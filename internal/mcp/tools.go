package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jenaiz/pcke/internal/analysis"
	"github.com/jenaiz/pcke/internal/onboard"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// registerTools adds all MCP tools to the server.
func (s *Server) registerTools() {
	// recall — BM25-style full-text search over knowledge nodes.
	s.srv.AddTool(mcplib.NewTool("recall",
		mcplib.WithDescription("Search the knowledge base for files, modules, and code entities"),
		mcplib.WithString("query",
			mcplib.Required(),
			mcplib.Description("Search query text"),
		),
		mcplib.WithNumber("limit",
			mcplib.Description("Maximum results to return (default 10)"),
		),
	), s.handleRecall)

	// get_module_context — module summary with files, deps, stability.
	s.srv.AddTool(mcplib.NewTool("get_module_context",
		mcplib.WithDescription("Get summary of a module: files, dependencies, stability, entities"),
		mcplib.WithString("module",
			mcplib.Required(),
			mcplib.Description("Module name (e.g. 'cmd/pcke', 'internal/kdb')"),
		),
	), s.handleGetModuleContext)

	// get_constraints — engineering rules applicable to a scope.
	s.srv.AddTool(mcplib.NewTool("get_constraints",
		mcplib.WithDescription("List engineering constraints and rules for the project"),
		mcplib.WithString("scope",
			mcplib.Description("Filter scope: 'global', 'module:<name>', or 'file:<path>'. Default: global"),
		),
	), s.handleGetConstraints)

	// get_history — evolution timeline for a file.
	s.srv.AddTool(mcplib.NewTool("get_history",
		mcplib.WithDescription("Get the change history (evolution logs) for a file"),
		mcplib.WithString("file_path",
			mcplib.Required(),
			mcplib.Description("Relative file path (e.g. 'internal/kdb/db.go')"),
		),
	), s.handleGetHistory)

	// get_onboarding — auto-generated project walkthrough.
	s.srv.AddTool(mcplib.NewTool("get_onboarding",
		mcplib.WithDescription("Generate a project walkthrough for new developers: architecture, entry points, key modules, conventions"),
		mcplib.WithString("section",
			mcplib.Description("Filter to a specific section (e.g. 'overview', 'architecture', 'entry_points', 'key_modules', 'conventions', 'constraints', 'decisions')"),
		),
		mcplib.WithString("module",
			mcplib.Description("Scope walkthrough to a specific module"),
		),
		mcplib.WithString("depth",
			mcplib.Description("Walkthrough depth: 'shallow' or 'full' (default: full)"),
		),
	), s.handleGetOnboarding)
}

// handleRecall performs a text search over knowledge nodes.
// Uses simple substring matching on node fields; FTS/BM25 wiring is
// planned for a future phase.
func (s *Server) handleRecall(
	ctx context.Context,
	request mcplib.CallToolRequest,
) (*mcplib.CallToolResult, error) {
	query, err := request.RequireString("query")
	if err != nil {
		return mcplib.NewToolResultError("query parameter required"), nil
	}

	limit := 10
	if l := request.GetFloat("limit", 0); l > 0 {
		limit = int(l)
	}

	nodes, err := s.loadNodes(ctx)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("load nodes: %v", err)), nil
	}

	query = strings.ToLower(query)
	terms := strings.Fields(query)

	type scored struct {
		score int
		json  string
	}
	var results []scored

	for _, node := range nodes {
		if score := scoreNode(node, terms); score > 0 {
			results = append(results, scored{score: score, json: nodeToJSON(node)})
		}
	}

	// Sort by score descending.
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if len(results) > limit {
		results = results[:limit]
	}

	if len(results) == 0 {
		return mcplib.NewToolResultText("No results found."), nil
	}

	sw := NewStreamWriter(ctx, 0, 0)
	for _, r := range results {
		if err := sw.WriteItem(r.json); err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("stream: %v", err)), nil
		}
	}

	text, err := sw.Flush()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("stream: %v", err)), nil
	}
	return mcplib.NewToolResultText(text), nil
}

// handleGetModuleContext returns the context for a specific module.
func (s *Server) handleGetModuleContext(
	ctx context.Context,
	request mcplib.CallToolRequest,
) (*mcplib.CallToolResult, error) {
	module, err := request.RequireString("module")
	if err != nil {
		return mcplib.NewToolResultError("module parameter required"), nil
	}

	nodes, err := s.loadNodes(ctx)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("load nodes: %v", err)), nil
	}

	rels, err := s.loadRelations(ctx)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("load relations: %v", err)), nil
	}

	// Filter nodes belonging to the module.
	var moduleNodes []map[string]any
	var totalStability float64
	fileSet := map[string]bool{}

	for _, node := range nodes {
		if node.Module != module {
			continue
		}
		fileSet[node.FilePath] = true
		totalStability += node.Stability
		entry := map[string]any{
			"file":     node.FilePath,
			"language": node.Language,
			"class":    node.Class,
		}
		if len(node.Entities) > 0 {
			entry["entities"] = len(node.Entities)
		}
		moduleNodes = append(moduleNodes, entry)
	}

	if len(moduleNodes) == 0 {
		return mcplib.NewToolResultText(
			fmt.Sprintf("Module %q not found in knowledge base.", module),
		), nil
	}

	// Collect dependencies from relations.
	var deps []string
	seen := map[string]bool{}
	for _, rel := range rels {
		if rel.Type == "imports" && fileSet[rel.SourceNodeID] {
			if !seen[rel.TargetNodeID] {
				seen[rel.TargetNodeID] = true
				deps = append(deps, rel.TargetNodeID)
			}
		}
	}
	sort.Strings(deps)

	avgStability := float64(0)
	if len(moduleNodes) > 0 {
		avgStability = totalStability / float64(len(moduleNodes))
	}

	result := map[string]any{
		"module":    module,
		"files":     len(moduleNodes),
		"stability": fmt.Sprintf("%.2f", avgStability),
		"nodes":     moduleNodes,
	}
	if len(deps) > 0 {
		result["dependencies"] = deps
	}

	return mcplib.NewToolResultText(nodeToJSON(result)), nil
}

// handleGetConstraints returns engineering constraints for the project.
func (s *Server) handleGetConstraints(
	ctx context.Context,
	request mcplib.CallToolRequest,
) (*mcplib.CallToolResult, error) {
	nodes, err := s.loadNodes(ctx)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("load nodes: %v", err)), nil
	}

	// Detect constraints from project structure (same logic as output renderer).
	var constraints []map[string]string

	hasGo := false
	hasInfra := false
	for _, n := range nodes {
		if n.Language == "Go" {
			hasGo = true
		}
		if n.Class == "infra" {
			hasInfra = true
		}
	}

	if hasGo {
		constraints = append(constraints,
			map[string]string{
				"scope":    "global",
				"severity": "must",
				"rule":     "All code must compile with `go build ./...`",
				"source":   "auto",
			},
			map[string]string{
				"scope":    "global",
				"severity": "must",
				"rule":     "Tests must pass with `go test -race ./...`",
				"source":   "auto",
			},
			map[string]string{
				"scope":    "global",
				"severity": "should",
				"rule":     "Linting via golangci-lint (see `.golangci.yml`)",
				"source":   "auto",
			},
		)
	}
	if hasInfra {
		constraints = append(constraints, map[string]string{
			"scope":    "global",
			"severity": "should",
			"rule":     "Infrastructure-as-code files detected; changes require review",
			"source":   "auto",
		})
	}

	if len(constraints) == 0 {
		return mcplib.NewToolResultText("No constraints detected."), nil
	}

	// Apply scope filter if provided.
	scope := request.GetString("scope", "")
	if scope != "" {
		var filtered []map[string]string
		for _, c := range constraints {
			if c["scope"] == scope || scope == "global" {
				filtered = append(filtered, c)
			}
		}
		constraints = filtered
	}

	return mcplib.NewToolResultText(nodeToJSON(constraints)), nil
}

// handleGetHistory returns evolution logs for a file path.
func (s *Server) handleGetHistory(
	ctx context.Context,
	request mcplib.CallToolRequest,
) (*mcplib.CallToolResult, error) {
	filePath, err := request.RequireString("file_path")
	if err != nil {
		return mcplib.NewToolResultError("file_path parameter required"), nil
	}

	logs, err := s.loadEvolutionLogs(ctx)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("load logs: %v", err)), nil
	}

	var matching []map[string]string
	for _, log := range logs {
		if log.NodeID == filePath {
			matching = append(matching, map[string]string{
				"commit_hash": log.CommitHash,
				"change_type": log.ChangeType,
				"author":      log.Author,
				"timestamp":   log.Timestamp.Format("2006-01-02T15:04:05Z"),
			})
		}
	}

	if len(matching) == 0 {
		return mcplib.NewToolResultText(
			fmt.Sprintf("No history found for %q.", filePath),
		), nil
	}

	return mcplib.NewToolResultText(nodeToJSON(matching)), nil
}

// scoreNode computes a relevance score for a knowledge node against the
// given search terms. Returns 0 when none of the terms match.
func scoreNode(node analysis.KnowledgeNode, terms []string) int {
	text := strings.ToLower(
		node.Name + " " + node.FilePath + " " + node.Language + " " +
			node.Module + " " + node.Class,
	)

	score := 0
	for _, term := range terms {
		if strings.Contains(text, term) {
			score++
		}
	}

	// Entity name matches are weighted higher.
	for _, ent := range node.Entities {
		for _, term := range terms {
			if strings.Contains(strings.ToLower(ent.Name), term) {
				score += 2
			}
		}
	}

	return score
}

// handleGetOnboarding generates a project walkthrough.
func (s *Server) handleGetOnboarding(
	ctx context.Context,
	request mcplib.CallToolRequest,
) (*mcplib.CallToolResult, error) {
	w, err := s.buildOnboarding(ctx, request)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("onboarding: %v", err)), nil
	}

	text, err := onboard.RenderJSON(w)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("render: %v", err)), nil
	}

	if len(text) > 4096 {
		sw := NewStreamWriter(ctx, 0, 0)
		if err := sw.WriteItem(text); err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("stream: %v", err)), nil
		}
		flushed, err := sw.Flush()
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("stream: %v", err)), nil
		}
		return mcplib.NewToolResultText(flushed), nil
	}

	return mcplib.NewToolResultText(text), nil
}

func (s *Server) buildOnboarding(ctx context.Context, request mcplib.CallToolRequest) (*onboard.Walkthrough, error) {
	nodes, err := s.loadNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("load nodes: %w", err)
	}

	rels, err := s.loadRelations(ctx)
	if err != nil {
		return nil, fmt.Errorf("load relations: %w", err)
	}

	logs, err := s.loadEvolutionLogs(ctx)
	if err != nil {
		return nil, fmt.Errorf("load evolution logs: %w", err)
	}

	cfg, err := onboard.LoadConfig(s.root)
	if err != nil {
		cfg = onboard.DefaultConfig()
	}

	engine := &onboard.Engine{
		Nodes:     nodes,
		Relations: rels,
		EvolLogs:  logs,
		RepoPath:  s.root,
		Config:    cfg,
	}

	module := request.GetString("module", "")
	depth := request.GetString("depth", "full")
	section := request.GetString("section", "")

	var w *onboard.Walkthrough
	if module != "" {
		w, err = engine.GenerateForModule(ctx, module)
	} else {
		w, err = engine.Generate(ctx)
	}
	if err != nil {
		return nil, err
	}

	if depth == "shallow" && len(w.Sections) > 3 {
		w.Sections = w.Sections[:3]
	}

	if section != "" {
		var filtered []onboard.Section
		for _, s := range w.Sections {
			if s.Name == section {
				filtered = append(filtered, s)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("section %q not found", section)
		}
		w.Sections = filtered
	}

	return w, nil
}

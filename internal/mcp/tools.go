package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jenaiz/pcke/internal/analysis"
	//nolint:staticcheck // SA1019: federation is intentionally retained while frozen; MCP surface keeps the existing tools wired (PRD v5.2 §2).
	"github.com/jenaiz/pcke/internal/federation"
	"github.com/jenaiz/pcke/internal/onboard"
	"github.com/jenaiz/pcke/internal/output"
	"github.com/jenaiz/pcke/internal/retrieval"
	"github.com/jenaiz/pcke/internal/retrieval/session"
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

	// query_federation — executes DSL query across all federated repos.
	s.srv.AddTool(mcplib.NewTool("query_federation",
		mcplib.WithDescription("Execute a pcke DSL query across all federated repositories"),
		mcplib.WithString("query",
			mcplib.Required(),
			mcplib.Description("DSL query to execute (e.g. 'nodes WHERE module = \"internal/kdb\"')"),
		),
		mcplib.WithString("repos",
			mcplib.Description("Comma-separated repo names to query (empty = all federated repos)"),
		),
		mcplib.WithNumber("limit",
			mcplib.Description("Maximum total results (default 50)"),
		),
	), s.handleQueryFederation)

	// get_cross_repo_deps — returns cross-repo dependency graph.
	s.srv.AddTool(mcplib.NewTool("get_cross_repo_deps",
		mcplib.WithDescription("Get cross-repository dependencies for a module or node"),
		mcplib.WithString("node_id",
			mcplib.Description("Filter to dependencies involving this node"),
		),
		mcplib.WithString("module",
			mcplib.Description("Filter to dependencies involving this module"),
		),
		mcplib.WithString("direction",
			mcplib.Description("Direction: 'incoming', 'outgoing', or 'both' (default 'both')"),
		),
	), s.handleGetCrossRepoDeps)

	// get_context_for_diff — typed-event subgraph for a set of changed
	// files, ranked + budgeted via the retrieval engine. If
	// changed_files is empty, auto-detects via `git status` on s.root.
	s.srv.AddTool(mcplib.NewTool("get_context_for_diff",
		mcplib.WithDescription("Get ranked, budget-bounded context for the union of subgraphs around a set of changed files. If changed_files is empty, auto-detects via git worktree status."),
		mcplib.WithString("changed_files",
			mcplib.Description("Comma-separated repository-relative paths. If empty, auto-detected from `git status`."),
		),
		mcplib.WithNumber("budget",
			mcplib.Description("Approximate token budget; 0 or unset = engine default (2000)"),
		),
		mcplib.WithString("workflow",
			mcplib.Description("Caller's current task: explore | bugfix | feature | review | refactor | test"),
		),
		mcplib.WithString("focus",
			mcplib.Description("Narrow selection: all | constraints | history | patterns | impact"),
		),
		mcplib.WithString("already_served",
			mcplib.Description("Comma-separated refs the agent already has in context"),
		),
		mcplib.WithString("session_id",
			mcplib.Description("Opaque session identifier; paired with already_served for novelty tracking"),
		),
	), s.handleGetContextForDiff)

	// get_context_for_file — typed-event subgraph for a single file,
	// ranked + budgeted via the retrieval engine.
	s.srv.AddTool(mcplib.NewTool("get_context_for_file",
		mcplib.WithDescription("Get the ranked, budget-bounded context subgraph for a single file: entities in its 2-hop neighborhood, applicable decisions, and linked references."),
		mcplib.WithString("file_path",
			mcplib.Required(),
			mcplib.Description("Repository-relative file path (e.g. 'internal/kdb/db.go')"),
		),
		mcplib.WithNumber("budget",
			mcplib.Description("Approximate token budget; 0 or unset = engine default (2000)"),
		),
		mcplib.WithString("workflow",
			mcplib.Description("Caller's current task: explore | bugfix | feature | review | refactor | test"),
		),
		mcplib.WithString("focus",
			mcplib.Description("Narrow selection: all | constraints | history | patterns | impact"),
		),
		mcplib.WithString("already_served",
			mcplib.Description("Comma-separated refs the agent already has in context (lowers their novelty score)"),
		),
		mcplib.WithString("session_id",
			mcplib.Description("Opaque session identifier; paired with already_served for novelty tracking"),
		),
	), s.handleGetContextForFile)

	// set_workflow — record the caller's current workflow on a session so
	// subsequent context tools inherit it without re-specifying (F15.T6).
	s.srv.AddTool(mcplib.NewTool("set_workflow",
		mcplib.WithDescription("Set the active workflow for a session. Subsequent get_context_for_file / get_context_for_diff calls on the same session inherit it (unless they pass their own workflow). Tunes ranker weights and edge priorities."),
		mcplib.WithString("session_id",
			mcplib.Required(),
			mcplib.Description("Opaque session identifier the workflow applies to"),
		),
		mcplib.WithString("workflow",
			mcplib.Required(),
			mcplib.Description("One of: explore | bugfix | feature | review | refactor | test"),
		),
	), s.handleSetWorkflow)
}

// handleSetWorkflow records the caller's workflow on a session so later
// context tools inherit it. Returns the stored workflow as JSON.
func (s *Server) handleSetWorkflow(
	_ context.Context,
	request mcplib.CallToolRequest,
) (*mcplib.CallToolResult, error) {
	sessionID, err := request.RequireString("session_id")
	if err != nil {
		return mcplib.NewToolResultError("session_id parameter required"), nil
	}
	wfRaw, err := request.RequireString("workflow")
	if err != nil {
		return mcplib.NewToolResultError("workflow parameter required"), nil
	}
	wf, ok := parseWorkflow(wfRaw)
	if !ok {
		return mcplib.NewToolResultError(fmt.Sprintf(
			"unknown workflow %q; want one of: explore bugfix feature review refactor test", wfRaw)), nil
	}

	s.setSessionWorkflow(sessionID, wf)

	out, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"workflow":   string(wf),
		"ok":         true,
	})
	return mcplib.NewToolResultText(string(out)), nil
}

// parseWorkflow validates a workflow string against the known set,
// returning the typed value and whether it was recognised. Empty maps
// to WorkflowExplore.
func parseWorkflow(s string) (retrieval.Workflow, bool) {
	switch retrieval.Workflow(strings.ToLower(strings.TrimSpace(s))) {
	case retrieval.WorkflowExplore, "":
		return retrieval.WorkflowExplore, true
	case retrieval.WorkflowBugfix:
		return retrieval.WorkflowBugfix, true
	case retrieval.WorkflowFeature:
		return retrieval.WorkflowFeature, true
	case retrieval.WorkflowReview:
		return retrieval.WorkflowReview, true
	case retrieval.WorkflowRefactor:
		return retrieval.WorkflowRefactor, true
	case retrieval.WorkflowTest:
		return retrieval.WorkflowTest, true
	default:
		return "", false
	}
}

// setSessionWorkflow records wf for sessionID. Empty sessionID is a
// no-op (anonymous calls can't inherit a workflow).
func (s *Server) setSessionWorkflow(sessionID string, wf retrieval.Workflow) {
	if sessionID == "" {
		return
	}
	s.workflowMu.Lock()
	s.workflows[sessionID] = wf
	s.workflowMu.Unlock()
}

// sessionWorkflow returns the workflow stored for sessionID, or empty
// (WorkflowExplore semantics) when none is set.
func (s *Server) sessionWorkflow(sessionID string) retrieval.Workflow {
	if sessionID == "" {
		return ""
	}
	s.workflowMu.Lock()
	defer s.workflowMu.Unlock()
	return s.workflows[sessionID]
}

// resolveWorkflow picks the effective workflow for a context call: an
// explicit per-call parameter wins; otherwise the session's stored
// workflow (set via set_workflow) applies.
func (s *Server) resolveWorkflow(sessionID, param string) retrieval.Workflow {
	if w, ok := parseWorkflow(param); ok && w != retrieval.WorkflowExplore {
		return w
	}
	// param was empty or explicitly "explore": fall back to the session.
	if strings.TrimSpace(param) == "" {
		return s.sessionWorkflow(sessionID)
	}
	return retrieval.WorkflowExplore
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

	decisions, err := output.LoadDecisions(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("load decisions: %w", err)
	}

	cfg, err := onboard.LoadConfig(s.root)
	if err != nil {
		cfg = onboard.DefaultConfig()
	}

	engine := &onboard.Engine{
		Nodes:     nodes,
		Relations: rels,
		EvolLogs:  logs,
		Decisions: decisions,
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

// handleQueryFederation executes a DSL query across federated repos.
func (s *Server) handleQueryFederation(
	ctx context.Context,
	request mcplib.CallToolRequest,
) (*mcplib.CallToolResult, error) {
	dsl, err := request.RequireString("query")
	if err != nil {
		return mcplib.NewToolResultError("query parameter required"), nil
	}

	limit := 50
	if l := request.GetFloat("limit", 0); l > 0 {
		limit = int(l)
	}

	manifest, err := federation.LoadManifest()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("load federation manifest: %v", err)), nil
	}
	if len(manifest.Repos) == 0 {
		return mcplib.NewToolResultText("No federated repos configured. Use `pcke federation add` to add repos."), nil
	}

	opts := federation.QueryOpts{Limit: limit}
	reposStr := request.GetString("repos", "")
	if reposStr != "" {
		opts.RepoFilter = strings.Split(reposStr, ",")
		for i := range opts.RepoFilter {
			opts.RepoFilter[i] = strings.TrimSpace(opts.RepoFilter[i])
		}
	}

	rs, err := federation.QueryFederation(ctx, manifest, dsl, opts)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("federation query: %v", err)), nil
	}

	result := map[string]any{
		"total_results":    len(rs.Results),
		"repos_queried":    rs.Repos,
		"partial_failures": len(rs.Errors),
	}

	rows := make([]map[string]any, 0, len(rs.Results))
	for _, r := range rs.Results {
		rows = append(rows, r.Row)
	}
	result["results"] = rows

	if len(rs.Errors) > 0 {
		errs := make([]map[string]string, 0, len(rs.Errors))
		for _, e := range rs.Errors {
			errs = append(errs, map[string]string{
				"repo":  e.Repo,
				"error": e.Error.Error(),
			})
		}
		result["errors"] = errs
	}

	out, _ := json.MarshalIndent(result, "", "  ")
	text := string(out)

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

// handleGetCrossRepoDeps returns cross-repo dependencies.
func (s *Server) handleGetCrossRepoDeps(
	ctx context.Context,
	request mcplib.CallToolRequest,
) (*mcplib.CallToolResult, error) {
	manifest, err := federation.LoadManifest()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("load federation manifest: %v", err)), nil
	}

	deps, err := federation.DetectCrossRepoDeps(ctx, manifest, s.root)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("detect deps: %v", err)), nil
	}

	nodeID := request.GetString("node_id", "")
	module := request.GetString("module", "")
	direction := request.GetString("direction", "both")

	filtered := filterCrossRepoDeps(deps, nodeID, module, direction)

	result := map[string]any{
		"total_deps": len(filtered),
		"deps":       filtered,
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	return mcplib.NewToolResultText(string(out)), nil
}

// filterCrossRepoDeps applies node/module/direction filters to the dependency list.
func filterCrossRepoDeps(deps []federation.CrossRepoDep, nodeID, module, direction string) []federation.CrossRepoDep {
	var filtered []federation.CrossRepoDep
	for _, d := range deps {
		if nodeID != "" && !matchesDirection(d, nodeID, direction) {
			continue
		}
		if module != "" && d.TargetModule != module && d.SourceNodeID != module {
			continue
		}
		filtered = append(filtered, d)
	}
	if len(filtered) == 0 && len(deps) > 0 {
		return deps
	}
	return filtered
}

func matchesDirection(d federation.CrossRepoDep, nodeID, direction string) bool {
	if direction == "outgoing" || direction == "both" {
		if d.SourceNodeID == nodeID {
			return true
		}
	}
	if direction == "incoming" || direction == "both" {
		if d.TargetModule == nodeID {
			return true
		}
	}
	return false
}

// handleGetContextForDiff assembles a ranked, budget-bounded context
// for a set of changed files. When changed_files is empty, the handler
// asks git for the worktree status and uses the resulting set; if git
// is unavailable or the worktree is clean, it returns a summary-only
// response so callers can distinguish "no changes" from a failure.
//
// Output shape matches get_context_for_file: one JSON line per
// section, then a final _summary item.
func (s *Server) handleGetContextForDiff(
	ctx context.Context,
	request mcplib.CallToolRequest,
) (*mcplib.CallToolResult, error) {
	changed := splitCSV(request.GetString("changed_files", ""))
	autodetected := false
	if len(changed) == 0 {
		detected, derr := analysis.NewGitIntel(s.root)
		if derr == nil {
			files, ferr := detected.ChangedFiles()
			if ferr == nil {
				changed = files
				autodetected = true
			}
		}
	}

	sessionID := request.GetString("session_id", "")
	served := s.servedForSession(sessionID, splitCSV(request.GetString("already_served", "")))

	req := retrieval.Request{
		ChangedFiles:  changed,
		Budget:        int(request.GetFloat("budget", 0)),
		Workflow:      s.resolveWorkflow(sessionID, request.GetString("workflow", "")),
		Focus:         retrieval.Focus(request.GetString("focus", "")),
		SessionID:     sessionID,
		AlreadyServed: served,
	}

	engine := retrieval.New(s.db)
	pkg, err := engine.Assemble(ctx, req)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("assemble: %v", err)), nil
	}

	s.noteSession(sessionID, "get_context_for_diff", pkg)

	if autodetected && len(changed) == 0 {
		pkg.Warnings = append(pkg.Warnings, "git worktree is clean; no changed files to analyse")
	}

	if len(pkg.Sections) == 0 {
		summary := contextForDiffSummary(pkg, changed)
		summaryJSON, _ := json.Marshal(summary)
		return mcplib.NewToolResultText(string(summaryJSON)), nil
	}

	sw := NewStreamWriter(ctx, 0, 0)
	for _, sec := range pkg.Sections {
		b, marshalErr := json.Marshal(sectionPayload(sec))
		if marshalErr != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("marshal section: %v", marshalErr)), nil
		}
		if writeErr := sw.WriteItem(string(b)); writeErr != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("stream: %v", writeErr)), nil
		}
	}
	summary := contextForDiffSummary(pkg, changed)
	summaryJSON, marshalErr := json.Marshal(summary)
	if marshalErr != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("marshal summary: %v", marshalErr)), nil
	}
	if writeErr := sw.WriteItem(string(summaryJSON)); writeErr != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("stream: %v", writeErr)), nil
	}

	text, err := sw.Flush()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("stream flush: %v", err)), nil
	}
	return mcplib.NewToolResultText(text), nil
}

// contextForDiffSummary mirrors contextForFileSummary but also echoes
// the changed-file list back so the caller can verify what the engine
// actually used (especially under git autodetect).
func contextForDiffSummary(pkg *retrieval.ContextPackage, changedFiles []string) map[string]any {
	warns := pkg.Warnings
	if warns == nil {
		warns = []string{}
	}
	files := changedFiles
	if files == nil {
		files = []string{}
	}
	return map[string]any{
		"_summary":      true,
		"tokens_used":   pkg.TokensUsed,
		"budget_limit":  pkg.BudgetLimit,
		"truncated":     pkg.Truncated,
		"warnings":      warns,
		"section_count": len(pkg.Sections),
		"changed_files": files,
	}
}

// handleGetContextForFile assembles a ranked, budget-bounded context
// subgraph for one file. Thin wrapper over retrieval.Engine.Assemble:
// the engine does the work; the handler maps MCP parameters into a
// Request and streams the result back as one JSON line per section
// plus a final summary item.
//
// Output shape per section (one per StreamWriter item):
//
//	{"ref":"e:internal/kdb/db.go","kind":"entity","title":"…",
//	 "body":"…","score":0.81,"tokens":45,"created_at":"…"}
//
// Final summary item:
//
//	{"_summary":true,"tokens_used":1234,"budget_limit":2000,
//	 "truncated":false,"warnings":[],"section_count":7}
func (s *Server) handleGetContextForFile(
	ctx context.Context,
	request mcplib.CallToolRequest,
) (*mcplib.CallToolResult, error) {
	filePath, err := request.RequireString("file_path")
	if err != nil {
		return mcplib.NewToolResultError("file_path parameter required"), nil
	}

	sessionID := request.GetString("session_id", "")
	served := s.servedForSession(sessionID, splitCSV(request.GetString("already_served", "")))

	req := retrieval.Request{
		FilePath:      filePath,
		Budget:        int(request.GetFloat("budget", 0)),
		Workflow:      s.resolveWorkflow(sessionID, request.GetString("workflow", "")),
		Focus:         retrieval.Focus(request.GetString("focus", "")),
		SessionID:     sessionID,
		AlreadyServed: served,
	}

	engine := retrieval.New(s.db)
	pkg, err := engine.Assemble(ctx, req)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("assemble: %v", err)), nil
	}

	s.noteSession(sessionID, "get_context_for_file", pkg)

	if len(pkg.Sections) == 0 {
		summary := contextForFileSummary(pkg)
		summaryJSON, _ := json.Marshal(summary)
		return mcplib.NewToolResultText(string(summaryJSON)), nil
	}

	sw := NewStreamWriter(ctx, 0, 0)
	for _, sec := range pkg.Sections {
		b, marshalErr := json.Marshal(sectionPayload(sec))
		if marshalErr != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("marshal section: %v", marshalErr)), nil
		}
		if writeErr := sw.WriteItem(string(b)); writeErr != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("stream: %v", writeErr)), nil
		}
	}
	summary := contextForFileSummary(pkg)
	summaryJSON, marshalErr := json.Marshal(summary)
	if marshalErr != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("marshal summary: %v", marshalErr)), nil
	}
	if writeErr := sw.WriteItem(string(summaryJSON)); writeErr != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("stream: %v", writeErr)), nil
	}

	text, err := sw.Flush()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("stream flush: %v", err)), nil
	}
	return mcplib.NewToolResultText(text), nil
}

// sectionPayload returns a JSON-marshalable map describing the
// section. Defined as a function because the rendered shape differs
// slightly from the internal retrieval.Section — we format the
// timestamp and drop unexported fields that may exist in the future.
func sectionPayload(sec retrieval.Section) map[string]any {
	return map[string]any{
		"ref":        sec.Ref,
		"kind":       sec.Kind,
		"title":      sec.Title,
		"body":       sec.Body,
		"score":      sec.Score,
		"tokens":     sec.Tokens,
		"created_at": sec.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// contextForFileSummary returns the final summary item the handler
// appends after the per-section stream.
func contextForFileSummary(pkg *retrieval.ContextPackage) map[string]any {
	warns := pkg.Warnings
	if warns == nil {
		warns = []string{}
	}
	return map[string]any{
		"_summary":      true,
		"tokens_used":   pkg.TokensUsed,
		"budget_limit":  pkg.BudgetLimit,
		"truncated":     pkg.Truncated,
		"warnings":      warns,
		"section_count": len(pkg.Sections),
	}
}

// servedForSession returns the union of (a) refs the caller explicitly
// flagged via already_served and (b) refs the server has already served
// in the named session, so novelty scoring covers both sources.
//
// Empty sessionID means "no server-side tracking" — only the caller-
// supplied list is returned. Phase 14 will swap MemoryStore for a
// kdb-backed Store behind the same interface.
func (s *Server) servedForSession(sessionID string, callerSupplied []string) []string {
	if sessionID == "" {
		return callerSupplied
	}
	sess := s.sessions.Get(sessionID)
	prior := sess.Served()
	if len(prior) == 0 {
		return callerSupplied
	}
	if len(callerSupplied) == 0 {
		return prior
	}
	seen := make(map[string]struct{}, len(prior)+len(callerSupplied))
	out := make([]string, 0, len(prior)+len(callerSupplied))
	for _, r := range prior {
		if _, dup := seen[r]; dup {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	for _, r := range callerSupplied {
		if _, dup := seen[r]; dup {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	return out
}

// noteSession records the refs the engine actually returned so the
// next call on the same session inherits them as already-served.
// Empty sessionID short-circuits. tool is the MCP tool name; the
// persistent session impl (Phase 14) stamps it on the resulting
// ToolCall observation.
func (s *Server) noteSession(sessionID, tool string, pkg *retrieval.ContextPackage) {
	if sessionID == "" || pkg == nil || len(pkg.Sections) == 0 {
		return
	}
	refs := make([]string, 0, len(pkg.Sections))
	for _, sec := range pkg.Sections {
		refs = append(refs, sec.Ref)
	}
	s.sessions.Get(sessionID).Note(session.Observation{
		Refs: refs,
		At:   time.Now().UTC(),
		Tool: tool,
	})
}

// splitCSV trims and returns non-empty comma-separated values.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

package context

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jenaiz/pcke/internal/analysis"
	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// Engine assembles ranked, budgeted context for MCP responses.
type Engine struct {
	db      *kdb.DB
	root    string
	session *Session
	cfg     Config
}

// rawNode extends KnowledgeNode with extra fields from storage (e.g. severity, scope).
type rawNode struct {
	analysis.KnowledgeNode
	Severity string `json:"severity,omitempty"`
	Scope    string `json:"scope,omitempty"`
}

// Config holds context engine configuration.
type Config struct {
	Budget            int     // Default token budget (2000)
	TokenMultiplier   float64 // Words × multiplier ≈ tokens (1.3)
	Weights           Weights
	ProactiveWarnings bool // Inject warnings for must-severity constraints
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Budget:            2000,
		TokenMultiplier:   1.3,
		Weights:           DefaultWeights(),
		ProactiveWarnings: true,
	}
}

// Request specifies what context to assemble.
type Request struct {
	FilePath     string   // Target file (for file-scoped requests)
	ChangedFiles []string // Changed files (for diff-scoped requests)
	Workflow     string   // "bugfix", "feature", "review", "refactor", "test", "explore"
	Budget       int      // Max approximate token count (default 2000)
	Focus        string   // "constraints", "history", "patterns", "impact", "all"
	SessionID    string   // For novelty scoring (empty = no tracking)
}

// Package is the assembled context response.
type Package struct {
	Sections   []Section // Ordered by relevance score, truncated at budget
	TokensUsed int       // Approximate tokens consumed
	Warnings   []Warning // Proactive warnings (always included, free budget)
	Workflow   string    // Detected or explicit workflow
}

// Section is a single context item in a response.
type Section struct {
	Type     string  // "constraint", "history", "pattern", "decision", "impact"
	Title    string  // Human-readable title
	Content  string  // Markdown content
	Score    float64 // Ranking score [0, 1]
	Source   string  // "annotation", "auto", "note", "evolution_log"
	FilePath string  // Most specific file (empty for global)
	Module   string  // Module scope
}

// Warning is a proactive constraint warning always included in responses.
type Warning struct {
	Severity  string // "must", "should"
	Rule      string // Constraint text
	Source    string // "annotation", "auto", "manual"
	AppliesTo string // File or module path
}

// NewEngine creates an Engine backed by the given kdb database.
func NewEngine(db *kdb.DB, root string, cfg Config) *Engine {
	if cfg.Budget <= 0 {
		cfg.Budget = 2000
	}
	if cfg.TokenMultiplier <= 0 {
		cfg.TokenMultiplier = 1.3
	}
	return &Engine{db: db, root: root, cfg: cfg}
}

// SetSession attaches a session for novelty tracking.
func (e *Engine) SetSession(s *Session) {
	e.session = s
}

// Assemble produces a ranked, budget-constrained context package.
func (e *Engine) Assemble(ctx context.Context, req Request) (*Package, error) {
	budget := req.Budget
	if budget <= 0 {
		budget = e.cfg.Budget
	}

	nodes, err := e.loadNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("context: load nodes: %w", err)
	}

	logs, err := e.loadEvolutionLogs(ctx)
	if err != nil {
		return nil, fmt.Errorf("context: load evolution logs: %w", err)
	}

	rels, err := e.loadRelations(ctx)
	if err != nil {
		return nil, fmt.Errorf("context: load relations: %w", err)
	}

	// Determine target module from file path.
	targetModule := ""
	if req.FilePath != "" {
		targetModule = moduleForFile(nodes, req.FilePath)
	}

	// Build sections from different sources.
	var sections []Section

	// 1. Constraints from annotations and manual rules.
	constraints := e.buildConstraints(nodes, req.FilePath, targetModule)
	sections = append(sections, constraints...)

	// 2. Evolution history.
	history := e.buildHistory(logs, nodes, req.FilePath, req.ChangedFiles)
	sections = append(sections, history...)

	// 3. Impact radius (reverse deps).
	impact := e.buildImpact(rels, nodes, req.FilePath)
	sections = append(sections, impact...)

	// 4. Patterns from entities.
	patterns := e.buildPatterns(nodes, req.FilePath, targetModule)
	sections = append(sections, patterns...)

	// Score all sections.
	for i := range sections {
		sections[i].Score = e.scoreSection(sections[i], req.FilePath, targetModule)
	}

	// Sort by score descending.
	sort.Slice(sections, func(i, j int) bool {
		return sections[i].Score > sections[j].Score
	})

	// Filter by focus if specified.
	if req.Focus != "" && req.Focus != "all" {
		sections = filterByFocus(sections, req.Focus)
	}

	// Extract warnings (free, always included).
	var warnings []Warning
	if e.cfg.ProactiveWarnings {
		warnings = e.extractWarnings(nodes, req.FilePath, targetModule)
	}

	// Truncate to budget.
	truncated, tokensUsed := TruncateToBudget(sections, budget, e.cfg.TokenMultiplier)

	// Mark served items in session.
	if e.session != nil {
		for _, s := range truncated {
			e.session.MarkServed(sectionKey(s))
		}
		if req.FilePath != "" {
			e.session.MarkFileAccessed(req.FilePath)
		}
	}

	pkg := &Package{
		Sections:   truncated,
		TokensUsed: tokensUsed,
		Warnings:   warnings,
		Workflow:   req.Workflow,
	}

	return pkg, nil
}

// buildConstraints extracts constraint sections from knowledge nodes.
func (e *Engine) buildConstraints(nodes []rawNode, filePath, module string) []Section {
	var sections []Section

	for _, n := range nodes {
		if n.Type != "rule" && n.Class != "constraint" {
			continue
		}

		proximity := "global"
		if n.FilePath != "" && n.FilePath == filePath {
			proximity = "file"
		} else if n.Module != "" && n.Module == module {
			proximity = "module"
		}

		severity := n.Severity
		if severity == "" {
			severity = "should"
		}

		sections = append(sections, Section{
			Type:     "constraint",
			Title:    n.Name,
			Content:  fmt.Sprintf("**[%s]** %s", severity, n.Name),
			Source:   n.Source,
			FilePath: n.FilePath,
			Module:   n.Module,
		})

		_ = proximity
	}

	return sections
}

// buildHistory creates history sections from evolution logs.
func (e *Engine) buildHistory(logs []analysis.EvolutionLog, nodes []rawNode, filePath string, changedFiles []string) []Section {
	// Map node IDs to file paths.
	nodeFiles := make(map[string]string, len(nodes))
	for _, n := range nodes {
		nodeFiles[n.ID] = n.FilePath
	}

	// Filter logs relevant to the request.
	var relevant []analysis.EvolutionLog
	for _, log := range logs {
		fp := nodeFiles[log.NodeID]
		if filePath != "" && fp == filePath {
			relevant = append(relevant, log)
		} else if len(changedFiles) > 0 {
			for _, cf := range changedFiles {
				if fp == cf {
					relevant = append(relevant, log)
					break
				}
			}
		}
	}

	// Sort by timestamp descending, limit to 5.
	sort.Slice(relevant, func(i, j int) bool {
		return relevant[i].Timestamp.After(relevant[j].Timestamp)
	})
	if len(relevant) > 5 {
		relevant = relevant[:5]
	}

	if len(relevant) == 0 {
		return nil
	}

	var content strings.Builder
	for _, log := range relevant {
		fmt.Fprintf(&content, "- %s: %s by %s (%s)\n",
			log.Timestamp.Format("2006-01-02"),
			log.ChangeType,
			log.Author,
			log.CommitHash[:minInt(7, len(log.CommitHash))],
		)
	}

	return []Section{{
		Type:     "history",
		Title:    "Recent Changes",
		Content:  content.String(),
		Source:   "evolution_log",
		FilePath: filePath,
	}}
}

// buildImpact finds reverse dependencies for a file.
func (e *Engine) buildImpact(rels []analysis.Relation, nodes []rawNode, filePath string) []Section {
	if filePath == "" {
		return nil
	}

	// Find node IDs for this file.
	fileNodeIDs := make(map[string]bool)
	for _, n := range nodes {
		if n.FilePath == filePath {
			fileNodeIDs[n.ID] = true
		}
	}

	if len(fileNodeIDs) == 0 {
		return nil
	}

	// Find relations where target is this file's node.
	nodeFiles := make(map[string]string, len(nodes))
	for _, n := range nodes {
		nodeFiles[n.ID] = n.FilePath
	}

	dependents := make(map[string]bool)
	for _, rel := range rels {
		if fileNodeIDs[rel.TargetNodeID] {
			srcFile := nodeFiles[rel.SourceNodeID]
			if srcFile != "" && srcFile != filePath {
				dependents[srcFile] = true
			}
		}
	}

	if len(dependents) == 0 {
		return nil
	}

	var content strings.Builder
	fmt.Fprintf(&content, "%d file(s) depend on this:\n", len(dependents))
	count := 0
	for dep := range dependents {
		if count >= 10 {
			fmt.Fprintf(&content, "- ... and %d more\n", len(dependents)-10)
			break
		}
		fmt.Fprintf(&content, "- %s\n", dep)
		count++
	}

	return []Section{{
		Type:     "impact",
		Title:    "Impact Radius",
		Content:  content.String(),
		Source:   "auto",
		FilePath: filePath,
	}}
}

// buildPatterns extracts code patterns from sibling files in the same module.
func (e *Engine) buildPatterns(nodes []rawNode, filePath, module string) []Section {
	if module == "" {
		return nil
	}

	// Collect entity patterns from the module.
	entityTypes := make(map[string]int)
	for _, n := range nodes {
		if n.Module == module && n.FilePath != filePath {
			for _, ent := range n.Entities {
				entityTypes[string(ent.Kind)]++
			}
		}
	}

	if len(entityTypes) == 0 {
		return nil
	}

	var content strings.Builder
	content.WriteString("Common entity types in this module:\n")
	for t, count := range entityTypes {
		fmt.Fprintf(&content, "- %s: %d occurrences\n", t, count)
	}

	return []Section{{
		Type:    "pattern",
		Title:   "Module Patterns",
		Content: content.String(),
		Source:  "auto",
		Module:  module,
	}}
}

// scoreSection computes the final score for a section.
func (e *Engine) scoreSection(s Section, targetFile, targetModule string) float64 {
	// Recency: use a default of 0.5 for non-history items.
	recency := 0.5
	if s.Type == "history" {
		recency = 0.9 // History sections are recent by definition (we only keep recent ones).
	}

	// Severity.
	severity := 0.3
	if s.Type == "constraint" {
		if strings.Contains(s.Content, "[must]") {
			severity = 1.0
		} else if strings.Contains(s.Content, "[should]") {
			severity = 0.6
		}
	}

	// Proximity.
	proximity := ProximityScore("global")
	if s.FilePath != "" && s.FilePath == targetFile {
		proximity = ProximityScore("file")
	} else if s.Module != "" && s.Module == targetModule {
		proximity = ProximityScore("module")
	}

	// Novelty.
	novelty := 1.0
	if e.session != nil {
		novelty = e.session.NoveltyScore(sectionKey(s))
	}

	return ComputeScore(e.cfg.Weights, recency, severity, proximity, novelty)
}

// extractWarnings produces proactive warnings for must-severity constraints.
func (e *Engine) extractWarnings(nodes []rawNode, filePath, module string) []Warning {
	var warnings []Warning

	for _, n := range nodes {
		if n.Type != "rule" && n.Class != "constraint" {
			continue
		}

		if n.Severity != "must" {
			continue
		}

		// Check if this constraint applies to the target.
		applies := false
		appliesTo := "global"
		if n.FilePath != "" && n.FilePath == filePath {
			applies = true
			appliesTo = n.FilePath
		} else if n.Module != "" && n.Module == module {
			applies = true
			appliesTo = n.Module
		} else if n.FilePath == "" && n.Module == "" {
			applies = true // global rule
		}

		if !applies {
			continue
		}

		warnings = append(warnings, Warning{
			Severity:  "must",
			Rule:      n.Name,
			Source:    n.Source,
			AppliesTo: appliesTo,
		})
	}

	return warnings
}

// loadNodes reads all knowledge nodes from the database, preserving extra fields.
func (e *Engine) loadNodes(ctx context.Context) ([]rawNode, error) {
	var nodes []rawNode
	if err := e.db.View(ctx, func(rtx *tx.ReadTx) error {
		prefix := []byte("kn:")
		cursor := rtx.Cursor()
		for ok := cursor.Seek(prefix); ok; ok = cursor.Next() {
			k := cursor.Key()
			if !strings.HasPrefix(string(k), "kn:") {
				break
			}
			var node rawNode
			if err := json.Unmarshal(cursor.Value(), &node); err != nil {
				continue
			}
			if node.Status != "deleted" {
				nodes = append(nodes, node)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return nodes, nil
}

// loadEvolutionLogs reads all evolution logs from the database.
func (e *Engine) loadEvolutionLogs(ctx context.Context) ([]analysis.EvolutionLog, error) {
	var logs []analysis.EvolutionLog
	if err := e.db.View(ctx, func(rtx *tx.ReadTx) error {
		prefix := []byte("el:")
		cursor := rtx.Cursor()
		for ok := cursor.Seek(prefix); ok; ok = cursor.Next() {
			k := cursor.Key()
			if !strings.HasPrefix(string(k), "el:") {
				break
			}
			var log analysis.EvolutionLog
			if err := json.Unmarshal(cursor.Value(), &log); err != nil {
				continue
			}
			logs = append(logs, log)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return logs, nil
}

// loadRelations reads all relations from the database.
func (e *Engine) loadRelations(ctx context.Context) ([]analysis.Relation, error) {
	var rels []analysis.Relation
	if err := e.db.View(ctx, func(rtx *tx.ReadTx) error {
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

// moduleForFile finds the module a file belongs to.
func moduleForFile(nodes []rawNode, filePath string) string {
	for _, n := range nodes {
		if n.FilePath == filePath && n.Module != "" {
			return n.Module
		}
	}
	return ""
}

// filterByFocus filters sections to only include the specified type.
func filterByFocus(sections []Section, focus string) []Section {
	focusType := focus
	switch focus {
	case "constraints":
		focusType = "constraint"
	}

	var filtered []Section
	for _, s := range sections {
		if s.Type == focusType {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// sectionKey generates a unique key for novelty tracking.
func sectionKey(s Section) string {
	return fmt.Sprintf("%s:%s:%s", s.Type, s.Module, s.Title)
}

// minInt returns the smaller of a and b.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

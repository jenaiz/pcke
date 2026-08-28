package onboard

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jenaiz/pcke/internal/analysis"
	"github.com/jenaiz/pcke/internal/output"
)

// Walkthrough is the top-level result produced by [Engine.Generate].
type Walkthrough struct {
	Title       string    `json:"title"`
	Sections    []Section `json:"sections"`
	GeneratedAt time.Time `json:"generated_at"`
	RepoPath    string    `json:"repo_path"`
	NodeCount   int       `json:"node_count"`
	ModuleCount int       `json:"module_count"`
}

// Section represents a named segment of the walkthrough.
type Section struct {
	Name     string `json:"name"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	IsCustom bool   `json:"is_custom,omitempty"`
	Order    int    `json:"order"`
}

// Engine generates walkthroughs from the knowledge base.
type Engine struct {
	Nodes     []analysis.KnowledgeNode
	Relations []analysis.Relation
	EvolLogs  []analysis.EvolutionLog
	Decisions []output.DecisionInfo
	RepoPath  string
	Config    *Config
}

// Generate produces a full [Walkthrough] from the loaded data.
func (e *Engine) Generate(_ context.Context) (*Walkthrough, error) {
	modules := output.GroupByModule(e.Nodes)
	scores := ScoreModules(e.Nodes, e.Relations, e.EvolLogs)

	cfg := e.Config
	if cfg == nil {
		cfg = DefaultConfig()
	}

	title := "Project Walkthrough"
	if cfg.Walkthrough.Title != "" {
		title = cfg.Walkthrough.Title
	}

	w := &Walkthrough{
		Title:       title,
		GeneratedAt: time.Now(),
		RepoPath:    e.RepoPath,
		NodeCount:   len(e.Nodes),
		ModuleCount: len(modules),
	}

	// Standard section generators.
	type sectionGen struct {
		name  string
		title string
		fn    func() string
	}

	generators := []sectionGen{
		{"overview", "Project Overview", func() string { return e.renderOverview(modules) }},
		{"tech_stack", "Tech Stack", func() string { return e.renderTechStack() }},
		{"architecture", "Architecture", func() string { return output.RenderArchitecture(e.Nodes) }},
		{"entry_points", "Entry Points", func() string { return e.renderEntryPoints() }},
		{"key_modules", "Key Modules", func() string { return e.renderKeyModules(modules, scores, cfg) }},
		{"conventions", "Conventions", func() string { return output.RenderConventions(e.Nodes) }},
		{"constraints", "Constraints", func() string { return output.RenderConstraints(e.Nodes) }},
		{"decisions", "Open Decisions", func() string { return output.RenderDecisions(e.Decisions) }},
	}

	skipSet := make(map[string]bool, len(cfg.Walkthrough.SkipSections))
	for _, s := range cfg.Walkthrough.SkipSections {
		skipSet[s] = true
	}

	order := 0
	for _, gen := range generators {
		if skipSet[gen.name] {
			continue
		}
		content := gen.fn()
		if content == "" {
			continue
		}
		order++
		w.Sections = append(w.Sections, Section{
			Name:    gen.name,
			Title:   gen.title,
			Content: content,
			Order:   order,
		})
	}

	// Insert custom sections.
	for _, cs := range cfg.Walkthrough.CustomSections {
		order++
		sec := Section{
			Name:     cs.Name,
			Title:    cs.Name,
			Content:  cs.Content,
			IsCustom: true,
			Order:    order,
		}
		w.Sections = insertCustomSection(w.Sections, sec, cs.Position)
	}

	// Reassign order after insertion.
	for i := range w.Sections {
		w.Sections[i].Order = i + 1
	}

	return w, nil
}

// GenerateForModule generates a walkthrough scoped to a single module.
func (e *Engine) GenerateForModule(_ context.Context, module string) (*Walkthrough, error) {
	var filtered []analysis.KnowledgeNode
	for _, n := range e.Nodes {
		if n.Module == module {
			filtered = append(filtered, n)
		}
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("module %q not found in knowledge base", module)
	}

	scoped := &Engine{
		Nodes:     filtered,
		Relations: e.Relations,
		EvolLogs:  e.EvolLogs,
		Decisions: output.FilterDecisionsByModule(e.Decisions, module),
		RepoPath:  e.RepoPath,
		Config:    e.Config,
	}
	return scoped.Generate(context.Background())
}

func (e *Engine) renderOverview(modules map[string][]analysis.KnowledgeNode) string {
	langs := map[string]int{}
	for _, n := range e.Nodes {
		if n.Language != "" {
			langs[n.Language]++
		}
	}

	type langCount struct {
		lang  string
		count int
	}
	var sorted []langCount
	for l, c := range langs {
		sorted = append(sorted, langCount{l, c})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].count > sorted[j].count })

	var sb strings.Builder
	sb.WriteString("# Project Overview\n\n")
	fmt.Fprintf(&sb, "**Files:** %d | **Modules:** %d\n\n", len(e.Nodes), len(modules))
	sb.WriteString("**Languages:** ")
	parts := make([]string, 0, len(sorted))
	for _, lc := range sorted {
		parts = append(parts, fmt.Sprintf("%s (%d files)", lc.lang, lc.count))
	}
	sb.WriteString(strings.Join(parts, ", "))
	sb.WriteString("\n")
	return sb.String()
}

func (e *Engine) renderTechStack() string {
	langs := map[string]int{}
	for _, n := range e.Nodes {
		if n.Language != "" {
			langs[n.Language]++
		}
	}
	if len(langs) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("# Tech Stack\n\n")
	sb.WriteString("| Language | Files |\n|----------|-------|\n")

	type langCount struct {
		lang  string
		count int
	}
	var sorted []langCount
	for l, c := range langs {
		sorted = append(sorted, langCount{l, c})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].count > sorted[j].count })

	for _, lc := range sorted {
		fmt.Fprintf(&sb, "| %s | %d |\n", lc.lang, lc.count)
	}
	return sb.String()
}

func (e *Engine) renderEntryPoints() string {
	var entries []analysis.KnowledgeNode
	for _, n := range e.Nodes {
		if isEntryPoint(n) {
			entries = append(entries, n)
		}
	}
	if len(entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("# Entry Points\n\n")
	for _, ep := range entries {
		fmt.Fprintf(&sb, "- **%s** (`%s`) — %s\n", ep.Name, ep.FilePath, ep.Class)
	}
	return sb.String()
}

func (e *Engine) renderKeyModules(
	modules map[string][]analysis.KnowledgeNode,
	scores map[string]float64,
	cfg *Config,
) string {
	type modEntry struct {
		name  string
		score float64
		files int
	}

	var entries []modEntry
	for mod, nodes := range modules {
		entries = append(entries, modEntry{
			name:  mod,
			score: scores[mod],
			files: len(nodes),
		})
	}

	// Highlighted modules first, then sort by complexity score descending.
	highlightSet := make(map[string]bool, len(cfg.Walkthrough.HighlightModules))
	for _, h := range cfg.Walkthrough.HighlightModules {
		highlightSet[h] = true
	}

	sort.Slice(entries, func(i, j int) bool {
		hi := highlightSet[entries[i].name]
		hj := highlightSet[entries[j].name]
		if hi != hj {
			return hi
		}
		return entries[i].score > entries[j].score
	})

	var sb strings.Builder
	sb.WriteString("# Key Modules\n\n")
	sb.WriteString("| Module | Files | Complexity |\n|--------|-------|------------|\n")
	for _, me := range entries {
		fmt.Fprintf(&sb, "| %s | %d | %.2f |\n", me.name, me.files, me.score)
	}
	return sb.String()
}

// isEntryPoint detects whether a node represents a project entry point.
func isEntryPoint(n analysis.KnowledgeNode) bool {
	if n.Class == "entry_point" || n.Class == "api" {
		return true
	}
	if n.Name == "main.go" {
		return true
	}
	// Check common entry-point directories.
	for _, prefix := range []string{"cmd/", "cli/", "bin/", "api/", "routes/", "handlers/", "endpoints/", "controllers/"} {
		if strings.HasPrefix(n.FilePath, prefix) && n.Class != "" {
			return true
		}
	}
	return false
}

// insertCustomSection inserts a custom section at the position specified by
// the "after:<name>" or "before:<name>" directive.
func insertCustomSection(sections []Section, sec Section, position string) []Section {
	if position == "" {
		return append(sections, sec)
	}

	parts := strings.SplitN(position, ":", 2)
	if len(parts) != 2 {
		return append(sections, sec)
	}
	where, target := parts[0], parts[1]

	for i, s := range sections {
		if s.Name == target {
			switch where {
			case "after":
				idx := i + 1
				result := make([]Section, 0, len(sections)+1)
				result = append(result, sections[:idx]...)
				result = append(result, sec)
				result = append(result, sections[idx:]...)
				return result
			case "before":
				result := make([]Section, 0, len(sections)+1)
				result = append(result, sections[:i]...)
				result = append(result, sec)
				result = append(result, sections[i:]...)
				return result
			}
		}
	}

	return append(sections, sec)
}

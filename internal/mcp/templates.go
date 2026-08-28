package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/jenaiz/pcke/internal/analysis"
	"github.com/jenaiz/pcke/internal/output"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// templateDef describes a built-in prompt template.
type templateDef struct {
	name        string
	description string
	hasModule   bool // whether the template accepts a "module" argument
	sections    []string
}

// builtinTemplates defines the four standard prompt templates.
var builtinTemplates = []templateDef{
	{
		name:        "onboarding",
		description: "New developer orientation: architecture, conventions, key decisions, and top modules",
		hasModule:   false,
		sections:    []string{"architecture", "conventions", "decisions"},
	},
	{
		name:        "review",
		description: "Code review context: constraints, recent history, and module stability",
		hasModule:   true,
		sections:    []string{"constraints", "history"},
	},
	{
		name:        "debug",
		description: "Debugging assistance: module context, dependencies, and related constraints",
		hasModule:   true,
		sections:    []string{"architecture", "constraints"},
	},
	{
		name:        "refactor",
		description: "Refactoring guidance: architecture, module coupling, and stability scores",
		hasModule:   false,
		sections:    []string{"architecture", "conventions", "constraints"},
	},
}

// registerPrompts adds all built-in and custom prompt templates to the MCP server.
func (s *Server) registerPrompts() {
	// Register built-in templates.
	builtinNames := make(map[string]bool)
	for _, td := range builtinTemplates {
		builtinNames[td.name] = true
		s.registerOnePrompt(td)
	}

	// Load and register custom templates.
	custom := loadCustomTemplates(s.root)
	for _, td := range custom {
		if builtinNames[td.name] {
			fmt.Fprintf(os.Stderr, "mcp: custom template %q overrides built-in\n", td.name)
		}
		s.registerOnePrompt(td)
	}
}

func (s *Server) registerOnePrompt(def templateDef) {
	prompt := mcplib.NewPrompt(def.name,
		mcplib.WithPromptDescription(def.description),
	)
	if def.hasModule {
		prompt = mcplib.NewPrompt(def.name,
			mcplib.WithPromptDescription(def.description),
			mcplib.WithArgument("module",
				mcplib.ArgumentDescription("Module name to scope the context"),
			),
		)
	}
	handler := s.makePromptHandler(def)
	s.prompts = append(s.prompts, mcpserver.ServerPrompt{
		Prompt:  prompt,
		Handler: handler,
	})
	s.srv.AddPrompt(prompt, handler)
}

// makePromptHandler creates a PromptHandlerFunc for the given template definition.
func (s *Server) makePromptHandler(def templateDef) func(context.Context, mcplib.GetPromptRequest) (*mcplib.GetPromptResult, error) {
	return func(ctx context.Context, req mcplib.GetPromptRequest) (*mcplib.GetPromptResult, error) {
		nodes, err := s.loadNodes(ctx)
		if err != nil {
			return nil, fmt.Errorf("mcp: template %s: load nodes: %w", def.name, err)
		}
		decisions, err := output.LoadDecisions(ctx, s.db)
		if err != nil {
			return nil, fmt.Errorf("mcp: template %s: load decisions: %w", def.name, err)
		}

		module := req.Params.Arguments["module"]

		// If a module filter is active, narrow nodes to that module.
		filtered := nodes
		filteredDecisions := decisions
		if module != "" {
			filtered = filterNodesByModule(nodes, module)
			filteredDecisions = output.FilterDecisionsByModule(decisions, module)
		}

		var parts []string
		for _, section := range def.sections {
			rendered := renderSection(section, filtered, filteredDecisions)
			if rendered != "" {
				parts = append(parts, rendered)
			}
		}

		if len(parts) == 0 {
			parts = append(parts, fmt.Sprintf("No context available for template %q.", def.name))
		}

		text := strings.Join(parts, "\n\n---\n\n")

		messages := []mcplib.PromptMessage{
			mcplib.NewPromptMessage(
				mcplib.RoleUser,
				mcplib.NewTextContent(text),
			),
		}

		return mcplib.NewGetPromptResult(
			fmt.Sprintf("Context for %s workflow", def.name),
			messages,
		), nil
	}
}

// renderSection renders a named context section using the output renderers.
func renderSection(section string, nodes []analysis.KnowledgeNode, decisions []output.DecisionInfo) string {
	switch section {
	case "architecture":
		return output.RenderArchitecture(nodes)
	case "conventions":
		return output.RenderConventions(nodes)
	case "constraints":
		return output.RenderConstraints(nodes)
	case "decisions":
		return output.RenderDecisions(decisions)
	case "history":
		return output.RenderHistory(nodes)
	default:
		return ""
	}
}

// filterNodesByModule returns only nodes belonging to the given module.
func filterNodesByModule(nodes []analysis.KnowledgeNode, module string) []analysis.KnowledgeNode {
	var filtered []analysis.KnowledgeNode
	for _, n := range nodes {
		if n.Module == module {
			filtered = append(filtered, n)
		}
	}
	return filtered
}

// customTemplateDef represents a user-defined template loaded from TOML.
type customTemplateDef struct {
	Name        string   `toml:"name"`
	Description string   `toml:"description"`
	HasModule   bool     `toml:"has_module"`
	Sections    []string `toml:"sections"`
}

// loadCustomTemplates reads .pcke/templates/*.toml and returns template defs.
func loadCustomTemplates(root string) []templateDef {
	if root == "" {
		return nil
	}
	dir := filepath.Join(root, ".pcke", "templates")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // No custom templates directory.
	}

	var templates []templateDef
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path) //nolint:gosec // G304: path is constructed from known root.
		if err != nil {
			fmt.Fprintf(os.Stderr, "mcp: skip template %s: %v\n", entry.Name(), err)
			continue
		}
		var ct customTemplateDef
		if err := toml.Unmarshal(data, &ct); err != nil {
			fmt.Fprintf(os.Stderr, "mcp: skip template %s: parse error: %v\n", entry.Name(), err)
			continue
		}
		if ct.Name == "" {
			continue
		}
		validSections := filterValidSections(ct.Sections)
		if len(validSections) == 0 {
			fmt.Fprintf(os.Stderr, "mcp: skip template %s: no valid sections\n", entry.Name())
			continue
		}
		templates = append(templates, templateDef{
			name:        ct.Name,
			description: ct.Description,
			hasModule:   ct.HasModule,
			sections:    validSections,
		})
	}
	return templates
}

// filterValidSections returns only sections that have a renderer.
func filterValidSections(sections []string) []string {
	valid := map[string]bool{
		"architecture": true,
		"conventions":  true,
		"constraints":  true,
		"decisions":    true,
		"history":      true,
	}
	var out []string
	for _, s := range sections {
		if valid[s] {
			out = append(out, s)
		}
	}
	return out
}

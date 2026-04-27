package mcp

import (
	"context"
	"fmt"
	"strings"

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

// registerPrompts adds all built-in prompt templates to the MCP server.
func (s *Server) registerPrompts() {
	for _, td := range builtinTemplates {
		def := td // capture for closure
		prompt := mcplib.NewPrompt(def.name,
			mcplib.WithPromptDescription(def.description),
		)
		if def.hasModule {
			prompt = mcplib.NewPrompt(def.name,
				mcplib.WithPromptDescription(def.description),
				mcplib.WithArgument("module",
					mcplib.ArgumentDescription("Module name to scope the context (e.g. 'internal/kdb')"),
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
}

// makePromptHandler creates a PromptHandlerFunc for the given template definition.
func (s *Server) makePromptHandler(def templateDef) func(context.Context, mcplib.GetPromptRequest) (*mcplib.GetPromptResult, error) {
	return func(ctx context.Context, req mcplib.GetPromptRequest) (*mcplib.GetPromptResult, error) {
		nodes, err := s.loadNodes(ctx)
		if err != nil {
			return nil, fmt.Errorf("mcp: template %s: load nodes: %w", def.name, err)
		}

		module := req.Params.Arguments["module"]

		// If a module filter is active, narrow nodes to that module.
		filtered := nodes
		if module != "" {
			filtered = filterNodesByModule(nodes, module)
		}

		var parts []string
		for _, section := range def.sections {
			rendered := renderSection(section, filtered)
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
func renderSection(section string, nodes []analysis.KnowledgeNode) string {
	switch section {
	case "architecture":
		return output.RenderArchitecture(nodes)
	case "conventions":
		return output.RenderConventions(nodes)
	case "constraints":
		return output.RenderConstraints(nodes)
	case "decisions":
		return output.RenderDecisions(nodes)
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

package mcp

import (
	"context"
	"fmt"

	"github.com/jenaiz/pcke/internal/output"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// registerResources adds MCP resources to the server.
// Resources expose synthesised Markdown snapshots matching .context/ output.
func (s *Server) registerResources() {
	s.resources = []mcpserver.ServerResource{
		{
			Resource: mcplib.NewResource("pcke://architecture",
				"Architecture",
				mcplib.WithMIMEType("text/markdown"),
				mcplib.WithResourceDescription("Full architecture map: modules, tech stack, file classification"),
			),
			Handler: s.handleArchitecture,
		},
		{
			Resource: mcplib.NewResource("pcke://constraints",
				"Constraints",
				mcplib.WithMIMEType("text/markdown"),
				mcplib.WithResourceDescription("Engineering rules and constraints inferred from the project"),
			),
			Handler: s.handleConstraintsResource,
		},
		{
			Resource: mcplib.NewResource("pcke://decisions",
				"Decisions",
				mcplib.WithMIMEType("text/markdown"),
				mcplib.WithResourceDescription("Developer decisions and documentation"),
			),
			Handler: s.handleDecisionsResource,
		},
	}

	s.srv.AddResources(s.resources...)
}

func (s *Server) handleArchitecture(
	ctx context.Context,
	_ mcplib.ReadResourceRequest,
) ([]mcplib.ResourceContents, error) {
	nodes, err := s.loadNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp: load nodes: %w", err)
	}
	return []mcplib.ResourceContents{
		mcplib.TextResourceContents{
			URI:      "pcke://architecture",
			MIMEType: "text/markdown",
			Text:     output.RenderArchitecture(nodes),
		},
	}, nil
}

func (s *Server) handleConstraintsResource(
	ctx context.Context,
	_ mcplib.ReadResourceRequest,
) ([]mcplib.ResourceContents, error) {
	nodes, err := s.loadNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp: load nodes: %w", err)
	}
	return []mcplib.ResourceContents{
		mcplib.TextResourceContents{
			URI:      "pcke://constraints",
			MIMEType: "text/markdown",
			Text:     output.RenderConstraints(nodes),
		},
	}, nil
}

func (s *Server) handleDecisionsResource(
	ctx context.Context,
	_ mcplib.ReadResourceRequest,
) ([]mcplib.ResourceContents, error) {
	decisions, err := output.LoadDecisions(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("mcp: load decisions: %w", err)
	}
	return []mcplib.ResourceContents{
		mcplib.TextResourceContents{
			URI:      "pcke://decisions",
			MIMEType: "text/markdown",
			Text:     output.RenderDecisions(decisions),
		},
	}, nil
}

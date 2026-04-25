// Package mcp implements the MCP (Model Context Protocol) server for pcke.
//
// The server runs on stdio transport and exposes read-only tools and
// resources backed by the kdb knowledge database. See PRD §5.6.2.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jenaiz/pcke/internal/analysis"
	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
	"github.com/jenaiz/pcke/internal/output"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Server wraps the MCP protocol server with access to the kdb database.
type Server struct {
	db        *kdb.DB
	srv       *mcpserver.MCPServer
	resources []mcpserver.ServerResource
}

// New creates a [Server] backed by the given kdb database.
// All tools and resources are read-only.
func New(db *kdb.DB) *Server {
	s := &Server{db: db}

	s.srv = mcpserver.NewMCPServer(
		"pcke",
		"0.2.0",
		mcpserver.WithToolCapabilities(false),
		mcpserver.WithResourceCapabilities(true, false),
		mcpserver.WithRecovery(),
	)

	s.registerTools()
	s.registerResources()

	return s
}

// Serve starts the MCP server on stdio. Blocks until the client
// disconnects or a signal is received.
func (s *Server) Serve() error {
	return mcpserver.ServeStdio(s.srv)
}

// MCPServer returns the underlying MCPServer for testing purposes.
func (s *Server) MCPServer() *mcpserver.MCPServer {
	return s.srv
}

// ServerResources returns the registered MCP resources for testing.
func (s *Server) ServerResources() []mcpserver.ServerResource {
	return s.resources
}

// loadNodes reads all active knowledge nodes from the database.
func (s *Server) loadNodes(ctx context.Context) ([]analysis.KnowledgeNode, error) {
	return output.LoadNodes(ctx, s.db)
}

// loadRelations reads all import relations from the database.
func (s *Server) loadRelations(ctx context.Context) ([]analysis.Relation, error) {
	var rels []analysis.Relation
	if err := s.db.View(ctx, func(rtx *tx.ReadTx) error {
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

// loadEvolutionLogs reads all evolution logs from the database.
func (s *Server) loadEvolutionLogs(ctx context.Context) ([]analysis.EvolutionLog, error) {
	var logs []analysis.EvolutionLog
	if err := s.db.View(ctx, func(rtx *tx.ReadTx) error {
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

// nodeToJSON marshals a node to indented JSON for tool responses.
func nodeToJSON(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("{\"error\": %q}", err.Error())
	}
	return string(data)
}

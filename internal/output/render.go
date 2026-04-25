// Package output generates Markdown context files from the knowledge base.
//
// It reads knowledge nodes persisted by the analysis scanner and renders
// structured Markdown documents into the .context/ directory and agent
// instruction files (.github/copilot-instructions.md, .claude/CLAUDE.md).
//
// Concurrency: a single [Renderer] instance should be used per sync
// invocation. The underlying [kdb.DB] manages its own locking.
//
// See PRD §5.6.1 for the output structure specification.
package output

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jenaiz/pcke/internal/analysis"
	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// Renderer reads the knowledge base and writes Markdown output files.
type Renderer struct {
	db   *kdb.DB
	root string // Repository root directory.
}

// NewRenderer creates a [Renderer] for the repository at root.
func NewRenderer(root string, db *kdb.DB) *Renderer {
	return &Renderer{db: db, root: root}
}

// SyncResult summarises a completed sync operation.
type SyncResult struct {
	FilesWritten int
}

// Sync reads the knowledge base and generates all output files.
func (r *Renderer) Sync(ctx context.Context) (*SyncResult, error) {
	nodes, err := LoadNodes(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("output: load nodes: %w", err)
	}

	result := &SyncResult{}

	generators := []struct {
		path string
		fn   func([]analysis.KnowledgeNode) string
	}{
		{".context/ARCHITECTURE.md", RenderArchitecture},
		{".context/CONVENTIONS.md", RenderConventions},
		{".context/HISTORY.md", RenderHistory},
		{".context/DECISIONS.md", RenderDecisions},
		{".context/CONSTRAINTS.md", RenderConstraints},
	}

	for _, g := range generators {
		content := g.fn(nodes)
		if err := r.writeFile(g.path, content); err != nil {
			return nil, fmt.Errorf("output: write %s: %w", g.path, err)
		}
		result.FilesWritten++
	}

	// Per-module pages.
	modules := groupByModule(nodes)
	for mod, modNodes := range modules {
		safeName := strings.ReplaceAll(mod, "/", "_")
		path := fmt.Sprintf(".context/MODULES/%s.md", safeName)
		content := renderModulePage(mod, modNodes)
		if err := r.writeFile(path, content); err != nil {
			return nil, fmt.Errorf("output: write %s: %w", path, err)
		}
		result.FilesWritten++
	}

	// Agent instruction files.
	agentFiles := []struct {
		path    string
		content string
	}{
		{".github/copilot-instructions.md", renderCopilotInstructions(nodes, modules)},
		{".claude/CLAUDE.md", renderClaudeInstructions(nodes, modules)},
	}
	for _, af := range agentFiles {
		if err := r.writeFile(af.path, af.content); err != nil {
			return nil, fmt.Errorf("output: write %s: %w", af.path, err)
		}
		result.FilesWritten++
	}

	return result, nil
}

// LoadNodes reads all knowledge nodes from the database using cursor
// iteration over the "kn:" prefix.
func LoadNodes(ctx context.Context, db *kdb.DB) ([]analysis.KnowledgeNode, error) {
	var nodes []analysis.KnowledgeNode
	if err := db.View(ctx, func(rtx *tx.ReadTx) error {
		prefix := []byte("kn:")
		cursor := rtx.Cursor()
		for ok := cursor.Seek(prefix); ok; ok = cursor.Next() {
			k := cursor.Key()
			if !strings.HasPrefix(string(k), "kn:") {
				break
			}
			var node analysis.KnowledgeNode
			if err := json.Unmarshal(cursor.Value(), &node); err != nil {
				continue // Skip corrupt entries.
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

// writeFile creates the file (and parent directories) at relPath under
// the repository root.
func (r *Renderer) writeFile(relPath, content string) error {
	fullPath := filepath.Join(r.root, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o750); err != nil {
		return err
	}
	return os.WriteFile(fullPath, []byte(content), 0o600)
}

// groupByModule organises nodes by their module field.
func groupByModule(nodes []analysis.KnowledgeNode) map[string][]analysis.KnowledgeNode {
	m := map[string][]analysis.KnowledgeNode{}
	for _, n := range nodes {
		if n.Module != "" {
			m[n.Module] = append(m[n.Module], n)
		}
	}
	return m
}

// sortedModuleNames returns module names sorted alphabetically.
func sortedModuleNames(modules map[string][]analysis.KnowledgeNode) []string {
	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

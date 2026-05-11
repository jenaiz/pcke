package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/analysis"
	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/tx"
	pckmcp "github.com/jenaiz/pcke/internal/mcp"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// seedDB creates a kdb database with sample knowledge nodes and relations.
func seedDB(t *testing.T) *kdb.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}

	nodes := []analysis.KnowledgeNode{
		{
			ID:       "cmd/pcke/main.go",
			Type:     "file",
			Name:     "main.go",
			FilePath: "cmd/pcke/main.go",
			Language: "Go",
			Module:   "cmd/pcke",
			Class:    "source",
			Status:   "active",
		},
		{
			ID:       "internal/kdb/db.go",
			Type:     "file",
			Name:     "db.go",
			FilePath: "internal/kdb/db.go",
			Language: "Go",
			Module:   "internal/kdb",
			Class:    "source",
			Status:   "active",
		},
		{
			ID:       "README.md",
			Type:     "file",
			Name:     "README.md",
			FilePath: "README.md",
			Language: "Markdown",
			Module:   "",
			Class:    "doc",
			Status:   "active",
		},
		{
			ID:       "Makefile",
			Type:     "file",
			Name:     "Makefile",
			FilePath: "Makefile",
			Language: "",
			Module:   "",
			Class:    "infra",
			Status:   "active",
		},
	}

	rels := []analysis.Relation{
		{
			ID:           "cmd/pcke/main.go→fmt",
			SourceNodeID: "cmd/pcke/main.go",
			TargetNodeID: "fmt",
			Type:         "imports",
			Source:       "auto",
		},
		{
			ID:           "cmd/pcke/main.go→os",
			SourceNodeID: "cmd/pcke/main.go",
			TargetNodeID: "os",
			Type:         "imports",
			Source:       "auto",
		},
	}

	ctx := context.Background()
	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		for _, n := range nodes {
			data, err := json.Marshal(n)
			if err != nil {
				return err
			}
			if err := wtx.Put([]byte("kn:"+n.ID), data); err != nil {
				return err
			}
		}
		for _, r := range rels {
			data, err := json.Marshal(r)
			if err != nil {
				return err
			}
			if err := wtx.Put([]byte("rel:"+r.ID), data); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	return db
}

// startTestServer creates an mcptest Server with tools and resources
// from pckmcp, starts it, and returns the server (caller must Close).
func startTestServer(t *testing.T, db *kdb.DB) *mcptest.Server {
	t.Helper()

	srv := pckmcp.New(db, t.TempDir())

	// Extract registered tools and resources from the pcke MCPServer.
	ts := mcptest.NewUnstartedServer(t)

	// Copy tools.
	toolMap := srv.MCPServer().ListTools()
	for _, st := range toolMap {
		ts.AddTool(st.Tool, st.Handler)
	}

	// Copy resources.
	ts.AddServerOptions(
		mcpserver.WithResourceCapabilities(true, false),
		mcpserver.WithPromptCapabilities(false),
	)
	for _, sr := range srv.ServerResources() {
		ts.AddResource(sr.Resource, sr.Handler)
	}

	// Copy prompts.
	for _, sp := range srv.ServerPrompts() {
		ts.AddPrompt(sp.Prompt, sp.Handler)
	}

	ctx := context.Background()
	if err := ts.Start(ctx); err != nil {
		t.Fatalf("start test server: %v", err)
	}
	t.Cleanup(ts.Close)

	return ts
}

func callTool(t *testing.T, ts *mcptest.Server, name string, args map[string]any) string {
	t.Helper()
	ctx := context.Background()

	result, err := ts.Client().CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return extractText(t, result)
}

func readResource(t *testing.T, ts *mcptest.Server, uri string) string {
	t.Helper()
	ctx := context.Background()

	result, err := ts.Client().ReadResource(ctx, mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: uri},
	})
	if err != nil {
		t.Fatalf("ReadResource %s: %v", uri, err)
	}
	if len(result.Contents) == 0 {
		t.Fatalf("no contents for resource %s", uri)
	}
	text, ok := mcp.AsTextResourceContents(result.Contents[0])
	if !ok {
		t.Fatalf("expected TextResourceContents for %s", uri)
	}
	return text.Text
}

// --- Tool tests ---

func TestRecallTool(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	text := callTool(t, ts, "recall", map[string]any{"query": "kdb database"})

	if !strings.Contains(text, "db.go") {
		t.Errorf("recall for 'kdb database' should find db.go, got:\n%s", text)
	}
}

func TestRecallNoResults(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	text := callTool(t, ts, "recall", map[string]any{"query": "nonexistent_xyzzy"})

	if !strings.Contains(text, "No results") {
		t.Errorf("expected 'No results', got: %s", text)
	}
}

func TestGetModuleContext(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	text := callTool(t, ts, "get_module_context", map[string]any{"module": "cmd/pcke"})

	if !strings.Contains(text, "cmd/pcke") {
		t.Errorf("expected module name, got:\n%s", text)
	}
	if !strings.Contains(text, "fmt") {
		t.Errorf("expected dependency 'fmt', got:\n%s", text)
	}
}

func TestGetModuleContextNotFound(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	text := callTool(t, ts, "get_module_context", map[string]any{"module": "nonexistent"})

	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found', got: %s", text)
	}
}

func TestGetConstraints(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	text := callTool(t, ts, "get_constraints", map[string]any{})

	if !strings.Contains(text, "go build") {
		t.Errorf("expected Go build constraint, got:\n%s", text)
	}
	if !strings.Contains(text, "Infrastructure") {
		t.Errorf("expected infra constraint, got:\n%s", text)
	}
}

func TestGetHistory(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	el := analysis.EvolutionLog{
		ID:         "main.go:abc123",
		NodeID:     "cmd/pcke/main.go",
		CommitHash: "abc123",
		ChangeType: "modified",
		Author:     "test@test.com",
	}
	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		data, err := json.Marshal(el)
		if err != nil {
			return err
		}
		return wtx.Put([]byte("el:"+el.ID), data)
	}); err != nil {
		t.Fatalf("seed log: %v", err)
	}

	ts := startTestServer(t, db)
	text := callTool(t, ts, "get_history", map[string]any{"file_path": "cmd/pcke/main.go"})

	if !strings.Contains(text, "abc123") {
		t.Errorf("expected commit hash, got:\n%s", text)
	}
	if !strings.Contains(text, "modified") {
		t.Errorf("expected change_type, got:\n%s", text)
	}
}

func TestGetHistoryNoLogs(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	text := callTool(t, ts, "get_history", map[string]any{"file_path": "nonexistent.go"})

	if !strings.Contains(text, "No history") {
		t.Errorf("expected 'No history', got: %s", text)
	}
}

// --- Resource tests ---

func TestArchitectureResource(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	text := readResource(t, ts, "pcke://architecture")

	if !strings.Contains(text, "# Architecture") {
		t.Errorf("expected architecture header, got:\n%s", truncate(text, 200))
	}
	if !strings.Contains(text, "Go") {
		t.Errorf("expected Go in tech stack, got:\n%s", truncate(text, 200))
	}
}

func TestConstraintsResource(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	text := readResource(t, ts, "pcke://constraints")

	if !strings.Contains(text, "# Constraints") {
		t.Errorf("expected constraints header, got:\n%s", truncate(text, 200))
	}
}

func TestDecisionsResource(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	text := readResource(t, ts, "pcke://decisions")

	if !strings.Contains(text, "# Decisions") {
		t.Errorf("expected decisions header, got:\n%s", truncate(text, 200))
	}
	if !strings.Contains(text, "README.md") {
		t.Errorf("expected README.md in decisions, got:\n%s", text)
	}
}

func TestListResources(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	ctx := context.Background()

	result, err := ts.Client().ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}

	if len(result.Resources) != 3 {
		t.Errorf("expected 3 resources, got %d", len(result.Resources))
	}

	uris := map[string]bool{}
	for _, r := range result.Resources {
		uris[r.URI] = true
	}
	for _, expected := range []string{"pcke://architecture", "pcke://constraints", "pcke://decisions"} {
		if !uris[expected] {
			t.Errorf("missing resource %s", expected)
		}
	}
}

// --- Helpers ---

func extractText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result == nil || len(result.Content) == 0 {
		t.Fatal("empty result")
	}
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatal("no text content in result")
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// --- get_context_for_file tests ---
//
// These tests seed the typed-event log directly via event.Store rather than
// using the legacy seedDB helper, because the retrieval engine reads from
// e:/d:/l: records, not kn:/rel: collections.

// seedTypedEventDB returns a DB pre-loaded with the typed-event topology
// the retrieval engine consumes:
//
//	internal/kdb/db.go        ←focus file
//	  imports → internal/kdb/btree.go
//	  reverse: cmd/pcke/main.go imports it
//	  decision_link → adr-0008 (global must)
func seedTypedEventDB(t *testing.T) *kdb.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	for range 5 {
		if err := db.Grow(); err != nil {
			t.Fatalf("db.Grow: %v", err)
		}
	}

	store := event.New(db)
	ctx := context.Background()
	now := time.Now().UTC()

	entities := []*event.Entity{
		{Hdr: event.Header{CreatedAt: now}, EID: "internal/kdb/db.go", Path: "internal/kdb/db.go", Type: "file"},
		{Hdr: event.Header{CreatedAt: now}, EID: "internal/kdb/btree.go", Path: "internal/kdb/btree.go", Type: "file"},
		{Hdr: event.Header{CreatedAt: now}, EID: "cmd/pcke/main.go", Path: "cmd/pcke/main.go", Type: "file"},
	}
	for _, e := range entities {
		if _, err := store.Append(ctx, e); err != nil {
			t.Fatalf("append entity %s: %v", e.EID, err)
		}
	}

	if _, err := store.Append(ctx, &event.Decision{
		Hdr:      event.Header{CreatedAt: now},
		DID:      "adr-0008",
		Title:    "Context Graph Pivot",
		Body:     "Pivot the data model to a typed-event graph.",
		Severity: event.SeverityMust,
		Scope:    event.ScopeGlobal,
		Source:   "adr",
	}); err != nil {
		t.Fatalf("append decision: %v", err)
	}

	links := []*event.Link{
		{Hdr: event.Header{CreatedAt: now}, SrcRef: "e:internal/kdb/db.go", EdgeType: "imports", DstRef: "e:internal/kdb/btree.go"},
		{Hdr: event.Header{CreatedAt: now}, SrcRef: "e:cmd/pcke/main.go", EdgeType: "imports", DstRef: "e:internal/kdb/db.go"},
		{Hdr: event.Header{CreatedAt: now}, SrcRef: "e:internal/kdb/db.go", EdgeType: "decision_link", DstRef: "d:adr-0008"},
	}
	for _, l := range links {
		if _, err := store.AppendLink(ctx, l); err != nil {
			t.Fatalf("append link: %v", err)
		}
	}

	return db
}

// parseStreamItems splits the streamed tool output into JSON items.
// The plain-mode StreamWriter joins items with "\n---\n"; this helper
// reverses the join and unmarshals each segment.
func parseStreamItems(t *testing.T, text string) []map[string]any {
	t.Helper()
	if text == "" {
		return nil
	}
	parts := strings.Split(text, "\n---\n")
	out := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(p), &m); err != nil {
			t.Fatalf("unmarshal stream item %q: %v", p, err)
		}
		out = append(out, m)
	}
	return out
}

func TestGetContextForFile_ReturnsFocusAndNeighbors(t *testing.T) {
	db := seedTypedEventDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	text := callTool(t, ts, "get_context_for_file", map[string]any{
		"file_path": "internal/kdb/db.go",
	})

	items := parseStreamItems(t, text)
	if len(items) < 2 {
		t.Fatalf("expected ≥2 items (sections + summary), got %d: %s", len(items), text)
	}

	refs := map[string]bool{}
	var summary map[string]any
	for _, it := range items {
		if v, ok := it["_summary"].(bool); ok && v {
			summary = it
			continue
		}
		if ref, ok := it["ref"].(string); ok {
			refs[ref] = true
		}
	}

	for _, want := range []string{
		"e:internal/kdb/db.go",
		"e:internal/kdb/btree.go",
		"e:cmd/pcke/main.go",
		"d:adr-0008",
	} {
		if !refs[want] {
			t.Errorf("missing ref %q in stream; got %v", want, refs)
		}
	}

	if summary == nil {
		t.Fatal("missing _summary item")
	}
	if got, _ := summary["budget_limit"].(float64); got == 0 {
		t.Errorf("budget_limit should default to engine default, got 0")
	}
	if got, _ := summary["section_count"].(float64); int(got) != len(refs) {
		t.Errorf("section_count = %v, want %d", got, len(refs))
	}
}

func TestGetContextForFile_MissingFilePath(t *testing.T) {
	db := seedTypedEventDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	text := callTool(t, ts, "get_context_for_file", map[string]any{})
	if !strings.Contains(text, "file_path") {
		t.Errorf("expected error mentioning file_path, got: %s", text)
	}
}

func TestGetContextForFile_UnscannedFileReturnsSummaryOnly(t *testing.T) {
	db := seedTypedEventDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	text := callTool(t, ts, "get_context_for_file", map[string]any{
		"file_path": "does/not/exist.go",
	})

	var summary map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &summary); err != nil {
		t.Fatalf("unmarshal summary-only response %q: %v", text, err)
	}
	if got, _ := summary["_summary"].(bool); !got {
		t.Errorf("expected _summary=true, got: %v", summary)
	}
	if got, _ := summary["section_count"].(float64); int(got) != 0 {
		t.Errorf("section_count = %v, want 0 for unscanned file", got)
	}
	warns, _ := summary["warnings"].([]any)
	if len(warns) == 0 {
		t.Errorf("expected at least one warning for unscanned file, got: %v", summary)
	}
}

func TestGetContextForFile_BudgetTruncates(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	for range 5 {
		if err := db.Grow(); err != nil {
			t.Fatalf("db.Grow: %v", err)
		}
	}

	store := event.New(db)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := store.Append(ctx, &event.Entity{
		Hdr: event.Header{CreatedAt: now}, EID: "f.go", Path: "f.go", Type: "file",
	}); err != nil {
		t.Fatalf("append entity: %v", err)
	}
	bigBody := strings.Repeat("word ", 1000) // ~1300 tokens each
	for _, id := range []string{"a", "b"} {
		if _, err := store.Append(ctx, &event.Decision{
			Hdr:      event.Header{CreatedAt: now},
			DID:      id,
			Title:    id,
			Body:     bigBody,
			Severity: event.SeverityMust,
			Scope:    event.ScopeGlobal,
			Source:   "adr",
		}); err != nil {
			t.Fatalf("append decision %s: %v", id, err)
		}
		if _, err := store.AppendLink(ctx, &event.Link{
			Hdr: event.Header{CreatedAt: now}, SrcRef: "e:f.go", EdgeType: "decision_link", DstRef: "d:" + id,
		}); err != nil {
			t.Fatalf("append link: %v", err)
		}
	}

	ts := startTestServer(t, db)
	text := callTool(t, ts, "get_context_for_file", map[string]any{
		"file_path": "f.go",
		"budget":    float64(2000),
	})

	items := parseStreamItems(t, text)
	var summary map[string]any
	for _, it := range items {
		if v, ok := it["_summary"].(bool); ok && v {
			summary = it
		}
	}
	if summary == nil {
		t.Fatalf("missing _summary in %s", text)
	}
	if truncated, _ := summary["truncated"].(bool); !truncated {
		t.Errorf("expected truncated=true with two 1300-token decisions in 2000-token budget, got: %v", summary)
	}
	if limit, _ := summary["budget_limit"].(float64); int(limit) != 2000 {
		t.Errorf("budget_limit = %v, want 2000", limit)
	}
	if used, _ := summary["tokens_used"].(float64); int(used) > 2000 {
		t.Errorf("tokens_used = %v exceeds budget 2000", used)
	}
}

func TestMain(m *testing.M) {
	_ = filepath.Join(os.TempDir(), "pcke-mcp-test")
	os.Exit(m.Run())
}

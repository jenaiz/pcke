package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
	pckmcp "github.com/jenaiz/pcke/internal/mcp"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// seedE2EDB creates a kdb with 5+ knowledge nodes across 2 modules.
func seedE2EDB(t *testing.T) *kdb.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}

	nodes := []map[string]any{
		{
			"id": "internal/kdb/db.go", "name": "db.go", "type": "file",
			"file_path": "internal/kdb/db.go", "module": "internal/kdb",
			"language": "Go", "class": "source", "status": "active",
		},
		{
			"id": "internal/kdb/tx.go", "name": "tx.go", "type": "file",
			"file_path": "internal/kdb/tx.go", "module": "internal/kdb",
			"language": "Go", "class": "source", "status": "active",
		},
		{
			"id": "internal/kdb/wal.go", "name": "wal.go", "type": "file",
			"file_path": "internal/kdb/wal.go", "module": "internal/kdb",
			"language": "Go", "class": "source", "status": "active",
		},
		{
			"id": "cmd/pcke/main.go", "name": "main.go", "type": "file",
			"file_path": "cmd/pcke/main.go", "module": "cmd/pcke",
			"language": "Go", "class": "source", "status": "active",
		},
		{
			"id": "cmd/pcke/commands.go", "name": "commands.go", "type": "file",
			"file_path": "cmd/pcke/commands.go", "module": "cmd/pcke",
			"language": "Go", "class": "source", "status": "active",
		},
	}

	rels := []map[string]any{
		{
			"id": "cmd/pcke/main.go→fmt", "source_node_id": "cmd/pcke/main.go",
			"target_node_id": "fmt", "type": "imports", "source": "auto",
		},
		{
			"id": "internal/kdb/db.go→os", "source_node_id": "internal/kdb/db.go",
			"target_node_id": "os", "type": "imports", "source": "auto",
		},
	}

	ctx := context.Background()
	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		for _, n := range nodes {
			data, err := json.Marshal(n)
			if err != nil {
				return err
			}
			if err := wtx.Put([]byte("kn:"+n["id"].(string)), data); err != nil {
				return err
			}
		}
		for _, r := range rels {
			data, err := json.Marshal(r)
			if err != nil {
				return err
			}
			if err := wtx.Put([]byte("rel:"+r["id"].(string)), data); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	return db
}

// startE2EServer creates and starts a full MCP test server with all tools/resources/prompts.
func startE2EServer(t *testing.T, db *kdb.DB) *mcptest.Server {
	t.Helper()
	srv := pckmcp.New(db, t.TempDir())

	ts := mcptest.NewUnstartedServer(t)

	toolMap := srv.MCPServer().ListTools()
	for _, st := range toolMap {
		ts.AddTool(st.Tool, st.Handler)
	}

	ts.AddServerOptions(
		mcpserver.WithResourceCapabilities(true, false),
		mcpserver.WithPromptCapabilities(false),
	)
	for _, sr := range srv.ServerResources() {
		ts.AddResource(sr.Resource, sr.Handler)
	}
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

// TestE2E_MCP_FullCycle validates the complete MCP lifecycle:
// connect → call tool → read resource → get prompt → disconnect.
// This test must pass under -race.
func TestE2E_MCP_FullCycle(t *testing.T) {
	db := seedE2EDB(t)
	defer func() { _ = db.Close() }()

	ts := startE2EServer(t, db)
	client := ts.Client()

	t.Run("call_recall_tool", func(t *testing.T) {
		e2eTestRecallTool(t, client)
	})
	t.Run("call_get_module_context", func(t *testing.T) {
		e2eTestModuleContext(t, client)
	})
	t.Run("list_resources", func(t *testing.T) {
		e2eTestListResources(t, client)
	})
	t.Run("read_resource", func(t *testing.T) {
		e2eTestReadResource(t, client)
	})
	t.Run("list_prompts", func(t *testing.T) {
		e2eTestListPrompts(t, client)
	})
	t.Run("get_prompt", func(t *testing.T) {
		e2eTestGetPrompt(t, client)
	})
	t.Run("recall_with_results", func(t *testing.T) {
		e2eTestRecallBroad(t, client)
	})
	t.Run("call_get_constraints", func(t *testing.T) {
		e2eTestGetConstraints(t, client)
	})
}

func e2eTestRecallTool(t *testing.T, client *mcpclient.Client) {
	t.Helper()
	ctx := context.Background()
	result, err := client.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "recall",
			Arguments: map[string]any{"query": "kdb database"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool recall: %v", err)
	}
	text := e2eExtractText(t, result)
	if !strings.Contains(text, "db.go") {
		t.Errorf("recall should find db.go, got:\n%s", text)
	}
}

func e2eTestModuleContext(t *testing.T, client *mcpclient.Client) {
	t.Helper()
	ctx := context.Background()
	result, err := client.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "get_module_context",
			Arguments: map[string]any{"module": "internal/kdb"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool get_module_context: %v", err)
	}
	text := e2eExtractText(t, result)
	if !strings.Contains(text, "internal/kdb") {
		t.Errorf("expected module name, got:\n%s", text)
	}
}

func e2eTestListResources(t *testing.T, client *mcpclient.Client) {
	t.Helper()
	ctx := context.Background()
	result, err := client.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(result.Resources) < 3 {
		t.Errorf("expected at least 3 resources, got %d", len(result.Resources))
	}
	uris := make(map[string]bool)
	for _, r := range result.Resources {
		uris[r.URI] = true
	}
	if !uris["pcke://architecture"] {
		t.Error("missing pcke://architecture resource")
	}
}

func e2eTestReadResource(t *testing.T, client *mcpclient.Client) {
	t.Helper()
	ctx := context.Background()
	result, err := client.ReadResource(ctx, mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: "pcke://architecture"},
	})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("empty contents for pcke://architecture")
	}
	text, ok := mcp.AsTextResourceContents(result.Contents[0])
	if !ok {
		t.Fatal("expected TextResourceContents")
	}
	if text.Text == "" {
		t.Error("architecture resource returned empty text")
	}
}

func e2eTestListPrompts(t *testing.T, client *mcpclient.Client) {
	t.Helper()
	ctx := context.Background()
	result, err := client.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	names := make(map[string]bool)
	for _, p := range result.Prompts {
		names[p.Name] = true
	}
	for _, expected := range []string{"onboarding", "review", "debug", "refactor"} {
		if !names[expected] {
			t.Errorf("missing prompt %q", expected)
		}
	}
}

func e2eTestGetPrompt(t *testing.T, client *mcpclient.Client) {
	t.Helper()
	ctx := context.Background()
	result, err := client.GetPrompt(ctx, mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{Name: "onboarding"},
	})
	if err != nil {
		t.Fatalf("GetPrompt onboarding: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Error("expected non-empty messages from onboarding prompt")
	}
}

func e2eTestRecallBroad(t *testing.T, client *mcpclient.Client) {
	t.Helper()
	ctx := context.Background()
	result, err := client.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "recall",
			Arguments: map[string]any{"query": "Go file source"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool recall: %v", err)
	}
	text := e2eExtractText(t, result)
	if !strings.Contains(text, "result") && !strings.Contains(text, "Go") {
		t.Errorf("expected results for broad query, got:\n%s", text)
	}
}

func e2eTestGetConstraints(t *testing.T, client *mcpclient.Client) {
	t.Helper()
	ctx := context.Background()
	result, err := client.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "get_constraints",
			Arguments: map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("CallTool get_constraints: %v", err)
	}
	text := e2eExtractText(t, result)
	if text == "" {
		t.Error("expected non-empty constraints response")
	}
}

func e2eExtractText(t *testing.T, result *mcp.CallToolResult) string {
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

// TestGetConstraints_WithScope verifies the scope filter path.
func TestGetConstraints_WithScope(t *testing.T) {
	db := seedE2EDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	ctx := context.Background()

	// With scope="global" filter.
	result, err := ts.Client().CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "get_constraints",
			Arguments: map[string]any{"scope": "global"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool get_constraints: %v", err)
	}
	text := extractText(t, result)
	if !strings.Contains(text, "go build") {
		t.Errorf("expected Go constraints with global scope, got:\n%s", text)
	}
}

// TestGetConstraints_NoConstraints tests with nodes that trigger no constraints.
func TestGetConstraints_NoConstraints(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Seed with a non-Go, non-infra node.
	ctx := context.Background()
	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		node := map[string]any{
			"id": "readme.md", "name": "readme.md", "type": "file",
			"file_path": "readme.md", "module": "",
			"language": "Markdown", "class": "doc", "status": "active",
		}
		data, _ := json.Marshal(node)
		return wtx.Put([]byte("kn:readme.md"), data)
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ts := startTestServer(t, db)
	result, err := ts.Client().CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "get_constraints",
			Arguments: map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	text := extractText(t, result)
	if !strings.Contains(text, "No constraints") {
		t.Errorf("expected 'No constraints', got: %s", text)
	}
}

// TestCustomTemplatesOverride verifies the override warning path.
func TestCustomTemplatesOverride(t *testing.T) {
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Create a .pcke/templates/ with a template that overrides "onboarding".
	templDir := dir + "/.pcke/templates"
	if err := os.MkdirAll(templDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := `name = "onboarding"
description = "Custom onboarding"
sections = ["architecture"]
`
	if err := os.WriteFile(templDir+"/override.toml", []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Creating a server with this root should trigger the override warning (to stderr).
	srv := pckmcp.New(db, dir)
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
}

// TestRecallTool_WithLimit tests the limit parameter.
func TestRecallTool_WithLimit(t *testing.T) {
	db := seedE2EDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	ctx := context.Background()

	result, err := ts.Client().CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "recall",
			Arguments: map[string]any{"query": "Go", "limit": float64(2)},
		},
	})
	if err != nil {
		t.Fatalf("CallTool recall with limit: %v", err)
	}
	text := extractText(t, result)
	if text == "" {
		t.Error("expected non-empty recall result")
	}
}

// TestE2E_GetPrompt_ReviewWithModule tests prompt with module argument.
func TestE2E_GetPrompt_ReviewWithModule(t *testing.T) {
	db := seedE2EDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	ctx := context.Background()

	result, err := ts.Client().GetPrompt(ctx, mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{
			Name:      "review",
			Arguments: map[string]string{"module": "internal/kdb"},
		},
	})
	if err != nil {
		t.Fatalf("GetPrompt review with module: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Error("expected non-empty messages")
	}
}

// TestE2E_GetPrompt_DebugWithModule tests the debug template.
func TestE2E_GetPrompt_DebugWithModule(t *testing.T) {
	db := seedE2EDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	ctx := context.Background()

	result, err := ts.Client().GetPrompt(ctx, mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{
			Name:      "debug",
			Arguments: map[string]string{"module": "cmd/pcke"},
		},
	})
	if err != nil {
		t.Fatalf("GetPrompt debug: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Error("expected non-empty messages")
	}
}

// TestE2E_GetPrompt_RefactorNoModule tests the refactor template.
func TestE2E_GetPrompt_RefactorNoModule(t *testing.T) {
	db := seedE2EDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	ctx := context.Background()

	result, err := ts.Client().GetPrompt(ctx, mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{Name: "refactor"},
	})
	if err != nil {
		t.Fatalf("GetPrompt refactor: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Error("expected non-empty messages")
	}
}

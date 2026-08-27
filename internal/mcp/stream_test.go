package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb/event"
	pckmcp "github.com/jenaiz/pcke/internal/mcp"
	"github.com/mark3labs/mcp-go/mcp"
)

// --- Stream tests ---

func TestStreamWriter_BelowThreshold(t *testing.T) {
	ctx := context.Background()
	sw := pckmcp.NewStreamWriter(ctx, 50, 20)

	for i := 0; i < 10; i++ {
		if err := sw.WriteItem(`{"id": "item"}`); err != nil {
			t.Fatalf("WriteItem: %v", err)
		}
	}

	if sw.NeedsChunking() {
		t.Error("expected no chunking for 10 items with threshold 50")
	}

	text, err := sw.Flush()
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Below threshold: plain format with --- separators.
	if !strings.Contains(text, `{"id": "item"}`) {
		t.Error("expected items in plain output")
	}
	if strings.Contains(text, `"chunk_index"`) {
		t.Error("should not contain chunk metadata below threshold")
	}
}

func TestStreamWriter_AboveThreshold(t *testing.T) {
	ctx := context.Background()
	sw := pckmcp.NewStreamWriter(ctx, 5, 3)

	for i := 0; i < 10; i++ {
		if err := sw.WriteItem(`{"id": "item"}`); err != nil {
			t.Fatalf("WriteItem: %v", err)
		}
	}

	if !sw.NeedsChunking() {
		t.Error("expected chunking for 10 items with threshold 5")
	}

	text, err := sw.Flush()
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var chunks []pckmcp.ChunkedResult
	if err := json.Unmarshal([]byte(text), &chunks); err != nil {
		t.Fatalf("unmarshal chunks: %v", err)
	}

	// 10 items / 3 per chunk = 4 chunks (3+3+3+1).
	if len(chunks) != 4 {
		t.Errorf("expected 4 chunks, got %d", len(chunks))
	}

	totalItems := 0
	for i, c := range chunks {
		if c.Total != 10 {
			t.Errorf("chunk %d: total=%d, want 10", i, c.Total)
		}
		if c.ChunkIndex != i {
			t.Errorf("chunk %d: index=%d", i, c.ChunkIndex)
		}
		if c.ChunkCount != 4 {
			t.Errorf("chunk %d: count=%d, want 4", i, c.ChunkCount)
		}
		totalItems += len(c.Items)
	}

	if totalItems != 10 {
		t.Errorf("total items across chunks: %d, want 10", totalItems)
	}
}

func TestStreamWriter_CancelMidWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sw := pckmcp.NewStreamWriter(ctx, 50, 20)

	if err := sw.WriteItem("first"); err != nil {
		t.Fatalf("first write: %v", err)
	}

	cancel()

	if err := sw.WriteItem("second"); err == nil {
		t.Error("expected error after cancellation")
	}

	_, err := sw.Flush()
	if err == nil {
		t.Error("expected flush error after cancellation")
	}
}

func TestStreamWriter_EmptyFlush(t *testing.T) {
	ctx := context.Background()
	sw := pckmcp.NewStreamWriter(ctx, 0, 0)

	text, err := sw.Flush()
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if text != "No results found." {
		t.Errorf("expected 'No results found.', got: %s", text)
	}
}

func TestStreamWriter_DefaultThreshold(t *testing.T) {
	ctx := context.Background()
	sw := pckmcp.NewStreamWriter(ctx, 0, 0) // defaults

	if sw.Len() != 0 {
		t.Errorf("expected 0 items, got %d", sw.Len())
	}

	for i := 0; i < 51; i++ {
		if err := sw.WriteItem("item"); err != nil {
			t.Fatalf("WriteItem: %v", err)
		}
	}

	if !sw.NeedsChunking() {
		t.Error("expected chunking at default threshold (50) with 51 items")
	}
}

// --- Subscription tests ---

func TestBroker_Subscribe_PublishReceive(t *testing.T) {
	b := pckmcp.NewBroker()

	var received pckmcp.Event
	unsub, err := b.Subscribe(pckmcp.EventScanCompleted, func(e pckmcp.Event) {
		received = e
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsub()

	b.Publish(pckmcp.Event{Type: pckmcp.EventScanCompleted, Detail: "42 nodes"})

	if received.Type != pckmcp.EventScanCompleted {
		t.Errorf("expected %s, got %s", pckmcp.EventScanCompleted, received.Type)
	}
	if received.Detail != "42 nodes" {
		t.Errorf("expected '42 nodes', got %q", received.Detail)
	}
}

func TestBroker_Subscribe_EmptyEvent(t *testing.T) {
	b := pckmcp.NewBroker()

	_, err := b.Subscribe("", func(pckmcp.Event) {})
	if err == nil {
		t.Error("expected error for empty event type")
	}
}

func TestBroker_Subscribe_NilHandler(t *testing.T) {
	b := pckmcp.NewBroker()

	_, err := b.Subscribe(pckmcp.EventScanCompleted, nil)
	if err == nil {
		t.Error("expected error for nil handler")
	}
}

func TestBroker_Unsubscribe(t *testing.T) {
	b := pckmcp.NewBroker()

	var count atomic.Int32
	unsub, err := b.Subscribe(pckmcp.EventScanCompleted, func(pckmcp.Event) {
		count.Add(1)
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	b.Publish(pckmcp.Event{Type: pckmcp.EventScanCompleted})
	if count.Load() != 1 {
		t.Errorf("expected 1 call, got %d", count.Load())
	}

	unsub()
	unsub() // idempotent

	b.Publish(pckmcp.Event{Type: pckmcp.EventScanCompleted})
	if count.Load() != 1 {
		t.Errorf("expected still 1 call after unsubscribe, got %d", count.Load())
	}
}

func TestBroker_Subscribe_DuplicateEvent(t *testing.T) {
	b := pckmcp.NewBroker()

	var count atomic.Int32
	for i := 0; i < 3; i++ {
		unsub, err := b.Subscribe(pckmcp.EventScanCompleted, func(pckmcp.Event) {
			count.Add(1)
		})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		defer unsub()
	}

	b.Publish(pckmcp.Event{Type: pckmcp.EventScanCompleted})
	if count.Load() != 3 {
		t.Errorf("expected 3 calls, got %d", count.Load())
	}
}

func TestBroker_MultipleEventTypes(t *testing.T) {
	b := pckmcp.NewBroker()

	var scanCount, ruleCount atomic.Int32

	unsub1, _ := b.Subscribe(pckmcp.EventScanCompleted, func(pckmcp.Event) {
		scanCount.Add(1)
	})
	defer unsub1()

	unsub2, _ := b.Subscribe(pckmcp.EventRuleAdded, func(pckmcp.Event) {
		ruleCount.Add(1)
	})
	defer unsub2()

	b.Publish(pckmcp.Event{Type: pckmcp.EventScanCompleted})
	b.Publish(pckmcp.Event{Type: pckmcp.EventRuleAdded})
	b.Publish(pckmcp.Event{Type: pckmcp.EventScanCompleted})

	if scanCount.Load() != 2 {
		t.Errorf("scan events: %d, want 2", scanCount.Load())
	}
	if ruleCount.Load() != 1 {
		t.Errorf("rule events: %d, want 1", ruleCount.Load())
	}
}

func TestBroker_ConcurrentSubscribePublish(t *testing.T) {
	b := pckmcp.NewBroker()

	var total atomic.Int32
	var wg sync.WaitGroup
	done := make(chan struct{})

	// Spawn subscribers.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unsub, err := b.Subscribe(pckmcp.EventKnowledgeUpdated, func(pckmcp.Event) {
				total.Add(1)
			})
			if err != nil {
				t.Errorf("Subscribe: %v", err)
				return
			}
			// Hold subscription alive until publishers finish.
			<-done
			unsub()
		}()
	}

	// Spawn publishers.
	var pubWg sync.WaitGroup
	for i := 0; i < 100; i++ {
		pubWg.Add(1)
		go func() {
			defer pubWg.Done()
			b.Publish(pckmcp.Event{Type: pckmcp.EventKnowledgeUpdated})
		}()
	}

	pubWg.Wait()
	close(done) // signal subscribers to unsubscribe
	wg.Wait()

	// We don't assert exact counts because of timing, but there should be
	// no data races (run with -race).
	if total.Load() == 0 {
		t.Error("expected some events to be received")
	}
}

func TestBroker_PanicHandler(t *testing.T) {
	b := pckmcp.NewBroker()

	var after atomic.Int32

	unsub1, _ := b.Subscribe(pckmcp.EventScanCompleted, func(pckmcp.Event) {
		panic("bad handler")
	})
	defer unsub1()

	unsub2, _ := b.Subscribe(pckmcp.EventScanCompleted, func(pckmcp.Event) {
		after.Add(1)
	})
	defer unsub2()

	b.Publish(pckmcp.Event{Type: pckmcp.EventScanCompleted})

	if after.Load() != 1 {
		t.Error("second handler should be called despite first handler panicking")
	}
}

func TestBroker_SubscriberCount(t *testing.T) {
	b := pckmcp.NewBroker()

	if b.SubscriberCount("") != 0 {
		t.Error("expected 0 total subscribers")
	}

	unsub1, _ := b.Subscribe(pckmcp.EventScanCompleted, func(pckmcp.Event) {})
	unsub2, _ := b.Subscribe(pckmcp.EventRuleAdded, func(pckmcp.Event) {})
	unsub3, _ := b.Subscribe(pckmcp.EventScanCompleted, func(pckmcp.Event) {})

	if b.SubscriberCount("") != 3 {
		t.Errorf("expected 3 total, got %d", b.SubscriberCount(""))
	}
	if b.SubscriberCount(pckmcp.EventScanCompleted) != 2 {
		t.Errorf("expected 2 scan subs, got %d", b.SubscriberCount(pckmcp.EventScanCompleted))
	}
	if b.SubscriberCount(pckmcp.EventRuleAdded) != 1 {
		t.Errorf("expected 1 rule sub, got %d", b.SubscriberCount(pckmcp.EventRuleAdded))
	}

	unsub1()
	if b.SubscriberCount(pckmcp.EventScanCompleted) != 1 {
		t.Errorf("after unsub: expected 1 scan sub, got %d", b.SubscriberCount(pckmcp.EventScanCompleted))
	}

	unsub2()
	unsub3()
	if b.SubscriberCount("") != 0 {
		t.Errorf("after all unsubs: expected 0, got %d", b.SubscriberCount(""))
	}
}

// --- Prompt template tests ---

func TestListPrompts(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	ctx := context.Background()

	result, err := ts.Client().ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}

	if len(result.Prompts) != 4 {
		t.Errorf("expected 4 prompts, got %d", len(result.Prompts))
	}

	names := map[string]bool{}
	for _, p := range result.Prompts {
		names[p.Name] = true
	}
	for _, expected := range []string{"onboarding", "review", "debug", "refactor"} {
		if !names[expected] {
			t.Errorf("missing prompt %q", expected)
		}
	}
}

func TestGetPrompt_Onboarding(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	ctx := context.Background()

	result, err := ts.Client().GetPrompt(ctx, mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{
			Name: "onboarding",
		},
	})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}

	// The onboarding template should include architecture content.
	text := extractPromptText(t, result)
	if !strings.Contains(text, "Architecture") || !strings.Contains(text, "Go") {
		t.Errorf("onboarding template should contain architecture info, got:\n%s", truncate(text, 300))
	}
}

func TestGetPrompt_Review_WithModule(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	ctx := context.Background()

	result, err := ts.Client().GetPrompt(ctx, mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{
			Name:      "review",
			Arguments: map[string]string{"module": "cmd/pcke"},
		},
	})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}

	if result.Description == "" {
		t.Error("expected non-empty description")
	}
}

func TestGetPrompt_Debug_WithModule(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	ctx := context.Background()

	result, err := ts.Client().GetPrompt(ctx, mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{
			Name:      "debug",
			Arguments: map[string]string{"module": "internal/kdb"},
		},
	})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
}

func TestGetPrompt_Refactor(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	ts := startTestServer(t, db)
	ctx := context.Background()

	result, err := ts.Client().GetPrompt(ctx, mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{
			Name: "refactor",
		},
	})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
}

// --- Sampling / proactive context tests ---

func TestSuggestContext_Disabled(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	srv := pckmcp.New(db, t.TempDir())
	ctx := context.Background()

	pc, err := srv.SuggestContext(ctx, "tell me about internal/kdb", false)
	if err != nil {
		t.Fatalf("SuggestContext: %v", err)
	}
	if pc != nil {
		t.Error("expected nil when disabled")
	}
}

func TestSuggestContext_Enabled_ModuleFound(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	srv := pckmcp.New(db, t.TempDir())
	ctx := context.Background()

	pc, err := srv.SuggestContext(ctx, "tell me about internal/kdb", true)
	if err != nil {
		t.Fatalf("SuggestContext: %v", err)
	}
	if pc == nil {
		t.Fatal("expected proactive context for internal/kdb")
		return
	}
	if pc.Module != "internal/kdb" {
		t.Errorf("expected module 'internal/kdb', got %q", pc.Module)
	}
	if pc.Constraints == "" {
		t.Error("expected non-empty constraints")
	}
}

func TestSuggestContext_Enabled_NoModule(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	srv := pckmcp.New(db, t.TempDir())
	ctx := context.Background()

	pc, err := srv.SuggestContext(ctx, "how do I write good tests?", true)
	if err != nil {
		t.Fatalf("SuggestContext: %v", err)
	}
	if pc != nil {
		t.Error("expected nil when no module matches")
	}
}

func TestSuggestContext_DecisionWarnings(t *testing.T) {
	// seedDB plants kn:/rel: records; we layer typed-event decisions +
	// decision_links on top so SuggestContext can find both the legacy
	// module (via nodes) and the must-severity rules attached to its
	// files (via the new graph traversal).
	db := seedDB(t)
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	store := event.New(db)
	now := time.Now().UTC()
	// Must rule attached to internal/kdb/db.go.
	if _, err := store.Append(ctx, &event.Decision{
		Hdr:      event.Header{CreatedAt: now},
		DID:      "rule-mvcc",
		Title:    "Always commit transactions",
		Body:     "Use db.Update or explicit commit.",
		Severity: event.SeverityMust,
		Scope:    event.ScopeModule,
		Source:   "adr",
	}); err != nil {
		t.Fatalf("append must decision: %v", err)
	}
	// Should rule on the same file — must NOT appear in warnings.
	if _, err := store.Append(ctx, &event.Decision{
		Hdr:      event.Header{CreatedAt: now},
		DID:      "rule-format",
		Title:    "Run gofumpt",
		Severity: event.SeverityShould,
		Scope:    event.ScopeModule,
		Source:   "adr",
	}); err != nil {
		t.Fatalf("append should decision: %v", err)
	}
	if _, err := store.AppendLink(ctx, &event.Link{
		Hdr: event.Header{CreatedAt: now}, SrcRef: "e:internal/kdb/db.go", EdgeType: "decision_link", DstRef: "d:rule-mvcc",
	}); err != nil {
		t.Fatalf("append link: %v", err)
	}
	if _, err := store.AppendLink(ctx, &event.Link{
		Hdr: event.Header{CreatedAt: now}, SrcRef: "e:internal/kdb/db.go", EdgeType: "decision_link", DstRef: "d:rule-format",
	}); err != nil {
		t.Fatalf("append link: %v", err)
	}

	srv := pckmcp.New(db, t.TempDir())
	pc, err := srv.SuggestContext(ctx, "tell me about internal/kdb", true)
	if err != nil {
		t.Fatalf("SuggestContext: %v", err)
	}
	if pc == nil {
		t.Fatal("expected proactive context")
		return
	}
	if len(pc.Warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly 1 (must-severity only)", pc.Warnings)
	}
	if pc.Warnings[0].DID != "rule-mvcc" {
		t.Errorf("warning DID = %q, want rule-mvcc", pc.Warnings[0].DID)
	}
	if pc.Warnings[0].Severity != "must" {
		t.Errorf("warning severity = %q, want must", pc.Warnings[0].Severity)
	}
}

func TestSuggestContext_NoTypedEventsIsEmpty(t *testing.T) {
	// Legacy KB: no typed events seeded. Warnings should be empty,
	// constraints + history still rendered.
	db := seedDB(t)
	defer func() { _ = db.Close() }()
	srv := pckmcp.New(db, t.TempDir())
	pc, err := srv.SuggestContext(context.Background(), "tell me about internal/kdb", true)
	if err != nil {
		t.Fatalf("SuggestContext: %v", err)
	}
	if pc == nil {
		t.Fatal("expected proactive context for matched module")
		return
	}
	if len(pc.Warnings) != 0 {
		t.Errorf("legacy KB warnings = %v, want empty", pc.Warnings)
	}
	if pc.Constraints == "" {
		t.Errorf("expected constraints to still render on legacy KB")
	}
}

// --- Server integration tests ---

func TestServer_NotifyEvent(t *testing.T) {
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	srv := pckmcp.New(db, t.TempDir())

	var received pckmcp.Event
	unsub, _ := srv.Broker().Subscribe(pckmcp.EventScanCompleted, func(e pckmcp.Event) {
		received = e
	})
	defer unsub()

	srv.NotifyEvent(pckmcp.Event{
		Type:   pckmcp.EventScanCompleted,
		Detail: "test scan",
	})

	if received.Type != pckmcp.EventScanCompleted {
		t.Errorf("expected scan.completed, got %s", received.Type)
	}
}

func TestRecallTool_Streaming(t *testing.T) {
	// Seed a large DB to trigger chunked mode.
	db := seedDB(t)
	defer func() { _ = db.Close() }()

	// The default test DB has 4 nodes, which won't trigger chunking.
	// This test verifies that the streaming-aware handler still works
	// in plain mode with the existing data.
	ts := startTestServer(t, db)
	text := callTool(t, ts, "recall", map[string]any{"query": "go source"})

	if !strings.Contains(text, "main.go") {
		t.Errorf("expected main.go in results, got:\n%s", text)
	}
}

// --- Helpers ---

func extractPromptText(t *testing.T, result *mcp.GetPromptResult) string {
	t.Helper()
	if result == nil || len(result.Messages) == 0 {
		t.Fatal("empty prompt result")
	}
	for _, msg := range result.Messages {
		if tc, ok := msg.Content.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatal("no text content in prompt result")
	return ""
}

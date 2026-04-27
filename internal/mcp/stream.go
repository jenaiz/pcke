package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// defaultStreamThreshold is the minimum number of results that triggers
	// chunked mode. Below this threshold, results are returned as-is.
	defaultStreamThreshold = 50

	// defaultChunkSize is the number of items per chunk.
	defaultChunkSize = 20
)

// ChunkedResult represents a response that has been split into logical chunks.
// Each chunk contains a subset of the total results along with metadata that
// allows the agent to track progress and assemble the full result set.
//
// This is a logical chunking within a single JSON-RPC response — the stdio
// transport still delivers the entire response atomically. The structure
// prepares for true progressive delivery when the transport supports it.
type ChunkedResult struct {
	// Total is the total number of items across all chunks.
	Total int `json:"total"`
	// ChunkIndex is the zero-based index of this chunk.
	ChunkIndex int `json:"chunk_index"`
	// ChunkCount is the total number of chunks.
	ChunkCount int `json:"chunk_count"`
	// Items contains the results for this chunk.
	Items []string `json:"items"`
}

// StreamWriter emits progressive MCP results to a connected agent.
//
// It is safe for single-goroutine use. The writer collects items and, when
// Flush is called, produces either a single result (below threshold) or a
// chunked result set. It respects context cancellation to avoid unnecessary
// work when the client disconnects.
type StreamWriter struct {
	ctx       context.Context
	items     []string
	threshold int
	chunkSize int
}

// NewStreamWriter creates a StreamWriter that will chunk results when the
// item count exceeds the given threshold. Pass 0 for threshold and chunkSize
// to use defaults (50 and 20 respectively).
func NewStreamWriter(ctx context.Context, threshold, chunkSize int) *StreamWriter {
	if threshold <= 0 {
		threshold = defaultStreamThreshold
	}
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}
	return &StreamWriter{
		ctx:       ctx,
		threshold: threshold,
		chunkSize: chunkSize,
	}
}

// WriteItem appends a result item to the writer. It returns an error if the
// context has been cancelled.
func (w *StreamWriter) WriteItem(item string) error {
	if err := w.ctx.Err(); err != nil {
		return fmt.Errorf("mcp: stream cancelled: %w", err)
	}
	w.items = append(w.items, item)
	return nil
}

// Len returns the number of items written so far.
func (w *StreamWriter) Len() int {
	return len(w.items)
}

// NeedsChunking reports whether the current item count exceeds the threshold.
func (w *StreamWriter) NeedsChunking() bool {
	return len(w.items) > w.threshold
}

// Flush produces the final response text. If the item count is at or below the
// threshold, it returns items joined by separators (classic mode). If above,
// it returns a JSON array of ChunkedResult objects.
//
// It returns an error if the context is cancelled or JSON marshaling fails.
func (w *StreamWriter) Flush() (string, error) {
	if err := w.ctx.Err(); err != nil {
		return "", fmt.Errorf("mcp: stream cancelled: %w", err)
	}

	if !w.NeedsChunking() {
		return w.flushPlain(), nil
	}

	return w.flushChunked()
}

// flushPlain returns items in the legacy single-response format.
func (w *StreamWriter) flushPlain() string {
	if len(w.items) == 0 {
		return "No results found."
	}
	return strings.Join(w.items, "\n---\n")
}

// flushChunked produces a JSON array of ChunkedResult objects.
func (w *StreamWriter) flushChunked() (string, error) {
	total := len(w.items)
	chunkCount := (total + w.chunkSize - 1) / w.chunkSize

	chunks := make([]ChunkedResult, 0, chunkCount)
	for i := 0; i < total; i += w.chunkSize {
		if err := w.ctx.Err(); err != nil {
			return "", fmt.Errorf("mcp: stream cancelled during chunking: %w", err)
		}

		end := i + w.chunkSize
		if end > total {
			end = total
		}

		chunks = append(chunks, ChunkedResult{
			Total:      total,
			ChunkIndex: i / w.chunkSize,
			ChunkCount: chunkCount,
			Items:      w.items[i:end],
		})
	}

	data, err := json.MarshalIndent(chunks, "", "  ")
	if err != nil {
		return "", fmt.Errorf("mcp: marshal chunks: %w", err)
	}
	return string(data), nil
}

package context

import (
	"fmt"
	"time"
)

// Session tracks served context items within a single MCP connection
// to provide novelty scoring. Items already served are ranked lower
// on subsequent requests. In-memory only; lost on restart.
type Session struct {
	ID        string
	StartedAt time.Time
	Served    map[string]int // itemKey → times served
	Files     map[string]int // file_path → access count
	Workflow  string
}

// NewSession creates a fresh session with a unique ID.
func NewSession() *Session {
	return &Session{
		ID:        generateSessionID(),
		StartedAt: time.Now(),
		Served:    make(map[string]int),
		Files:     make(map[string]int),
	}
}

// MarkServed records that an item was included in a response.
func (s *Session) MarkServed(key string) {
	if s == nil {
		return
	}
	s.Served[key]++
}

// MarkFileAccessed records a file access in this session.
func (s *Session) MarkFileAccessed(path string) {
	if s == nil {
		return
	}
	s.Files[path]++
}

// NoveltyScore returns a score in [0, 1] for the given item key.
// Items never served get 1.0; score decreases with repeated serving.
func (s *Session) NoveltyScore(key string) float64 {
	if s == nil {
		return 1.0
	}
	times := s.Served[key]
	if times == 0 {
		return 1.0
	}
	score := 1.0 - 0.5*float64(times)
	if score < 0 {
		return 0
	}
	return score
}

// generateSessionID produces a simple unique session identifier.
func generateSessionID() string {
	return fmt.Sprintf("sess_%d", time.Now().UnixNano())
}

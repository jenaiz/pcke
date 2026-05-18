// Package session tracks per-MCP-session state so the retrieval engine
// can apply novelty scoring across multiple tool calls.
//
// Phase 13 (PRD v5.2 §4.6) ships an in-memory implementation. The
// Session interface is the seam that Phase 14 will swap for a kdb-backed
// store without changing call sites.
//
// All exported types are safe for concurrent use.
package session

import (
	"sync"
	"time"
)

// Observation is the unit of session writes: one snapshot of refs that
// were served to the agent in a single retrieval response, with the
// time the response was produced. Order is preserved.
//
// Tool is optional metadata describing which MCP tool produced the
// observation (e.g. "get_context_for_file"). The in-memory Session
// ignores it; the persistent variant (Phase 14) uses it as the Subject
// of the ToolCall observation it writes to the graph.
type Observation struct {
	Refs []string
	At   time.Time
	Tool string
}

// Session is the read+write interface to one client's session state.
// Implementations may persist; this package ships an in-memory variant
// (see NewMemorySession / NewMemoryStore).
type Session interface {
	// ID returns the session's stable identifier.
	ID() string
	// Note records an Observation. Refs are deduplicated against any
	// previously-noted refs so Served() never returns duplicates.
	Note(o Observation)
	// Served returns the union of all refs noted on this session,
	// in stable insertion order.
	Served() []string
	// Close releases any resources the session holds. The in-memory
	// implementation is a no-op but real-storage implementations
	// (Phase 14) may flush or unlock.
	Close() error
}

// MemorySession is the in-memory Session implementation.
type MemorySession struct {
	mu       sync.RWMutex
	id       string
	seen     map[string]struct{}
	ordered  []string
	lastNote time.Time
}

// NewMemorySession constructs a session backed by an in-memory set.
// Sessions are cheap to create; the caller is responsible for keeping
// the same instance alive across related tool calls.
func NewMemorySession(id string) *MemorySession {
	return &MemorySession{
		id:   id,
		seen: make(map[string]struct{}),
	}
}

// ID returns the session id.
func (s *MemorySession) ID() string { return s.id }

// Note records an Observation, deduplicating refs against the existing
// served set. Refs with empty strings are ignored.
func (s *MemorySession) Note(o Observation) {
	if len(o.Refs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range o.Refs {
		if r == "" {
			continue
		}
		if _, dup := s.seen[r]; dup {
			continue
		}
		s.seen[r] = struct{}{}
		s.ordered = append(s.ordered, r)
	}
	if o.At.After(s.lastNote) {
		s.lastNote = o.At
	}
}

// Served returns a copy of the ordered ref list. The slice is safe for
// the caller to retain; mutating it does not affect the session.
func (s *MemorySession) Served() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.ordered))
	copy(out, s.ordered)
	return out
}

// LastNoteAt returns the most recent Observation timestamp. Used by
// the store's TTL-based eviction (Phase 14); the in-memory store does
// not currently expire sessions but exposes the field for parity with
// the persistent implementation.
func (s *MemorySession) LastNoteAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastNote
}

// Close is a no-op for the in-memory implementation.
func (s *MemorySession) Close() error { return nil }

// Store is a factory + registry for sessions. Get returns the same
// Session for the same id across calls; new sessions are created on
// demand so the caller never has to manage allocation.
type Store interface {
	Get(id string) Session
	Close() error
}

// MemoryStore is an in-memory Store. The zero value is not usable;
// construct with NewMemoryStore.
type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string]*MemorySession
}

// NewMemoryStore constructs an empty in-memory Store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: map[string]*MemorySession{}}
}

// Get returns the Session for id, creating it on first reference.
// Empty id is allowed and returns a fresh anonymous session each call;
// this is a convenience for callers that haven't allocated an id.
func (m *MemoryStore) Get(id string) Session {
	if id == "" {
		return NewMemorySession("")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		return s
	}
	s := NewMemorySession(id)
	m.sessions[id] = s
	return s
}

// Close releases all in-memory sessions.
func (m *MemoryStore) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		_ = s.Close()
	}
	m.sessions = map[string]*MemorySession{}
	return nil
}

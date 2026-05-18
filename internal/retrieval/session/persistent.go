package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/graph"
	"github.com/jenaiz/pcke/internal/observe"
)

// CallSink is the minimal observe.Collector surface PersistentSession
// needs. Decoupling via interface keeps the package testable without
// constructing a real collector.
type CallSink interface {
	RecordSessionStart(observe.SessionStart)
	RecordCall(observe.Call)
}

// PersistentSession is a Session whose Note() also persists a ToolCall
// observation through a CallSink (typically *observe.Collector) so the
// session subgraph survives `pcke serve` restart.
//
// Reads (Served, LastNoteAt) hit only the in-memory cache. On first
// construction PersistentStore replays prior calls from the graph so
// dedup novelty scoring carries across restarts.
type PersistentSession struct {
	mem  *MemorySession
	sink CallSink
}

// ID returns the session id.
func (p *PersistentSession) ID() string { return p.mem.ID() }

// Note records refs into the in-memory dedup cache and asynchronously
// persists a ToolCall observation. Refs are deduplicated against the
// cache exactly as the MemorySession does; the persisted Call captures
// the *original* refs (including duplicates) so the audit log reflects
// what the tool actually returned.
func (p *PersistentSession) Note(o Observation) {
	p.mem.Note(o)
	if p.sink == nil || len(o.Refs) == 0 {
		return
	}
	tool := o.Tool
	if tool == "" {
		tool = "unknown"
	}
	p.sink.RecordCall(observe.Call{
		UUID:        newCallID(),
		SessionUUID: p.mem.ID(),
		Tool:        tool,
		Served:      o.Refs,
		At:          o.At,
	})
}

// Served returns the deduped union of refs noted on this session.
func (p *PersistentSession) Served() []string { return p.mem.Served() }

// Close releases in-memory state. The persisted observations remain in
// kdb; only the dedup cache is dropped.
func (p *PersistentSession) Close() error { return p.mem.Close() }

// PersistentStore implements Store by returning PersistentSession
// instances backed by the supplied CallSink and a kdb.DB used for
// replay-on-first-Get.
//
// The zero value is not usable; construct with NewPersistentStore.
type PersistentStore struct {
	mu       sync.Mutex
	sessions map[string]*PersistentSession

	db    *kdb.DB
	store *event.Store
	sink  CallSink
}

// NewPersistentStore constructs a Store that persists session state via
// sink and rebuilds it on first Get from db.
//
// sink may be nil for read-only use (e.g. CLI surfaces that want to
// query session state without writing). In that case Note becomes a
// no-op observer that only updates the in-memory dedup cache.
func NewPersistentStore(db *kdb.DB, sink CallSink) *PersistentStore {
	return &PersistentStore{
		sessions: map[string]*PersistentSession{},
		db:       db,
		store:    event.New(db),
		sink:     sink,
	}
}

// Get returns the Session for id, creating it on first reference.
// On first creation the prior call history for id is replayed from kdb
// so Served() reflects what was served in earlier `pcke serve` runs.
//
// Empty id is allowed and yields a fresh anonymous in-memory session
// per call — symmetric with MemoryStore.Get.
func (s *PersistentStore) Get(id string) Session {
	if id == "" {
		return NewMemorySession("")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.sessions[id]; ok {
		return existing
	}
	ps := &PersistentSession{
		mem:  NewMemorySession(id),
		sink: s.sink,
	}
	s.replay(id, ps)
	if s.sink != nil {
		s.sink.RecordSessionStart(observe.SessionStart{UUID: id})
	}
	s.sessions[id] = ps
	return ps
}

// Close releases in-memory state. Persisted observations are untouched.
func (s *PersistentStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ps := range s.sessions {
		_ = ps.Close()
	}
	s.sessions = map[string]*PersistentSession{}
	return nil
}

// replay loads the served-refs union for session id from kdb into the
// in-memory cache. Walks: session →(contains)→ call →(served)→ ref.
//
// Best-effort: traversal errors are logged via the observe package
// (already routed through pckelog) and treated as "no replay". The
// next live Note will repopulate the cache.
func (s *PersistentStore) replay(id string, p *PersistentSession) {
	ctx := context.Background()
	sessRef := graph.Ref(event.SessionRef(id))

	callRefs, err := graph.Neighbors(ctx, s.db, sessRef, graph.TraversalOptions{
		Direction: graph.Forward,
		EdgeTypes: []string{event.EdgeContains},
	})
	if err != nil || len(callRefs) == 0 {
		return
	}

	var refs []string
	for _, callRef := range callRefs {
		served, sErr := graph.Neighbors(ctx, s.db, callRef, graph.TraversalOptions{
			Direction: graph.Forward,
			EdgeTypes: []string{event.EdgeServed},
		})
		if sErr != nil {
			continue
		}
		for _, sr := range served {
			refs = append(refs, string(sr))
		}
	}
	if len(refs) == 0 {
		return
	}
	p.mem.Note(Observation{Refs: refs})
}

// newCallID returns a 16-byte random hex string. Sufficiently
// collision-resistant for per-process call identifiers; the persisted
// graph treats UUIDs as opaque strings.
func newCallID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read is documented to never fail on supported
		// platforms; if it does we have bigger problems than session ids.
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}

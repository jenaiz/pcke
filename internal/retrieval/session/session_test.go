package session

import (
	"slices"
	"sync"
	"testing"
	"time"
)

func TestMemorySession_NoteAndServed(t *testing.T) {
	t.Parallel()
	s := NewMemorySession("alpha")
	s.Note(Observation{Refs: []string{"e:a.go", "d:rule-1"}, At: time.Unix(1, 0)})
	s.Note(Observation{Refs: []string{"e:b.go"}, At: time.Unix(2, 0)})

	got := s.Served()
	want := []string{"e:a.go", "d:rule-1", "e:b.go"}
	if !slices.Equal(got, want) {
		t.Errorf("Served = %v, want %v", got, want)
	}
}

func TestMemorySession_DedupesRefs(t *testing.T) {
	t.Parallel()
	s := NewMemorySession("alpha")
	s.Note(Observation{Refs: []string{"e:a.go", "e:a.go", "d:r"}})
	s.Note(Observation{Refs: []string{"d:r", "e:b.go"}})

	got := s.Served()
	want := []string{"e:a.go", "d:r", "e:b.go"}
	if !slices.Equal(got, want) {
		t.Errorf("Served = %v, want %v (dedup + preserved order)", got, want)
	}
}

func TestMemorySession_IgnoresEmptyRefs(t *testing.T) {
	t.Parallel()
	s := NewMemorySession("alpha")
	s.Note(Observation{Refs: []string{"", "e:a.go", ""}})
	got := s.Served()
	if len(got) != 1 || got[0] != "e:a.go" {
		t.Errorf("Served = %v, want [e:a.go]", got)
	}
}

func TestMemorySession_ReturnsCopy(t *testing.T) {
	t.Parallel()
	s := NewMemorySession("alpha")
	s.Note(Observation{Refs: []string{"e:a.go"}})
	got := s.Served()
	got[0] = "MUTATED"
	if g := s.Served(); g[0] != "e:a.go" {
		t.Errorf("internal state was mutated through returned slice: %v", g)
	}
}

func TestMemorySession_LastNoteAt(t *testing.T) {
	t.Parallel()
	s := NewMemorySession("alpha")
	earlier := time.Unix(100, 0)
	later := time.Unix(200, 0)
	s.Note(Observation{Refs: []string{"e:a.go"}, At: later})
	s.Note(Observation{Refs: []string{"e:b.go"}, At: earlier}) // out-of-order
	if got := s.LastNoteAt(); !got.Equal(later) {
		t.Errorf("LastNoteAt = %v, want %v (max-of-seen)", got, later)
	}
}

func TestMemoryStore_GetReturnsSameInstance(t *testing.T) {
	t.Parallel()
	m := NewMemoryStore()
	a := m.Get("alpha")
	b := m.Get("alpha")
	if a.ID() != "alpha" || b.ID() != "alpha" {
		t.Errorf("Get(alpha).ID() mismatch: %q / %q", a.ID(), b.ID())
	}
	a.Note(Observation{Refs: []string{"e:x"}})
	if !slices.Equal(b.Served(), []string{"e:x"}) {
		t.Errorf("second Get did not return same session; b.Served() = %v", b.Served())
	}
}

func TestMemoryStore_EmptyIDReturnsAnonymous(t *testing.T) {
	t.Parallel()
	m := NewMemoryStore()
	a := m.Get("")
	b := m.Get("")
	a.Note(Observation{Refs: []string{"e:x"}})
	if len(b.Served()) != 0 {
		t.Errorf("empty id should yield a fresh session per call, got b.Served() = %v", b.Served())
	}
}

func TestMemoryStore_Close(t *testing.T) {
	t.Parallel()
	m := NewMemoryStore()
	s := m.Get("alpha")
	s.Note(Observation{Refs: []string{"e:x"}})
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// After Close, a fresh Get for the same id returns a clean session.
	fresh := m.Get("alpha")
	if len(fresh.Served()) != 0 {
		t.Errorf("post-Close session should be empty, got %v", fresh.Served())
	}
}

func TestMemorySession_ConcurrentNote(t *testing.T) {
	t.Parallel()
	s := NewMemorySession("alpha")
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.Note(Observation{Refs: []string{"e:item-" + string(rune('A'+(i%26)))}})
		}(i)
	}
	wg.Wait()
	// 26 unique refs (one per letter A-Z).
	if got := len(s.Served()); got != 26 {
		t.Errorf("Served len = %d, want 26 (unique letters)", got)
	}
}

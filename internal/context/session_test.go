package context

import (
	"testing"
)

func TestNewSession(t *testing.T) {
	s := NewSession()
	if s.ID == "" {
		t.Fatal("session ID should not be empty")
	}
	if s.Served == nil {
		t.Fatal("Served map should be initialized")
	}
	if s.Files == nil {
		t.Fatal("Files map should be initialized")
	}
}

func TestNoveltyScore_NeverServed(t *testing.T) {
	s := NewSession()
	score := s.NoveltyScore("key1")
	if score != 1.0 {
		t.Fatalf("expected 1.0, got %f", score)
	}
}

func TestNoveltyScore_ServedOnce(t *testing.T) {
	s := NewSession()
	s.MarkServed("key1")
	score := s.NoveltyScore("key1")
	if score != 0.5 {
		t.Fatalf("expected 0.5, got %f", score)
	}
}

func TestNoveltyScore_ServedTwice(t *testing.T) {
	s := NewSession()
	s.MarkServed("key1")
	s.MarkServed("key1")
	score := s.NoveltyScore("key1")
	if score != 0.0 {
		t.Fatalf("expected 0.0, got %f", score)
	}
}

func TestNoveltyScore_ServedThrice_ClampedAtZero(t *testing.T) {
	s := NewSession()
	s.MarkServed("key1")
	s.MarkServed("key1")
	s.MarkServed("key1")
	score := s.NoveltyScore("key1")
	if score != 0.0 {
		t.Fatalf("expected 0.0, got %f", score)
	}
}

func TestNoveltyScore_NilSession(t *testing.T) {
	var s *Session
	score := s.NoveltyScore("key1")
	if score != 1.0 {
		t.Fatalf("nil session should return 1.0, got %f", score)
	}
}

func TestMarkServed_NilSession(_ *testing.T) {
	var s *Session
	s.MarkServed("key1") // Should not panic.
}

func TestMarkFileAccessed(t *testing.T) {
	s := NewSession()
	s.MarkFileAccessed("foo.go")
	s.MarkFileAccessed("foo.go")
	if s.Files["foo.go"] != 2 {
		t.Fatalf("expected 2, got %d", s.Files["foo.go"])
	}
}

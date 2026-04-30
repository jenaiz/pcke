package analysis

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/config"
	"github.com/jenaiz/pcke/internal/kdb"
)

func TestWatcher_ShouldIgnore(t *testing.T) {
	root := t.TempDir()

	db, err := kdb.Open(root, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	cfg := config.Defaults().Scan
	w, err := NewWatcher(root, db, cfg, WatcherOpts{Debounce: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Stop()

	tests := []struct {
		path   string
		ignore bool
	}{
		{filepath.Join(root, ".git", "objects", "abc"), true},
		{filepath.Join(root, "node_modules", "pkg", "index.js"), true},
		{filepath.Join(root, "vendor", "lib.go"), true},
		{filepath.Join(root, ".pcke", "data.kdb"), true},
		{filepath.Join(root, "src", "main.go"), false},
		{filepath.Join(root, "internal", "kdb", "db.go"), false},
	}

	for _, tc := range tests {
		got := w.ShouldIgnore(tc.path)
		if got != tc.ignore {
			t.Errorf("ShouldIgnore(%q) = %v, want %v", tc.path, got, tc.ignore)
		}
	}
}

func TestNewWatcher_ValidRoot(t *testing.T) {
	root := t.TempDir()

	db, err := kdb.Open(root, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	cfg := config.Defaults().Scan
	w, err := NewWatcher(root, db, cfg, WatcherOpts{Debounce: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	w.Stop()
}

func TestWatcher_DefaultDebounce(t *testing.T) {
	root := t.TempDir()

	db, err := kdb.Open(root, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	cfg := config.Defaults().Scan
	w, err := NewWatcher(root, db, cfg, WatcherOpts{})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Stop()

	if w.opts.Debounce != 500*time.Millisecond {
		t.Errorf("default debounce = %v, want 500ms", w.opts.Debounce)
	}
}

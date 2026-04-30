package analysis

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jenaiz/pcke/internal/config"
	"github.com/jenaiz/pcke/internal/kdb"
)

// WatcherOpts configures the file watcher.
type WatcherOpts struct {
	Verbose  bool          // Print verbose output.
	Debounce time.Duration // Debounce interval (default 500ms).
	// OnScan is called after each successful scan with the result.
	OnScan func(result *ScanResult)
}

// Watcher watches the repository for file changes and triggers scans.
type Watcher struct {
	root    string
	db      *kdb.DB
	cfg     config.ScanConfig
	opts    WatcherOpts
	fsw     *fsnotify.Watcher
	mu      sync.Mutex
	pending bool
	stop    chan struct{}
}

// NewWatcher creates a file watcher for the repository at root.
func NewWatcher(root string, db *kdb.DB, cfg config.ScanConfig, opts WatcherOpts) (*Watcher, error) {
	if opts.Debounce == 0 {
		opts.Debounce = 500 * time.Millisecond
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("watch: create watcher: %w", err)
	}

	w := &Watcher{
		root: root,
		db:   db,
		cfg:  cfg,
		opts: opts,
		fsw:  fsw,
		stop: make(chan struct{}),
	}

	if err := w.addDirs(); err != nil {
		_ = fsw.Close()
		return nil, err
	}

	return w, nil
}

// Run starts the watcher loop. It blocks until ctx is cancelled or Stop is called.
func (w *Watcher) Run(ctx context.Context) error {
	defer func() { _ = w.fsw.Close() }()

	var timer *time.Timer
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-w.stop:
			return nil
		case event, ok := <-w.fsw.Events:
			if !ok {
				return nil
			}
			if w.shouldIgnore(event.Name) {
				continue
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(w.opts.Debounce, func() {
					w.triggerScan(ctx)
				})
			}
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "watch: error: %v\n", err)
		}
	}
}

// Stop signals the watcher to stop.
func (w *Watcher) Stop() {
	close(w.stop)
}

func (w *Watcher) triggerScan(ctx context.Context) {
	w.mu.Lock()
	if w.pending {
		w.mu.Unlock()
		return
	}
	w.pending = true
	w.mu.Unlock()

	defer func() {
		w.mu.Lock()
		w.pending = false
		w.mu.Unlock()
	}()

	scanner, err := NewScanner(w.root, w.db, w.cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "watch: init scanner: %v\n", err)
		return
	}

	result, err := scanner.Scan(ctx, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "watch: scan error: %v\n", err)
		return
	}

	if w.opts.Verbose || result.NodesCreated > 0 || result.NodesUpdated > 0 || result.NodesDeleted > 0 {
		fmt.Printf("[watch] scan: %d created, %d updated, %d deleted (%s)\n",
			result.NodesCreated, result.NodesUpdated, result.NodesDeleted, result.Duration.Round(time.Millisecond))
	}

	if w.opts.OnScan != nil {
		w.opts.OnScan(result)
	}
}

func (w *Watcher) addDirs() error {
	return filepath.WalkDir(w.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip inaccessible dirs.
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		// Skip hidden directories and common ignores.
		if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
			return filepath.SkipDir
		}
		return w.fsw.Add(path)
	})
}

// ShouldIgnore reports whether the watcher should ignore a change to path.
// Exported for testing.
func (w *Watcher) ShouldIgnore(path string) bool {
	return w.shouldIgnore(path)
}

func (w *Watcher) shouldIgnore(path string) bool {
	rel, err := filepath.Rel(w.root, path)
	if err != nil {
		return true
	}
	// Ignore hidden files, .pcke/ directory, node_modules, vendor.
	parts := strings.Split(rel, string(filepath.Separator))
	for _, p := range parts {
		if strings.HasPrefix(p, ".") || p == "node_modules" || p == "vendor" {
			return true
		}
	}
	return false
}

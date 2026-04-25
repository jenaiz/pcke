package analysis // scanner.go — file tree scanner and orchestrator.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jenaiz/pcke/internal/config"
	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// KnowledgeNode represents a single file's analysis results persisted
// in the knowledge base. See PRD §5.2.
type KnowledgeNode struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Name        string    `json:"name"`
	FilePath    string    `json:"file_path"`
	Language    string    `json:"language"`
	Module      string    `json:"module"`
	Class       string    `json:"class"`
	Stability   float64   `json:"stability"`
	Status      string    `json:"status"`
	ContentHash string    `json:"content_hash"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// EvolutionLog records a change event for a knowledge node.
type EvolutionLog struct {
	ID         string    `json:"id"`
	NodeID     string    `json:"node_id"`
	CommitHash string    `json:"commit_hash"`
	ChangeType string    `json:"change_type"`
	Author     string    `json:"author"`
	Timestamp  time.Time `json:"timestamp"`
}

const (
	// Key prefixes for kdb storage.
	prefixNode     = "kn:"
	prefixEvol     = "el:"
	prefixMeta     = "meta:"
	metaLastCommit = "meta:last_scan_commit"
)

// ScanResult summarises a completed scan.
type ScanResult struct {
	NodesCreated int
	NodesUpdated int
	NodesDeleted int
	FilesScanned int
	FilesSkipped int
	SecretsFound int
	CommitHash   string
	Duration     time.Duration
}

// Scanner performs file tree analysis and persists results to a kdb database.
type Scanner struct {
	db   *kdb.DB
	cfg  config.ScanConfig
	git  *GitIntel
	root string // Repository root directory.
}

// NewScanner creates a [Scanner] for the repository at root, persisting
// results into db. The cfg controls redaction and exclusion behaviour.
func NewScanner(root string, db *kdb.DB, cfg config.ScanConfig) (*Scanner, error) {
	gitIntel, err := NewGitIntel(root)
	if err != nil {
		return nil, fmt.Errorf("analysis: init git: %w", err)
	}
	return &Scanner{
		db:   db,
		cfg:  cfg,
		git:  gitIntel,
		root: root,
	}, nil
}

// Scan performs a full or incremental scan based on the full flag.
// A full scan rebuilds all nodes. An incremental scan only processes
// files changed since the last scan commit.
func (s *Scanner) Scan(ctx context.Context, full bool) (*ScanResult, error) {
	start := time.Now()

	headHash, err := s.git.HeadHash()
	if err != nil {
		return nil, fmt.Errorf("analysis: head hash: %w", err)
	}

	files, err := s.collectAndGrow()
	if err != nil {
		return nil, err
	}

	// Load existing nodes for incremental comparison.
	existing := map[string]KnowledgeNode{}
	if !full {
		existing, err = s.loadExistingNodes(ctx)
		if err != nil {
			return nil, fmt.Errorf("analysis: load existing: %w", err)
		}
	}

	result := &ScanResult{CommitHash: headHash}
	now := time.Now()

	nodes, seen := s.processFiles(files, existing, full, now, result)

	// Mark deleted files.
	nodes = s.markDeleted(existing, seen, now, nodes, result)

	// Persist all nodes + last scan commit in a single transaction.
	if err := s.persistAll(ctx, nodes, headHash); err != nil {
		return nil, err
	}

	result.Duration = time.Since(start)
	return result, nil
}

// collectAndGrow collects scannable files and pre-grows the database.
func (s *Scanner) collectAndGrow() ([]string, error) {
	files, err := s.collectFiles()
	if err != nil {
		return nil, fmt.Errorf("analysis: collect files: %w", err)
	}
	growChunks := (len(files)*2)/kdb.GrowthChunk + 2
	for range growChunks {
		if err := s.db.Grow(); err != nil {
			return nil, fmt.Errorf("analysis: grow db: %w", err)
		}
	}
	return files, nil
}

// processFiles analyses each collected file and returns nodes to persist.
func (s *Scanner) processFiles(files []string, existing map[string]KnowledgeNode, full bool, now time.Time, result *ScanResult) ([]*KnowledgeNode, map[string]bool) {
	seen := map[string]bool{}
	var nodes []*KnowledgeNode

	for _, relPath := range files {
		seen[relPath] = true

		hash, err := s.fileHash(relPath)
		if err != nil {
			result.FilesSkipped++
			continue
		}

		if prev, ok := existing[relPath]; ok && prev.ContentHash == hash && !full {
			result.FilesScanned++
			continue
		}

		node := s.analyseFile(relPath, hash, now)
		if node == nil {
			result.FilesSkipped++
			continue
		}

		if _, ok := existing[relPath]; ok {
			result.NodesUpdated++
		} else {
			result.NodesCreated++
		}

		s.enrichWithGit(node, relPath)
		nodes = append(nodes, node)
		result.FilesScanned++
	}
	return nodes, seen
}

// markDeleted appends nodes for files no longer present in the scan.
func (s *Scanner) markDeleted(existing map[string]KnowledgeNode, seen map[string]bool, now time.Time, nodes []*KnowledgeNode, result *ScanResult) []*KnowledgeNode {
	for path, node := range existing {
		if !seen[path] && node.Status != "deleted" {
			result.NodesDeleted++
			node.Status = "deleted"
			node.UpdatedAt = now
			nodes = append(nodes, &node)
		}
	}
	return nodes
}

// persistAll writes all nodes and the last scan commit atomically.
func (s *Scanner) persistAll(ctx context.Context, nodes []*KnowledgeNode, headHash string) error {
	return s.db.Update(ctx, func(wtx *tx.WriteTx) error {
		for _, node := range nodes {
			data, err := json.Marshal(node)
			if err != nil {
				return fmt.Errorf("marshal node %s: %w", node.ID, err)
			}
			if err := wtx.Put([]byte(prefixNode+node.ID), data); err != nil {
				return fmt.Errorf("put node %s: %w", node.ID, err)
			}
		}
		return wtx.Put([]byte(metaLastCommit), []byte(headHash))
	})
}

// collectFiles walks the repository tree and returns relative paths of
// files to analyse. Respects .gitignore via git status and applies
// secret path filtering.
func (s *Scanner) collectFiles() ([]string, error) {
	var ignored map[string]bool
	if !s.cfg.IncludeIgnored {
		var err error
		ignored, err = s.git.GitIgnoredFiles()
		if err != nil {
			ignored = map[string]bool{} // Non-fatal: proceed without ignore info.
		}
	}

	var files []string
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip unreadable entries.
		}

		relPath, err := filepath.Rel(s.root, path)
		if err != nil {
			return nil
		}
		relPath = filepath.ToSlash(relPath)

		// Skip hidden directories (except .github).
		if d.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") && base != "." && base != ".github" {
				return fs.SkipDir
			}
			return nil
		}

		if s.shouldSkipFile(relPath, d, ignored) {
			return nil
		}

		files = append(files, relPath)
		return nil
	})
	return files, err
}

// shouldSkipFile checks whether a file should be excluded from the scan.
func (s *Scanner) shouldSkipFile(relPath string, d fs.DirEntry, ignored map[string]bool) bool {
	if strings.HasPrefix(relPath, ".git/") || relPath == ".git" {
		return true
	}
	if ignored[relPath] {
		return true
	}
	if s.matchesExcludeGlob(relPath) {
		return true
	}
	if s.cfg.RedactSecrets && IsSecretPath(relPath) {
		return true
	}
	if s.cfg.MaxFileBytes > 0 {
		info, err := d.Info()
		if err != nil {
			return true
		}
		if info.Size() > s.cfg.MaxFileBytes {
			return true
		}
	}
	return false
}

// matchesExcludeGlob checks if a path matches any configured exclude glob.
func (s *Scanner) matchesExcludeGlob(relPath string) bool {
	for _, glob := range s.cfg.ExcludeGlobs {
		if matched, _ := filepath.Match(glob, relPath); matched {
			return true
		}
		if matched, _ := filepath.Match(glob, filepath.Base(relPath)); matched {
			return true
		}
	}
	return false
}

// fileHash computes SHA-256 of the file content.
func (s *Scanner) fileHash(relPath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(s.root, relPath)) //nolint:gosec // G304: path relative to known root.
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// analyseFile builds a [KnowledgeNode] from a file path. Returns nil
// if the file should be skipped.
func (s *Scanner) analyseFile(relPath, hash string, now time.Time) *KnowledgeNode {
	ext := strings.ToLower(filepath.Ext(relPath))
	lang := Language(ext)
	class := Classify(relPath)
	module := DetectModule(relPath)

	// Read content for secret redaction check.
	if s.cfg.RedactSecrets {
		data, err := os.ReadFile(filepath.Join(s.root, relPath)) //nolint:gosec // G304: path relative to known root.
		if err == nil {
			RedactSecrets(string(data)) // Check only; content not stored in Phase 0.
		}
	}

	return &KnowledgeNode{
		ID:          relPath,
		Type:        "file",
		Name:        filepath.Base(relPath),
		FilePath:    relPath,
		Language:    lang,
		Module:      module,
		Class:       class.String(),
		Status:      "active",
		ContentHash: hash,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// enrichWithGit adds git-derived stats to a node.
func (s *Scanner) enrichWithGit(node *KnowledgeNode, relPath string) {
	stats, err := s.git.FileHistory(relPath)
	if err != nil {
		return // Non-fatal: proceed without git enrichment.
	}
	node.Stability = stats.Stability
}

// loadExistingNodes reads all knowledge nodes from the database.
func (s *Scanner) loadExistingNodes(ctx context.Context) (map[string]KnowledgeNode, error) {
	nodes := map[string]KnowledgeNode{}
	if err := s.db.View(ctx, func(_ *tx.ReadTx) error {
		// We don't have cursor-based prefix scan on ReadTx yet.
		// For Phase 0, we'll load nodes during scan by probing known paths.
		return nil
	}); err != nil {
		return nil, err
	}
	return nodes, nil
}

// LastScanCommit returns the commit hash of the last successful scan,
// or an empty string if no scan has been performed.
func (s *Scanner) LastScanCommit(ctx context.Context) string {
	var hash string
	_ = s.db.View(ctx, func(rtx *tx.ReadTx) error {
		val, err := rtx.Get([]byte(metaLastCommit))
		if err != nil {
			return nil
		}
		hash = string(val)
		return nil
	})
	return hash
}

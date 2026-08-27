package analysis // scanner.go — file tree scanner and orchestrator.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jenaiz/pcke/internal/analysis/ast"
	"github.com/jenaiz/pcke/internal/analysis/decisions"
	"github.com/jenaiz/pcke/internal/config"
	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/index"
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
	Source      string    `json:"source,omitempty"`
	Stability   float64   `json:"stability"`
	Status      string    `json:"status"`
	ContentHash string    `json:"content_hash"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// Deep analysis fields (populated when --deep is used).
	Entities []ast.Entity `json:"entities,omitempty"`
	Imports  []ast.Import `json:"imports,omitempty"`
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

// Relation records a directed edge between two knowledge nodes.
// See PRD §5.2 — collection: relations.
type Relation struct {
	ID           string    `json:"id"`
	SourceNodeID string    `json:"source_node_id"`
	TargetNodeID string    `json:"target_node_id"`
	Type         string    `json:"type"`
	Source       string    `json:"source"`
	CreatedAt    time.Time `json:"created_at"`
}

const (
	// Key prefixes for kdb storage.
	prefixNode     = "kn:"
	prefixEvol     = "el:"
	prefixRel      = "rel:"
	prefixMeta     = "meta:"
	metaLastCommit = "meta:last_scan_commit"
	metaScanBranch = "meta:scan_branch"
)

const (
	// relationBatchSize bounds how many relation records are written per
	// transaction so per-tx copy-on-write churn stays bounded on large repos.
	relationBatchSize = 1000
	// pagesPerRelation is the freelist headroom reserved per relation
	// record. Each relation now persists both the legacy rel: record and
	// an event-log link (l: forward + lr: reverse index), so the
	// reservation covers all three records plus B+tree split / CoW churn.
	pagesPerRelation = 8
	// pagesPerDeepNode is the freelist headroom reserved per knowledge
	// node when deep analysis embeds AST entities and imports, which makes
	// each node payload substantially larger than a shallow scan.
	pagesPerDeepNode = 8
	// entityEventBatchSize bounds how many e: entity events are written
	// per transaction so per-tx copy-on-write churn stays bounded.
	entityEventBatchSize = 1000
	// pagesPerEntityEvent is the freelist headroom reserved per e: entity
	// event (the record plus B+tree split / CoW churn).
	pagesPerEntityEvent = 4
)

// ScanResult summarises a completed scan.
type ScanResult struct {
	NodesCreated      int
	NodesUpdated      int
	NodesDeleted      int
	FilesScanned      int
	FilesSkipped      int
	SecretsFound      int
	EntitiesExtracted int
	RelationsCreated  int
	CommitHash        string
	Duration          time.Duration

	// Decisions records how many Decision events the scan-time backfill
	// wrote per source. Empty when the backfill was skipped (e.g. on a
	// repo with no docs/adr, no @pcke-rule annotations, and no
	// matching commit messages).
	Decisions decisions.Result
}

// CheckBranchMismatch reads the stored scan branch from the knowledge base
// and compares it against the current HEAD branch. Returns a non-empty
// warning message if they differ, or "" if they match (or if no prior scan
// exists).
func CheckBranchMismatch(ctx context.Context, db *kdb.DB, root string) string {
	git, err := NewGitIntel(root)
	if err != nil {
		return ""
	}

	currentBranch := git.CurrentBranch()
	if currentBranch == "" {
		return "" // detached HEAD — skip check
	}

	var storedBranch string
	_ = db.View(ctx, func(rtx *tx.ReadTx) error {
		val, err := rtx.Get([]byte(metaScanBranch))
		if err != nil {
			return err
		}
		storedBranch = string(val)
		return nil
	})

	if storedBranch == "" || storedBranch == currentBranch {
		return ""
	}

	return fmt.Sprintf("warning: knowledge base was built on branch %q but current branch is %q; consider running 'pcke scan'",
		storedBranch, currentBranch)
}

// Scanner performs file tree analysis and persists results to a kdb database.
type Scanner struct {
	db   *kdb.DB
	cfg  config.ScanConfig
	git  *GitIntel
	root string // Repository root directory.
	deep bool   // Enable AST-based deep analysis.
	astP *ast.Parser

	// historyCache holds per-file git stats for the current scan.
	// Populated once at the start of Scan via AllFileHistory; consulted
	// per file by enrichWithGit. Nil between scans.
	historyCache map[string]FileStats
}

// NewScanner creates a [Scanner] for the repository at root, persisting
// results into db. The cfg controls redaction and exclusion behaviour.
// Use [WithDeep] to enable AST-based entity extraction.
func NewScanner(root string, db *kdb.DB, cfg config.ScanConfig, opts ...ScanOption) (*Scanner, error) {
	gitIntel, err := NewGitIntel(root)
	if err != nil {
		return nil, fmt.Errorf("analysis: init git: %w", err)
	}
	s := &Scanner{
		db:   db,
		cfg:  cfg,
		git:  gitIntel,
		root: root,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.deep {
		s.astP = ast.NewParser()
	}
	return s, nil
}

// ScanOption configures optional Scanner behaviour.
type ScanOption func(*Scanner)

// WithDeep enables AST-based deep analysis (tree-sitter entity extraction).
func WithDeep() ScanOption {
	return func(s *Scanner) { s.deep = true }
}

// Scan performs a full or incremental scan based on the full flag.
// A full scan rebuilds all nodes. An incremental scan only processes
// files changed since the last scan commit.
func (s *Scanner) Scan(ctx context.Context, full bool) (*ScanResult, error) {
	start := time.Now()

	if s.astP != nil {
		defer s.astP.Close()
	}

	// Build the per-scan history cache once, up front. enrichWithGit
	// consults it per file in O(1) instead of walking the commit DAG
	// per file (which is O(all-commits) under go-git's --follow path).
	if s.git != nil {
		if cache, err := s.git.AllFileHistory(); err == nil {
			s.historyCache = cache
		}
	}
	defer func() { s.historyCache = nil }()

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

	nodes, seen := s.processFiles(ctx, files, existing, full, now, result)

	// Mark deleted files.
	nodes = s.markDeleted(existing, seen, now, nodes, result)

	// Persist all nodes + last scan commit in a single transaction.
	if err := s.persistAll(ctx, nodes, headHash, existing); err != nil {
		return nil, err
	}

	// Populate the typed-event graph (e: entities always; l: import
	// links in deep mode) so graph/context/recall work natively after a
	// scan, without a manual `pcke migrate`.
	if err := s.persistGraph(ctx, nodes, result); err != nil {
		return nil, err
	}

	// Detect renames and persist evolution logs.
	lastCommit := s.getLastCommit(ctx)
	if renames, err := s.git.DetectRenames(lastCommit); err == nil && len(renames) > 0 {
		if err := s.persistRenames(ctx, renames); err != nil {
			return nil, fmt.Errorf("analysis: persist renames: %w", err)
		}
	}

	// Decision backfill: harvest ADRs, @pcke-rule annotations, doc
	// headings, and commit-message decision markers into the typed-event
	// log so graph queries return populated d: records on day one.
	// Backfill failures are non-fatal: the scan succeeded, the
	// backfill is best-effort enrichment.
	if r, err := decisions.BackfillAll(ctx, s.db, s.root, s.git, files); err != nil {
		// Surface partial counts even on error so the result is honest.
		result.Decisions = r
	} else {
		result.Decisions = r
	}

	result.Duration = time.Since(start)
	return result, nil
}

// getLastCommit reads the stored last scan commit hash from the KB.
func (s *Scanner) getLastCommit(ctx context.Context) string {
	var hash string
	_ = s.db.View(ctx, func(rtx *tx.ReadTx) error {
		val, err := rtx.Get([]byte(metaLastCommit))
		if err != nil {
			return err
		}
		hash = string(val)
		return nil
	})
	return hash
}

// persistRenames stores rename evolution log entries.
func (s *Scanner) persistRenames(ctx context.Context, renames []RenameEntry) error {
	return s.db.Update(ctx, func(wtx *tx.WriteTx) error {
		for _, r := range renames {
			entry := EvolutionLog{
				ID:         fmt.Sprintf("%s:%s→%s", r.CommitHash[:8], r.OldPath, r.NewPath),
				NodeID:     r.NewPath,
				CommitHash: r.CommitHash,
				ChangeType: "renamed",
				Author:     r.Author,
				Timestamp:  r.Timestamp,
			}
			data, err := json.Marshal(entry)
			if err != nil {
				return fmt.Errorf("marshal rename %s: %w", entry.ID, err)
			}
			if err := wtx.Put([]byte(prefixEvol+entry.ID), data); err != nil {
				return fmt.Errorf("put rename %s: %w", entry.ID, err)
			}
		}
		return nil
	})
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
func (s *Scanner) processFiles(ctx context.Context, files []string, existing map[string]KnowledgeNode, full bool, now time.Time, result *ScanResult) ([]*KnowledgeNode, map[string]bool) {
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

		if s.deep {
			s.deepAnalyse(ctx, node, relPath, result)
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
// It also updates all four secondary indexes (by_module, by_tag, by_file, by_type).
func (s *Scanner) persistAll(ctx context.Context, nodes []*KnowledgeNode, headHash string, existing map[string]KnowledgeNode) error {
	// Deep-scanned nodes embed AST entities and imports, making each
	// payload much larger than a shallow node; reserve extra freelist
	// headroom so the single write transaction does not run dry.
	if s.deep {
		if err := s.db.EnsureFreePages(len(nodes) * pagesPerDeepNode); err != nil {
			return fmt.Errorf("analysis: grow for deep nodes: %w", err)
		}
	}

	branch := s.git.CurrentBranch()
	return s.db.Update(ctx, func(wtx *tx.WriteTx) error {
		for _, node := range nodes {
			pk := []byte(prefixNode + node.ID)

			// Compute old index keys from the previous version (if any).
			var oldModKeys, oldFileKeys, oldTypeKeys [][]byte
			if prev, ok := existing[node.ID]; ok {
				oldModKeys = index.ModuleKeys(prev.Module)
				oldFileKeys = index.FileKeys(prev.FilePath)
				oldTypeKeys = index.TypeKeys(prev.Type)
			}

			// Compute new index keys.
			newModKeys := index.ModuleKeys(node.Module)
			newFileKeys := index.FileKeys(node.FilePath)
			newTypeKeys := index.TypeKeys(node.Type)

			// Update secondary indexes (handles insert + update + soft-delete).
			if err := s.db.ModuleIndex().Update(pk, oldModKeys, newModKeys); err != nil {
				return fmt.Errorf("index module %s: %w", node.ID, err)
			}
			if err := s.db.FileIndex().Update(pk, oldFileKeys, newFileKeys); err != nil {
				return fmt.Errorf("index file %s: %w", node.ID, err)
			}
			if err := s.db.TypeIndex().Update(pk, oldTypeKeys, newTypeKeys); err != nil {
				return fmt.Errorf("index type %s: %w", node.ID, err)
			}

			// Marshal and persist the node.
			data, err := json.Marshal(node)
			if err != nil {
				return fmt.Errorf("marshal node %s: %w", node.ID, err)
			}
			if err := wtx.Put(pk, data); err != nil {
				return fmt.Errorf("put node %s: %w", node.ID, err)
			}
		}
		if err := wtx.Put([]byte(metaLastCommit), []byte(headHash)); err != nil {
			return err
		}
		if branch != "" {
			return wtx.Put([]byte(metaScanBranch), []byte(branch))
		}
		return nil
	})
}

// persistGraph populates the typed-event graph from the scanned nodes:
// e: entity events for every node, plus l: import links in deep mode.
// Keeping both steps behind one method keeps the Scan orchestrator flat.
func (s *Scanner) persistGraph(ctx context.Context, nodes []*KnowledgeNode, result *ScanResult) error {
	if err := s.persistEntityEvents(ctx, nodes); err != nil {
		return fmt.Errorf("analysis: persist entity events: %w", err)
	}
	if s.deep {
		if err := s.persistRelations(ctx, nodes, result); err != nil {
			return fmt.Errorf("analysis: persist relations: %w", err)
		}
	}
	return nil
}

// persistEntityEvents writes one e: entity event per knowledge node into
// the typed-event log, which is the query surface read by graph/context/
// recall. Writing these natively during the scan means a fresh
// `pcke scan` populates the event-log graph without a manual `pcke
// migrate`; the legacy kn:->e: migration remains only to upgrade
// databases written by older versions.
//
// A new entity version is appended only when a file is new or its
// content changed since the last recorded version (content-hash diff
// against the latest stored entity), so the version chain tracks real
// changes and a repeated --full scan does not churn versions.
func (s *Scanner) persistEntityEvents(ctx context.Context, nodes []*KnowledgeNode) error {
	store := event.New(s.db)
	for start := 0; start < len(nodes); start += entityEventBatchSize {
		end := min(start+entityEventBatchSize, len(nodes))
		batch := nodes[start:end]

		if err := s.db.EnsureFreePages(len(batch) * pagesPerEntityEvent); err != nil {
			return fmt.Errorf("grow for entity events: %w", err)
		}

		if err := s.db.Update(ctx, func(wtx *tx.WriteTx) error {
			for _, node := range batch {
				if err := appendEntityVersion(wtx, store, node); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// appendEntityVersion appends a new e: entity version for node when the
// file is new or its content changed since the latest stored version.
// Idempotency is self-contained: the content hash is stored on the
// entity, so an unchanged file (e.g. re-emitted by a --full scan) is
// skipped regardless of whether the scan loaded prior kn: state. The
// event store auto-increments the version and links the supersedes
// pointer to the prior version; CreatedAt is stamped with the node's
// observation time so each version carries a distinct timeline stamp.
func appendEntityVersion(wtx *tx.WriteTx, store *event.Store, node *KnowledgeNode) error {
	if node.ID == "" {
		return nil
	}
	if latest, err := store.LatestInTx(wtx, event.KindEntity, node.ID); err == nil {
		if ent, ok := latest.(*event.Entity); ok && ent.Hash == node.ContentHash {
			return nil // unchanged since the latest stored version
		}
	} else if !errors.Is(err, event.ErrNotFound) {
		return fmt.Errorf("probe latest e:%s: %w", node.ID, err)
	}

	ent := &event.Entity{
		Hdr:  event.Header{CreatedAt: node.UpdatedAt, Lifecycle: event.LifecycleActive},
		EID:  node.ID,
		Type: node.Type,
		Path: node.FilePath,
		Name: node.Name,
		Hash: node.ContentHash,
	}
	if _, err := store.AppendInTx(wtx, ent); err != nil {
		return fmt.Errorf("append e:%s: %w", node.ID, err)
	}
	return nil
}

// appendLinkIfAbsent writes rel as an l: link event (forward + reverse
// index) unless its v1 record already exists.
func appendLinkIfAbsent(wtx *tx.WriteTx, store *event.Store, rel *Relation) error {
	link := &event.Link{
		Hdr:      event.Header{CreatedAt: rel.CreatedAt, Lifecycle: event.LifecycleActive},
		SrcRef:   "e:" + rel.SourceNodeID,
		EdgeType: rel.Type,
		DstRef:   "e:" + rel.TargetNodeID,
	}
	key, err := event.BuildKey(event.KindLink, link.ID(), 1)
	if err != nil {
		return fmt.Errorf("build key for link %s: %w", link.ID(), err)
	}
	if _, getErr := wtx.Get(key); getErr == nil {
		return nil
	} else if !errors.Is(getErr, btree.ErrKeyNotFound) {
		return fmt.Errorf("probe link %s: %w", link.ID(), getErr)
	}
	if _, err := store.AppendInTx(wtx, link); err != nil {
		return fmt.Errorf("append link %s -> %s: %w", link.SrcRef, link.DstRef, err)
	}
	return nil
}

// persistRelations creates import-graph relations from deep-scanned nodes.
// Each import in a node produces a relation: source=node.ID → target=import.Path.
//
// Relations are written in bounded batches, pre-growing the freelist for
// each batch, because kdb does not auto-grow inside a write transaction
// and a deep scan can produce many thousands of edges. Each relation is
// persisted both as a legacy rel: record and as an event-log l: link so
// the graph query surface is populated natively by the scan.
func (s *Scanner) persistRelations(ctx context.Context, nodes []*KnowledgeNode, result *ScanResult) error {
	now := time.Now()
	store := event.New(s.db)

	rels := make([]Relation, 0, len(nodes))
	for _, node := range nodes {
		for _, imp := range node.Imports {
			rels = append(rels, Relation{
				ID:           node.ID + "→" + imp.Path,
				SourceNodeID: node.ID,
				TargetNodeID: imp.Path,
				Type:         "imports",
				Source:       "auto",
				CreatedAt:    now,
			})
		}
	}

	for start := 0; start < len(rels); start += relationBatchSize {
		end := min(start+relationBatchSize, len(rels))
		batch := rels[start:end]

		if err := s.db.EnsureFreePages(len(batch) * pagesPerRelation); err != nil {
			return fmt.Errorf("grow for relations: %w", err)
		}

		if err := s.db.Update(ctx, func(wtx *tx.WriteTx) error {
			for i := range batch {
				rel := &batch[i]
				data, err := json.Marshal(rel)
				if err != nil {
					return fmt.Errorf("marshal relation %s: %w", rel.ID, err)
				}
				if err := wtx.Put([]byte(prefixRel+rel.ID), data); err != nil {
					return fmt.Errorf("put relation %s: %w", rel.ID, err)
				}
				if err := appendLinkIfAbsent(wtx, store, rel); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		result.RelationsCreated += len(batch)
	}
	return nil
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

// deepAnalyse runs tree-sitter AST parsing on a file and populates
// the node's Entities and Imports fields. Non-fatal on failure.
func (s *Scanner) deepAnalyse(ctx context.Context, node *KnowledgeNode, relPath string, result *ScanResult) {
	ext := strings.ToLower(filepath.Ext(relPath))
	if !ast.IsSupported(ext) {
		return
	}
	absPath := filepath.Join(s.root, relPath)
	parsed, err := s.astP.ParseFile(ctx, absPath, ext)
	if err != nil || parsed == nil {
		return
	}
	node.Entities = parsed.Entities
	node.Imports = parsed.Imports
	result.EntitiesExtracted += len(parsed.Entities)
}

// enrichWithGit adds git-derived stats to a node, looking up from the
// per-scan history cache rather than walking the commit DAG per file.
//
// The cache is populated once at the start of each scan via
// AllFileHistory; missing entries are treated as zero-stats (file was
// not touched within the history window).
func (s *Scanner) enrichWithGit(node *KnowledgeNode, relPath string) {
	if s.historyCache == nil {
		return
	}
	if stats, ok := s.historyCache[relPath]; ok {
		node.Stability = stats.Stability
	}
}

// loadExistingNodes reads all knowledge nodes from the database.
func (s *Scanner) loadExistingNodes(ctx context.Context) (map[string]KnowledgeNode, error) {
	nodes := map[string]KnowledgeNode{}
	prefix := []byte(prefixNode)
	if err := s.db.View(ctx, func(rtx *tx.ReadTx) error {
		cursor := rtx.Cursor()
		if !cursor.Seek(prefix) {
			return nil
		}
		for cursor.Valid() {
			key := cursor.Key()
			if !bytes.HasPrefix(key, prefix) {
				break
			}
			var node KnowledgeNode
			if err := json.Unmarshal(cursor.Value(), &node); err != nil {
				return fmt.Errorf("unmarshal node %q: %w", key, err)
			}
			nodes[node.ID] = node
			cursor.Next()
		}
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

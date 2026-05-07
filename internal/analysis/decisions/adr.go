package decisions

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// adrTitleMaxRunes caps the Decision.Title length pulled from an ADR
// front matter heading. The full ADR body lives in Decision.Body.
const adrTitleMaxRunes = 200

// BackfillFromADRs walks <root>/docs/adr/*.md and writes one Decision
// event per file with severity=must, scope=global, source=adr.
//
// DID derivation: the file's basename without the .md suffix, prefixed
// with "adr:". For example "0008-context-graph-pivot.md" yields the
// id "adr:0008-context-graph-pivot".
//
// Title: first non-empty line of the file with leading '#' markers
// stripped, capped at adrTitleMaxRunes.
//
// Body: the full file contents.
//
// Header.CreatedAt: the file's modification time (best-available proxy
// for "when this decision was recorded"; git history would be richer
// but is out of scope for the file-based scanner).
//
// Idempotent: skips files whose e:<DID>:v1 already exists. New ADR
// files added between scans are written; existing ADRs whose content
// changed do NOT produce a v2 in v0.10.0 (content-change detection is
// follow-up work).
//
// Returns the number of Decisions newly written. Returns nil error if
// the docs/adr directory doesn't exist (a repo without ADRs is fine).
func BackfillFromADRs(ctx context.Context, db UpdateDB, repoRoot string) (int, error) {
	adrDir := filepath.Join(repoRoot, "docs", "adr")
	info, err := os.Stat(adrDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("backfill ADRs: stat %s: %w", adrDir, err)
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("backfill ADRs: %s is not a directory", adrDir)
	}

	files, err := collectADRFiles(adrDir)
	if err != nil {
		return 0, fmt.Errorf("backfill ADRs: %w", err)
	}
	if len(files) == 0 {
		return 0, nil
	}

	store := event.New(db)
	written := 0
	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		for _, f := range files {
			d, err := loadADRDecision(f)
			if err != nil {
				return fmt.Errorf("load %s: %w", f.path, err)
			}
			ok, err := writeDecision(wtx, store, d)
			if err != nil {
				return err
			}
			if ok {
				written++
			}
		}
		return nil
	}); err != nil {
		return written, fmt.Errorf("backfill ADRs: %w", err)
	}
	return written, nil
}

// UpdateDB is the database interface BackfillFromADRs needs. *kdb.DB
// satisfies it. Defined locally rather than imported from migrate to
// keep this package's dependency surface narrow.
type UpdateDB interface {
	View(ctx context.Context, fn func(*tx.ReadTx) error) error
	Update(ctx context.Context, fn func(*tx.WriteTx) error) error
}

// adrFile is one ADR markdown file detected by collectADRFiles.
type adrFile struct {
	path string // absolute path
	base string // basename without ".md" suffix
}

// collectADRFiles returns the *.md entries in dir, sorted by basename
// for deterministic write order. Subdirectories and non-md files are
// skipped. README.md is included if present (it's a real ADR file in
// some repos, e.g. an "ADR index").
func collectADRFiles(dir string) ([]adrFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var files []adrFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		base := strings.TrimSuffix(name, filepath.Ext(name))
		files = append(files, adrFile{
			path: filepath.Join(dir, name),
			base: base,
		})
	}
	// sort by base for deterministic ordering — kdb does not depend on
	// directory iteration order, but tests do.
	sortADRFiles(files)
	return files, nil
}

// loadADRDecision reads the file's content and constructs a Decision.
func loadADRDecision(f adrFile) (*event.Decision, error) {
	body, err := os.ReadFile(f.path) //nolint:gosec // G304: path is constructed from a known directory walk.
	if err != nil {
		return nil, err
	}
	bodyStr := string(body)

	info, err := os.Stat(f.path)
	if err != nil {
		return nil, err
	}
	createdAt := info.ModTime().UTC()

	title := truncateRunes(firstNonEmptyLine(bodyStr), adrTitleMaxRunes)

	return &event.Decision{
		Hdr: event.Header{
			CreatedAt: createdAt,
			Lifecycle: event.LifecycleActive,
		},
		DID:      "adr:" + f.base,
		Title:    title,
		Body:     bodyStr,
		Severity: event.SeverityMust,
		Scope:    event.ScopeGlobal,
		Source:   string(SourceADR),
	}, nil
}

// sortADRFiles sorts files by base name in place. Pulled into a named
// helper so tests can verify ordering without importing sort.
func sortADRFiles(files []adrFile) {
	for i := 1; i < len(files); i++ {
		j := i
		for j > 0 && files[j-1].base > files[j].base {
			files[j-1], files[j] = files[j], files[j-1]
			j--
		}
	}
}

// _ keeps fs in the import set for future use (e.g. fs.WalkDir for
// nested ADR directories). Without this var the linter complains.
var _ fs.DirEntry

// _ keeps time in the import set; loadADRDecision uses it implicitly
// via os.Stat's ModTime() but the linter wants explicit references in
// constants/vars. The truncateRunes call also uses time-derived data
// indirectly via the loaded body.
var _ = time.Now

package decisions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/tx"
)

// docTitleMaxRunes caps the Decision.Title pulled from a doc heading.
// The full section text lives in Decision.Body.
const docTitleMaxRunes = 200

// pagesPerDocLink is the freelist headroom reserved per extra
// decision_link edge created by the module-linking pass (Option A). It
// covers the l: forward + lr: reverse records plus B+tree split/CoW
// churn. Decisions themselves use the larger pagesPerDecision budget.
const pagesPerDocLink = 4

// docDecision is one H2 section harvested from a documentation file,
// paired with the artifacts needed to anchor it in the graph.
type docDecision struct {
	d       *event.Decision
	docFile string // repo-relative slash path of the source doc
	module  string // target module prefix; "" means project-global
}

// BackfillFromDocs walks the top-level <root>/docs/*.md files (the ADR
// subdirectory is handled by BackfillFromADRs) and writes one Decision
// per H2 (## ) section, with source=doc.
//
// Each decision is anchored to its source doc via a decision_link edge
// so touching the doc surfaces its guidance. For docs that map to a
// module (see docModule), the decision is scope=module and is also
// linked to every scanned file under that module prefix — the Option A
// rule that lets an arbitrary code file surface the architecture
// decisions governing its module.
//
// files is the full set of scanned file ids (repo-relative slash paths)
// used for module linking; pass nil to skip the module-link pass.
//
// Idempotent: decisions and links whose v1 record already exists are
// skipped, so re-scans do not churn.
//
// Returns the number of Decisions newly written. A repo without a docs
// directory is fine (returns 0, nil).
func BackfillFromDocs(ctx context.Context, db UpdateDB, repoRoot string, files []string) (int, error) {
	decisions, err := collectDocDecisions(repoRoot)
	if err != nil {
		return 0, err
	}
	if len(decisions) == 0 {
		return 0, nil
	}

	moduleDIDs := groupModuleDIDs(decisions)

	extraLinks := countModuleLinks(files, moduleDIDs)
	pages := len(decisions)*pagesPerDecision + extraLinks*pagesPerDocLink
	if err := ensureFreePages(db, pages); err != nil {
		return 0, fmt.Errorf("backfill docs: grow: %w", err)
	}

	store := event.New(db)
	var written int
	if err := db.Update(ctx, func(wtx *tx.WriteTx) error {
		n, err := writeDocDecisions(wtx, store, decisions, files, moduleDIDs)
		written = n
		return err
	}); err != nil {
		return written, fmt.Errorf("backfill docs: %w", err)
	}
	return written, nil
}

// collectDocDecisions reads every top-level docs/*.md file and returns
// the H2-section decisions in deterministic (DID-sorted) order.
func collectDocDecisions(repoRoot string) ([]docDecision, error) {
	docsDir := filepath.Join(repoRoot, "docs")
	entries, err := os.ReadDir(docsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("backfill docs: read %s: %w", docsDir, err)
	}

	var out []docDecision
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		ds, err := loadDocDecisions(filepath.Join(docsDir, e.Name()), base)
		if err != nil {
			return nil, fmt.Errorf("backfill docs: %w", err)
		}
		out = append(out, ds...)
	}
	sortDocDecisions(out)
	return out, nil
}

// writeDocDecisions writes each doc decision plus its doc-file link, then
// applies the Option A module-link pass. Returns the count of decisions
// newly written.
func writeDocDecisions(wtx *tx.WriteTx, store *event.Store, decisions []docDecision, files []string, moduleDIDs map[string][]string) (int, error) {
	var written int
	for _, dd := range decisions {
		ok, err := writeDecision(wtx, store, dd.d)
		if err != nil {
			return written, err
		}
		if ok {
			written++
		}
		if _, err := writeDecisionLink(wtx, store, dd.docFile, dd.d.DID); err != nil {
			return written, err
		}
	}

	// Option A: anchor module-scoped doc decisions to every file under
	// the module prefix so code files surface their governing decisions.
	for _, f := range files {
		for module, dids := range moduleDIDs {
			if !inModule(f, module) {
				continue
			}
			for _, did := range dids {
				if _, err := writeDecisionLink(wtx, store, f, did); err != nil {
					return written, err
				}
			}
		}
	}
	return written, nil
}

// loadDocDecisions parses one markdown file into per-H2-section
// decisions. base is the filename without its extension.
func loadDocDecisions(path, base string) ([]docDecision, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path is from a known docs directory walk.
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	createdAt := info.ModTime().UTC()

	module, _ := docModule(base)
	scope := event.ScopeGlobal
	if module != "" {
		scope = event.ScopeModule
	}
	docFile := "docs/" + base + ".md"

	var out []docDecision
	for _, sec := range splitH2Sections(string(raw)) {
		slug := slugify(sec.heading)
		if slug == "" {
			continue
		}
		out = append(out, docDecision{
			d: &event.Decision{
				Hdr:      event.Header{CreatedAt: createdAt, Lifecycle: event.LifecycleActive},
				DID:      "doc:" + base + ":" + slug,
				Title:    truncateRunes(sec.heading, docTitleMaxRunes),
				Body:     sec.body,
				Severity: event.SeverityShould,
				Scope:    scope,
				Source:   string(SourceDoc),
			},
			docFile: docFile,
			module:  module,
		})
	}
	return out, nil
}

// h2Section is a single "## " section: its heading text and full body
// (heading line included).
type h2Section struct {
	heading string
	body    string
}

// splitH2Sections partitions markdown content into H2 sections. Text
// before the first H2 (and any H1 title) is ignored; each section runs
// from its "## " heading to the next H2 or end of file.
func splitH2Sections(content string) []h2Section {
	var sections []h2Section
	var heading string
	var buf []string
	flush := func() {
		if heading == "" {
			return
		}
		sections = append(sections, h2Section{
			heading: heading,
			body:    strings.TrimSpace(strings.Join(buf, "\n")),
		})
		buf = nil
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "## ") {
			flush()
			heading = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			buf = append(buf, line)
			continue
		}
		if heading != "" {
			buf = append(buf, line)
		}
	}
	flush()
	return sections
}

// docModule maps a documentation file base name to the module prefix it
// primarily governs. Docs without a mapping are treated as project-wide
// guidance (scope=global). The mapping is intentionally explicit: the
// docs set is small and stable, and a wrong inference would attach
// decisions to unrelated code.
func docModule(base string) (string, bool) {
	switch base {
	case "advanced-mcp":
		return "internal/mcp", true
	case "annotations":
		return "internal/analysis/annotations", true
	case "architecture":
		return "internal/kdb", true
	case "federation":
		return "internal/federation", true
	case "graph-guide":
		return "internal/kdb/graph", true
	case "onboarding":
		return "internal/onboard", true
	case "query-language":
		return "internal/query", true
	case "schema-evolution", "schema-migrations":
		return "internal/kdb/migrate", true
	default:
		return "", false
	}
}

// inModule reports whether the file id sits within the module prefix.
func inModule(file, module string) bool {
	return file == module || strings.HasPrefix(file, module+"/")
}

// groupModuleDIDs indexes module-scoped doc decisions by their target
// module prefix.
func groupModuleDIDs(decisions []docDecision) map[string][]string {
	out := map[string][]string{}
	for _, dd := range decisions {
		if dd.module != "" {
			out[dd.module] = append(out[dd.module], dd.d.DID)
		}
	}
	return out
}

// countModuleLinks returns how many module-link edges the Option A pass
// will attempt, used to size freelist pre-growth.
func countModuleLinks(files []string, moduleDIDs map[string][]string) int {
	var n int
	for _, f := range files {
		for module, dids := range moduleDIDs {
			if inModule(f, module) {
				n += len(dids)
			}
		}
	}
	return n
}

// slugify renders a heading into a stable, lowercase, hyphenated id
// fragment safe for a DID. Runs of non-alphanumerics collapse to a
// single hyphen; leading/trailing hyphens are trimmed.
func slugify(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if b.Len() > 0 && !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// sortDocDecisions orders decisions by DID in place for deterministic
// write order (insertion sort keeps the dependency surface narrow,
// mirroring sortADRFiles).
func sortDocDecisions(d []docDecision) {
	for i := 1; i < len(d); i++ {
		j := i
		for j > 0 && d[j-1].d.DID > d[j].d.DID {
			d[j-1], d[j] = d[j], d[j-1]
			j--
		}
	}
}

// _ keeps context imported for symmetry with sibling source files.
var _ = context.Background

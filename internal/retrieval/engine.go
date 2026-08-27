package retrieval

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/kdb/graph"
)

// defaultMaxDepth is the BFS hop count Assemble passes to graph.Reachable
// when the caller doesn't override it. Two hops captures direct
// dependencies and their immediate connections, which is the typical
// "context for an open file" expectation.
const defaultMaxDepth = 2

// Engine assembles ranked, budget-bounded context for a Request by
// traversing the typed-event graph and scoring each reachable record.
//
// Engines are cheap to construct and safe to share across goroutines:
// every Assemble call uses its own kdb View transaction.
type Engine struct {
	db           *kdb.DB
	weights      Weights
	edgePriority []string
	edgeBoost    float64
	now          func() time.Time
}

// Option configures an Engine.
type Option func(*Engine)

// WithWeights overrides the default scoring weights. Sums outside
// [0.95, 1.05] are accepted but flagged as a Warning on every
// returned ContextPackage.
func WithWeights(w Weights) Option {
	return func(e *Engine) { e.weights = w }
}

// WithWorkflow applies the WorkflowProfile for w (PRD v5.2 §6.2
// F15.T2): it sets the engine's scoring weights and the priority-edge
// boost. WorkflowExplore (or an unknown value) yields the neutral
// baseline. WithWorkflow should appear before WithWeights if a caller
// wants to further override the workflow's weights.
func WithWorkflow(w Workflow) Option {
	return func(e *Engine) {
		p := ProfileFor(w)
		e.weights = p.Weights
		e.edgePriority = p.EdgePriority
		e.edgeBoost = p.EdgeBoost
	}
}

// WithClock injects a deterministic time source. Production callers
// leave it unset and the engine uses time.Now.
func WithClock(now func() time.Time) Option {
	return func(e *Engine) { e.now = now }
}

// New constructs an Engine backed by db.
func New(db *kdb.DB, opts ...Option) *Engine {
	e := &Engine{
		db:      db,
		weights: DefaultWeights(),
	}
	for _, opt := range opts {
		opt(e)
	}
	if e.now == nil {
		e.now = func() time.Time { return time.Now().UTC() }
	}
	return e
}

// Assemble builds a ranked, budget-bounded ContextPackage for req.
//
// Algorithm (PRD v5.2 §4.2):
//
//  1. For each file in the request, traverse the graph forward + reverse
//     up to defaultMaxDepth hops. Collect every reachable ref plus the
//     starting refs themselves.
//  2. Resolve each ref via event.Store.Latest. Skip refs whose record
//     is missing or fails to decode (best-effort; surface as a warning).
//  3. Compute Score via the existing Score function with the engine's
//     Weights and now.
//  4. Materialise each event as a Section (Body shaped per event kind).
//  5. Sort sections by descending Score (stable, so equal-score items
//     keep their resolution order — typically alphabetical).
//  6. Greedy-admit via FitToBudget; flag truncation.
//  7. Return ContextPackage.
//
// Empty file set: Assemble returns an empty package with a warning
// rather than scanning the entire event log. Global rankings without
// a focus file are deferred to a future "global mode" (Phase 14+).
func (e *Engine) Assemble(ctx context.Context, req Request) (*ContextPackage, error) {
	pkg := &ContextPackage{
		BudgetLimit: EffectiveBudget(req),
	}

	files := requestFiles(req)
	if len(files) == 0 {
		pkg.Warnings = append(pkg.Warnings,
			"request has no FilePath or ChangedFiles; returning empty package")
		return pkg, nil
	}

	prof := e.effectiveProfile(req)
	if !nearlyOne(prof.Weights.Sum()) {
		pkg.Warnings = append(pkg.Warnings,
			fmt.Sprintf("weights sum to %.3f, not 1.0; scores may exceed [0, 1]", prof.Weights.Sum()))
	}

	refs, err := e.collectCandidateRefs(ctx, files)
	if err != nil {
		return pkg, fmt.Errorf("retrieval: collect candidates: %w", err)
	}
	if len(refs) == 0 {
		pkg.Warnings = append(pkg.Warnings, notIndexedWarning(files))
		return pkg, nil
	}

	sections, err := e.scoreAndShape(ctx, req, prof, refs)
	if err != nil {
		return pkg, fmt.Errorf("retrieval: score: %w", err)
	}
	if len(sections) == 0 {
		// No candidate resolved. If traversal never grew past the focus
		// files themselves, the files simply aren't indexed; otherwise the
		// event log is partially populated (e.g. links point at missing
		// entities after an interrupted or pre-migration scan).
		if len(refs) <= len(files) {
			pkg.Warnings = append(pkg.Warnings, notIndexedWarning(files))
		} else {
			pkg.Warnings = append(pkg.Warnings,
				"all candidates failed to resolve; the event log may be partially populated — "+
					"run 'pcke scan' to rebuild it (add '--deep' for import relations)")
		}
		return pkg, nil
	}

	sort.SliceStable(sections, func(i, j int) bool {
		return sections[i].Score > sections[j].Score
	})

	admitted, truncated := FitToBudget(sections, pkg.BudgetLimit)
	pkg.Sections = admitted
	pkg.Truncated = truncated
	for _, s := range admitted {
		pkg.TokensUsed += s.Tokens
	}
	// Only the focus files' own entities resolved (no graph neighbours):
	// tell the caller how to get richer context instead of leaving them
	// guessing why the neighbourhood is empty.
	if len(refs) <= len(files) {
		pkg.Warnings = append(pkg.Warnings,
			"no linked context found; run 'pcke scan --deep' to extract import relations "+
				"(deep analysis supports Go, Java, JavaScript, and Python)")
	}
	pkg.Anticipated = e.anticipate(ctx, files, admitted)
	return pkg, nil
}

// notIndexedWarning builds the guidance shown when none of the requested
// files resolve to an entity — the typical "you never scanned this path"
// case.
func notIndexedWarning(files []string) string {
	if len(files) == 1 {
		return fmt.Sprintf(
			"file %q is not in the index — run 'pcke scan' so it is picked up "+
				"(add '--deep' for import relations)", files[0])
	}
	return "none of the requested files are in the index — run 'pcke scan' so they are " +
		"picked up (add '--deep' for import relations)"
}

// anticipate returns the refs of the focus files' direct (1-hop)
// neighbours that are not already admitted as sections — the
// anticipatory pre-load (PRD v5.2 §6.2 F15.T4). The result is a
// deterministic, lexically-sorted ref list carrying no bodies, so it
// costs no budget.
//
// Best-effort: a traversal error for any single file is skipped rather
// than failing assembly, since anticipation is an optimisation.
func (e *Engine) anticipate(ctx context.Context, files []string, admitted []Section) []string {
	if len(files) == 0 {
		return nil
	}
	served := make(map[string]struct{}, len(admitted)+len(files))
	for _, s := range admitted {
		served[s.Ref] = struct{}{}
	}
	// The focus files themselves are "already in context"; never
	// anticipate them.
	for _, f := range files {
		served["e:"+f] = struct{}{}
	}

	opts := graph.TraversalOptions{Direction: graph.Both, MaxDepth: 1}
	seen := make(map[string]struct{})
	var out []string
	for _, f := range files {
		neighbours, err := graph.Neighbors(ctx, e.db, graph.Ref("e:"+f), opts)
		if err != nil {
			continue
		}
		for _, n := range neighbours {
			ref := string(n)
			if _, dup := served[ref]; dup {
				continue
			}
			if _, dup := seen[ref]; dup {
				continue
			}
			seen[ref] = struct{}{}
			out = append(out, ref)
		}
	}
	sort.Strings(out)
	return out
}

// collectCandidateRefs traverses the graph from each file in both
// directions, returning the union of reachable refs plus the starting
// refs themselves (so the file's own Entity record is included).
func (e *Engine) collectCandidateRefs(ctx context.Context, files []string) ([]graph.Ref, error) {
	seen := make(map[graph.Ref]struct{})
	var ordered []graph.Ref

	add := func(r graph.Ref) {
		if r == "" {
			return
		}
		if _, dup := seen[r]; dup {
			return
		}
		seen[r] = struct{}{}
		ordered = append(ordered, r)
	}

	opts := graph.TraversalOptions{
		Direction: graph.Both,
		MaxDepth:  defaultMaxDepth,
	}
	for _, f := range files {
		start := graph.Ref("e:" + f)
		add(start)
		reach, err := graph.Reachable(ctx, e.db, start, opts)
		if err != nil && !errors.Is(err, graph.ErrVisitedCapExceeded) {
			return nil, err
		}
		for _, r := range reach {
			add(r)
		}
	}
	return ordered, nil
}

// scoreAndShape resolves each candidate ref via event.Store.Latest,
// scores it, and shapes a Section. Refs that fail to resolve are
// silently skipped so a partially-populated event log doesn't block
// retrieval; callers can detect emptiness via len(Sections) == 0.
func (e *Engine) scoreAndShape(ctx context.Context, req Request, prof WorkflowProfile, refs []graph.Ref) ([]Section, error) {
	store := event.New(e.db)
	now := e.now()
	priority := e.priorityRefs(ctx, prof, requestFiles(req))
	sections := make([]Section, 0, len(refs))

	for _, ref := range refs {
		kind, id, ok := splitRef(string(ref))
		if !ok {
			continue
		}
		evt, err := store.Latest(ctx, kind, id)
		if err != nil {
			if errors.Is(err, event.ErrNotFound) {
				continue
			}
			return nil, fmt.Errorf("latest %q: %w", ref, err)
		}
		score := Score(now, req, evt, prof.Weights)
		if _, boosted := priority[ref]; boosted {
			score = clampScore(score + prof.EdgeBoost)
		}
		s := shapeSection(evt, score)
		sections = append(sections, s)
	}
	return sections, nil
}

// effectiveProfile resolves which WorkflowProfile governs a request.
// A non-empty req.Workflow takes precedence (per-call override, used by
// the MCP tools and `pcke context`); otherwise the engine's
// construction-time configuration (WithWorkflow / WithWeights) applies.
func (e *Engine) effectiveProfile(req Request) WorkflowProfile {
	if req.Workflow != "" {
		return ProfileFor(req.Workflow)
	}
	return WorkflowProfile{
		Workflow:     WorkflowExplore,
		Weights:      e.weights,
		EdgePriority: e.edgePriority,
		EdgeBoost:    e.edgeBoost,
	}
}

// priorityRefs returns the set of refs that are 1-hop neighbours of any
// request file via one of the profile's priority edge types. The
// returned refs receive an edge boost in scoring.
//
// Best-effort: traversal errors for an individual edge type or file are
// skipped rather than failing the whole assembly, since the boost is an
// optimisation, not a correctness requirement.
func (e *Engine) priorityRefs(ctx context.Context, prof WorkflowProfile, files []string) map[graph.Ref]struct{} {
	if len(prof.EdgePriority) == 0 || prof.EdgeBoost == 0 || len(files) == 0 {
		return nil
	}
	out := make(map[graph.Ref]struct{})
	opts := graph.TraversalOptions{
		Direction: graph.Forward,
		MaxDepth:  1,
		EdgeTypes: prof.EdgePriority,
	}
	for _, f := range files {
		neighbours, err := graph.Neighbors(ctx, e.db, graph.Ref("e:"+f), opts)
		if err != nil {
			continue
		}
		for _, n := range neighbours {
			out[n] = struct{}{}
		}
	}
	return out
}

// clampScore bounds s to [0, 1] so an edge boost can't push a section
// above the engine's documented score range.
func clampScore(s float64) float64 {
	switch {
	case s < 0:
		return 0
	case s > 1:
		return 1
	default:
		return s
	}
}

// shapeSection turns one event + its score into a Section, computing
// the Body string shape per event kind (so budgeting reflects how
// the agent will actually see the content).
func shapeSection(evt event.Event, score float64) Section {
	hdr := evt.Header()
	body := bodyForEvent(evt)
	return Section{
		Ref:       refForEvent(evt),
		Kind:      evt.Kind().String(),
		Title:     titleForEvent(evt),
		Body:      body,
		Score:     score,
		Tokens:    TokensFor(body),
		CreatedAt: hdr.CreatedAt,
	}
}

// titleForEvent returns the one-line label rendered above each
// section in the agent's context.
func titleForEvent(evt event.Event) string {
	switch v := evt.(type) {
	case *event.Entity:
		if v.Path != "" {
			return v.Path
		}
		return v.EID
	case *event.Decision:
		if v.Title != "" {
			return v.Title
		}
		return v.DID
	case *event.Link:
		return fmt.Sprintf("%s --%s--> %s", v.SrcRef, v.EdgeType, v.DstRef)
	default:
		return string(refForEvent(evt))
	}
}

// bodyForEvent returns the section payload that counts against the
// budget. For entities we keep it tight (path + type/name); for
// decisions we ship the full body since that's where the rationale
// the agent needs lives.
func bodyForEvent(evt event.Event) string {
	switch v := evt.(type) {
	case *event.Entity:
		if v.Type == "" && v.Name == "" {
			return v.Path
		}
		return fmt.Sprintf("%s: %s %s", v.Path, v.Type, v.Name)
	case *event.Decision:
		if v.Body != "" {
			return v.Body
		}
		return v.Title
	case *event.Link:
		return fmt.Sprintf("%s --%s--> %s", v.SrcRef, v.EdgeType, v.DstRef)
	default:
		return ""
	}
}

// splitRef parses "<prefix>:<id>" into (Kind, id). Returns ok=false for
// unknown prefixes. Mirrors cmd/pcke.parseTypedRef but local so the
// retrieval package doesn't depend on cmd/pcke.
func splitRef(ref string) (event.Kind, string, bool) {
	if len(ref) < 3 {
		return 0, "", false
	}
	switch ref[:2] {
	case "e:":
		return event.KindEntity, ref[2:], true
	case "d:":
		return event.KindDecision, ref[2:], true
	case "l:":
		return event.KindLink, ref[2:], true
	case "o:":
		return event.KindObservation, ref[2:], true
	case "x:":
		return event.KindOutcome, ref[2:], true
	default:
		return 0, "", false
	}
}

// nearlyOne reports whether x is within ±0.05 of 1.0.
func nearlyOne(x float64) bool {
	return x >= 0.95 && x <= 1.05
}

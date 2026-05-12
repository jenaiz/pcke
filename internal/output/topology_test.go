package output_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/event"
	"github.com/jenaiz/pcke/internal/output"
)

// topoFixture seeds a small typed-event graph and returns the open DB
// and an event.Store. Callers close the DB; t.Cleanup handles temp dir.
func topoFixture(t *testing.T) (*kdb.DB, *event.Store) {
	t.Helper()
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("kdb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for range 5 {
		if err := db.Grow(); err != nil {
			t.Fatalf("db.Grow: %v", err)
		}
	}
	return db, event.New(db)
}

func appendEntity(t *testing.T, s *event.Store, eid string) {
	t.Helper()
	if _, err := s.Append(context.Background(), &event.Entity{
		Hdr: event.Header{CreatedAt: time.Now().UTC()}, EID: eid, Path: eid, Type: "file",
	}); err != nil {
		t.Fatalf("append entity %s: %v", eid, err)
	}
}

func appendLink(t *testing.T, s *event.Store, src, edge, dst string) {
	t.Helper()
	if _, err := s.AppendLink(context.Background(), &event.Link{
		Hdr: event.Header{CreatedAt: time.Now().UTC()}, SrcRef: src, EdgeType: edge, DstRef: dst,
	}); err != nil {
		t.Fatalf("append link: %v", err)
	}
}

func appendMustDecision(t *testing.T, s *event.Store, did string) {
	t.Helper()
	if _, err := s.Append(context.Background(), &event.Decision{
		Hdr:      event.Header{CreatedAt: time.Now().UTC()},
		DID:      did,
		Title:    did,
		Body:     "rule body",
		Severity: event.SeverityMust,
		Scope:    event.ScopeGlobal,
		Source:   "adr",
	}); err != nil {
		t.Fatalf("append decision: %v", err)
	}
}

func TestComputeTopology_EmptyGraph(t *testing.T) {
	t.Parallel()
	db, _ := topoFixture(t)
	topo, err := output.ComputeTopology(context.Background(), db)
	if err != nil {
		t.Fatalf("ComputeTopology: %v", err)
	}
	if !topo.IsEmpty() {
		t.Errorf("expected empty topology, got: %+v", topo)
	}
}

func TestComputeTopology_EntryPointsAndCore(t *testing.T) {
	t.Parallel()
	db, store := topoFixture(t)

	// cmd/pcke/main.go: entry point — fans out to 3 files, no incoming.
	// internal/kdb/db.go: core module member — receives 3 imports.
	for _, eid := range []string{
		"cmd/pcke/main.go",
		"internal/kdb/db.go",
		"internal/kdb/btree.go",
		"internal/kdb/wal.go",
		"internal/util/log.go",
	} {
		appendEntity(t, store, eid)
	}
	appendLink(t, store, "e:cmd/pcke/main.go", "imports", "e:internal/kdb/db.go")
	appendLink(t, store, "e:cmd/pcke/main.go", "imports", "e:internal/kdb/btree.go")
	appendLink(t, store, "e:cmd/pcke/main.go", "imports", "e:internal/util/log.go")
	appendLink(t, store, "e:internal/kdb/btree.go", "imports", "e:internal/kdb/db.go")
	appendLink(t, store, "e:internal/kdb/wal.go", "imports", "e:internal/kdb/db.go")

	topo, err := output.ComputeTopology(context.Background(), db)
	if err != nil {
		t.Fatalf("ComputeTopology: %v", err)
	}

	if len(topo.EntryPoints) == 0 || topo.EntryPoints[0] != "cmd/pcke/main.go" {
		t.Errorf("entry points: want cmd/pcke/main.go first, got %v", topo.EntryPoints)
	}

	// internal/kdb receives 3 incoming imports (db.go=3, btree.go=1).
	// internal/util receives 1.
	if len(topo.CoreModules) == 0 || !strings.HasPrefix(topo.CoreModules[0], "internal/kdb ") {
		t.Errorf("core modules: want internal/kdb first, got %v", topo.CoreModules)
	}
}

func TestComputeTopology_DecisionHotspots(t *testing.T) {
	t.Parallel()
	db, store := topoFixture(t)
	appendEntity(t, store, "internal/kdb/db.go")
	appendEntity(t, store, "cmd/pcke/main.go")
	for _, did := range []string{"rule-1", "rule-2", "rule-3", "rule-4"} {
		appendMustDecision(t, store, did)
	}
	for _, did := range []string{"rule-1", "rule-2", "rule-3"} {
		appendLink(t, store, "e:internal/kdb/db.go", "decision_link", "d:"+did)
	}
	appendLink(t, store, "e:cmd/pcke/main.go", "decision_link", "d:rule-4")

	topo, err := output.ComputeTopology(context.Background(), db)
	if err != nil {
		t.Fatalf("ComputeTopology: %v", err)
	}
	if len(topo.DecisionHotspots) != 1 {
		t.Fatalf("hotspots = %v, want exactly 1 (internal/kdb/db.go)", topo.DecisionHotspots)
	}
	if !strings.HasPrefix(topo.DecisionHotspots[0], "internal/kdb/db.go ") {
		t.Errorf("hotspot[0] = %q, want prefix internal/kdb/db.go", topo.DecisionHotspots[0])
	}
}

func TestSync_IncludesTopologyInAgentFiles(t *testing.T) {
	db, store := topoFixture(t)
	appendEntity(t, store, "cmd/pcke/main.go")
	appendEntity(t, store, "internal/kdb/db.go")
	appendEntity(t, store, "internal/kdb/btree.go")
	appendEntity(t, store, "internal/kdb/wal.go")
	appendLink(t, store, "e:cmd/pcke/main.go", "imports", "e:internal/kdb/db.go")
	appendLink(t, store, "e:cmd/pcke/main.go", "imports", "e:internal/kdb/btree.go")
	appendLink(t, store, "e:cmd/pcke/main.go", "imports", "e:internal/kdb/wal.go")
	appendLink(t, store, "e:internal/kdb/btree.go", "imports", "e:internal/kdb/db.go")
	appendLink(t, store, "e:internal/kdb/wal.go", "imports", "e:internal/kdb/db.go")

	outDir := t.TempDir()
	r := output.NewRenderer(outDir, db)
	if _, err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	for _, path := range []string{".github/copilot-instructions.md", ".claude/CLAUDE.md"} {
		data, err := readFile(t, outDir, path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(data, "Architecture Quick Reference") {
			t.Errorf("%s missing topology section:\n%s", path, data)
		}
		if !strings.Contains(data, "cmd/pcke/main.go") {
			t.Errorf("%s missing expected entry point cmd/pcke/main.go:\n%s", path, data)
		}
		if !strings.Contains(data, "internal/kdb") {
			t.Errorf("%s missing expected core module internal/kdb:\n%s", path, data)
		}
	}
}

func readFile(t *testing.T, dir, rel string) (string, error) {
	t.Helper()
	full := filepath.Join(dir, rel)
	b, err := os.ReadFile(full) //nolint:gosec // test reads a path we just wrote
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func TestComputeTopology_IgnoresShouldSeverity(t *testing.T) {
	t.Parallel()
	db, store := topoFixture(t)
	appendEntity(t, store, "f.go")
	// 3 SHOULD-severity decisions — should NOT trigger hotspot.
	for _, did := range []string{"s-1", "s-2", "s-3"} {
		if _, err := store.Append(context.Background(), &event.Decision{
			Hdr: event.Header{CreatedAt: time.Now().UTC()}, DID: did,
			Title: did, Severity: event.SeverityShould, Scope: event.ScopeGlobal, Source: "adr",
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
		appendLink(t, store, "e:f.go", "decision_link", "d:"+did)
	}
	topo, err := output.ComputeTopology(context.Background(), db)
	if err != nil {
		t.Fatalf("ComputeTopology: %v", err)
	}
	if len(topo.DecisionHotspots) != 0 {
		t.Errorf("should-severity must not trigger hotspots, got: %v", topo.DecisionHotspots)
	}
}

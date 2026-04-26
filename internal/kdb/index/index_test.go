package index_test

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/index"
)

// helperDB opens a fresh kdb database for testing and returns the DB.
func helperDB(t *testing.T) *kdb.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestEncodeDecodeCompositeKey verifies composite key round-trip.
func TestEncodeDecodeCompositeKey(t *testing.T) {
	tests := []struct {
		indexKey   string
		primaryKey string
	}{
		{"module-a", "node-001"},
		{"", "node-001"},
		{"module-a", ""},
		{"a", "b"},
		{"long-module-name", "long-primary-key-value"},
	}

	for _, tt := range tests {
		ck := index.EncodeCompositeKey([]byte(tt.indexKey), []byte(tt.primaryKey))
		gotIK, gotPK := index.DecodeCompositeKey(ck)

		if string(gotIK) != tt.indexKey {
			t.Errorf("indexKey: got %q, want %q", gotIK, tt.indexKey)
		}
		if string(gotPK) != tt.primaryKey {
			t.Errorf("primaryKey: got %q, want %q", gotPK, tt.primaryKey)
		}
	}
}

// TestByModuleInsertScan verifies basic by_module index operations.
func TestByModuleInsertScan(t *testing.T) {
	db := helperDB(t)
	pool, fl := db.TestSubsystems()

	idx := index.NewByModule(pool, fl, 0)

	if name := idx.Name(); name != "by_module" {
		t.Errorf("Name() = %q, want %q", name, "by_module")
	}

	// Insert 3 nodes in module "core".
	for i := 0; i < 3; i++ {
		pk := []byte(fmt.Sprintf("node-%03d", i))
		if err := idx.Insert(pk, index.ModuleKeys("core")); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Insert 2 nodes in module "util".
	for i := 3; i < 5; i++ {
		pk := []byte(fmt.Sprintf("node-%03d", i))
		if err := idx.Insert(pk, index.ModuleKeys("util")); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Scan "core": should find 3.
	pks, err := idx.Scan([]byte("core"))
	if err != nil {
		t.Fatalf("scan core: %v", err)
	}
	if len(pks) != 3 {
		t.Errorf("scan core: got %d results, want 3", len(pks))
	}

	// Scan "util": should find 2.
	pks, err = idx.Scan([]byte("util"))
	if err != nil {
		t.Fatalf("scan util: %v", err)
	}
	if len(pks) != 2 {
		t.Errorf("scan util: got %d results, want 2", len(pks))
	}

	// Scan "missing": should find 0.
	pks, err = idx.Scan([]byte("missing"))
	if err != nil {
		t.Fatalf("scan missing: %v", err)
	}
	if len(pks) != 0 {
		t.Errorf("scan missing: got %d results, want 0", len(pks))
	}
}

// TestByTagInsertScan verifies basic by_tag index operations.
func TestByTagInsertScan(t *testing.T) {
	db := helperDB(t)
	pool, fl := db.TestSubsystems()

	idx := index.NewByTag(pool, fl, 0)

	// Insert a note with 3 tags.
	pk := []byte("note-001")
	tags := []string{"go", "storage", "btree"}
	if err := idx.Insert(pk, index.TagKeys(tags)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Insert another note with overlapping tags.
	pk2 := []byte("note-002")
	tags2 := []string{"go", "testing"}
	if err := idx.Insert(pk2, index.TagKeys(tags2)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Scan "go": should find 2 notes.
	pks, err := idx.Scan([]byte("go"))
	if err != nil {
		t.Fatalf("scan go: %v", err)
	}
	if len(pks) != 2 {
		t.Errorf("scan go: got %d results, want 2", len(pks))
	}

	// Scan "btree": should find 1 note.
	pks, err = idx.Scan([]byte("btree"))
	if err != nil {
		t.Fatalf("scan btree: %v", err)
	}
	if len(pks) != 1 {
		t.Errorf("scan btree: got %d results, want 1", len(pks))
	}
}

// TestUpdateMovesModule verifies that updating a module removes old entries.
func TestUpdateMovesModule(t *testing.T) {
	db := helperDB(t)
	pool, fl := db.TestSubsystems()

	idx := index.NewByModule(pool, fl, 0)

	pk := []byte("node-001")

	// Insert into module "old".
	if err := idx.Insert(pk, index.ModuleKeys("old")); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Update: move from "old" to "new".
	if err := idx.Update(pk, index.ModuleKeys("old"), index.ModuleKeys("new")); err != nil {
		t.Fatalf("update: %v", err)
	}

	// "old" should be empty.
	pks, err := idx.Scan([]byte("old"))
	if err != nil {
		t.Fatalf("scan old: %v", err)
	}
	if len(pks) != 0 {
		t.Errorf("scan old: got %d, want 0", len(pks))
	}

	// "new" should have 1.
	pks, err = idx.Scan([]byte("new"))
	if err != nil {
		t.Fatalf("scan new: %v", err)
	}
	if len(pks) != 1 {
		t.Errorf("scan new: got %d, want 1", len(pks))
	}
}

// TestDeleteRemovesEntries verifies that deleting entries works.
func TestDeleteRemovesEntries(t *testing.T) {
	db := helperDB(t)
	pool, fl := db.TestSubsystems()

	idx := index.NewByTag(pool, fl, 0)

	pk := []byte("note-001")
	tags := []string{"alpha", "beta"}
	if err := idx.Insert(pk, index.TagKeys(tags)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Delete all tag entries.
	if err := idx.Delete(pk, index.TagKeys(tags)); err != nil {
		t.Fatalf("delete: %v", err)
	}

	for _, tag := range tags {
		pks, err := idx.Scan([]byte(tag))
		if err != nil {
			t.Fatalf("scan %s: %v", tag, err)
		}
		if len(pks) != 0 {
			t.Errorf("scan %s after delete: got %d, want 0", tag, len(pks))
		}
	}
}

// TestScanAll verifies the ScanAll diagnostic method.
func TestScanAll(t *testing.T) {
	db := helperDB(t)
	pool, fl := db.TestSubsystems()

	idx := index.NewByModule(pool, fl, 0)

	if err := idx.Insert([]byte("n1"), index.ModuleKeys("a")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := idx.Insert([]byte("n2"), index.ModuleKeys("b")); err != nil {
		t.Fatalf("insert: %v", err)
	}

	pairs, err := idx.ScanAll()
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}
	if len(pairs) != 2 {
		t.Fatalf("ScanAll: got %d pairs, want 2", len(pairs))
	}
}

// TestModuleKeysEmpty verifies that ModuleKeys returns nil for empty module.
func TestModuleKeysEmpty(t *testing.T) {
	if keys := index.ModuleKeys(""); keys != nil {
		t.Errorf("ModuleKeys empty: got %v, want nil", keys)
	}
}

// TestTagKeysEmpty verifies that TagKeys returns nil for empty slice.
func TestTagKeysEmpty(t *testing.T) {
	if keys := index.TagKeys(nil); keys != nil {
		t.Errorf("TagKeys nil: got %v, want nil", keys)
	}
	if keys := index.TagKeys([]string{}); keys != nil {
		t.Errorf("TagKeys empty: got %v, want nil", keys)
	}
}

// TestPropertyConsistency10K is the property test: 10K random mutations,
// then verify that an inverted scan of the index matches a filtered primary scan.
func TestPropertyConsistency10K(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 10K property test in short mode")
	}

	db := helperDB(t)
	pool, fl := db.TestSubsystems()

	modIdx := index.NewByModule(pool, fl, 0)
	tagIdx := index.NewByTag(pool, fl, 0)

	// Reference maps: track what the index SHOULD contain.
	moduleMap := map[string]string{}
	tagMap := map[string][]string{}

	rng := rand.New(rand.NewPCG(42, 0)) //nolint:gosec // G404: deterministic seed for reproducibility.

	modules := []string{"core", "util", "api", "web", "db", "auth", "log", "net"}
	allTags := []string{"go", "rust", "test", "doc", "perf", "sec", "bug", "feat", "fix", "ci"}

	const numKeys = 200
	const numOps = 10000

	pks := make([]string, numKeys)
	for i := range numKeys {
		pks[i] = fmt.Sprintf("pk-%05d", i)
	}

	for i := range numOps {
		pkStr := pks[rng.IntN(numKeys)]
		pk := []byte(pkStr)

		switch rng.IntN(3) {
		case 0:
			propApplyModuleUpdate(t, i, modIdx, moduleMap, pk, pkStr, modules[rng.IntN(len(modules))])
		case 1:
			propApplyTagUpdate(t, i, tagIdx, tagMap, pk, pkStr, allTags, rng)
		case 2:
			propApplyDelete(t, i, modIdx, tagIdx, moduleMap, tagMap, pk, pkStr)
		}
	}

	propVerifyModules(t, modIdx, moduleMap, modules)
	propVerifyTags(t, tagIdx, tagMap, allTags)
	propVerifyScanAll(t, modIdx, tagIdx, moduleMap, tagMap)
}

func propApplyModuleUpdate(t *testing.T, op int, idx *index.SecondaryIndex, m map[string]string, pk []byte, pkStr, newMod string) {
	t.Helper()
	if err := idx.Update(pk, index.ModuleKeys(m[pkStr]), index.ModuleKeys(newMod)); err != nil {
		t.Fatalf("op %d: module update: %v", op, err)
	}
	m[pkStr] = newMod
}

func propApplyTagUpdate(t *testing.T, op int, idx *index.SecondaryIndex, m map[string][]string, pk []byte, pkStr string, allTags []string, rng *rand.Rand) {
	t.Helper()
	numNew := rng.IntN(4) + 1
	newTags := make([]string, numNew)
	for j := range numNew {
		newTags[j] = allTags[rng.IntN(len(allTags))]
	}
	seen := map[string]bool{}
	deduped := newTags[:0]
	for _, tag := range newTags {
		if !seen[tag] {
			seen[tag] = true
			deduped = append(deduped, tag)
		}
	}
	newTags = deduped

	if err := idx.Update(pk, index.TagKeys(m[pkStr]), index.TagKeys(newTags)); err != nil {
		t.Fatalf("op %d: tag update: %v", op, err)
	}
	m[pkStr] = newTags
}

func propApplyDelete(t *testing.T, op int, modIdx, tagIdx *index.SecondaryIndex, moduleMap map[string]string, tagMap map[string][]string, pk []byte, pkStr string) {
	t.Helper()
	if oldMod := moduleMap[pkStr]; oldMod != "" {
		if err := modIdx.Delete(pk, index.ModuleKeys(oldMod)); err != nil {
			t.Fatalf("op %d: module delete: %v", op, err)
		}
		delete(moduleMap, pkStr)
	}
	if oldTags := tagMap[pkStr]; len(oldTags) > 0 {
		if err := tagIdx.Delete(pk, index.TagKeys(oldTags)); err != nil {
			t.Fatalf("op %d: tag delete: %v", op, err)
		}
		delete(tagMap, pkStr)
	}
}

func propVerifyModules(t *testing.T, modIdx *index.SecondaryIndex, moduleMap map[string]string, modules []string) {
	t.Helper()
	for _, mod := range modules {
		var expected []string
		for pk, m := range moduleMap {
			if m == mod {
				expected = append(expected, pk)
			}
		}
		sort.Strings(expected)

		got, err := modIdx.Scan([]byte(mod))
		if err != nil {
			t.Fatalf("verify module %s scan: %v", mod, err)
		}
		gotStrs := make([]string, len(got))
		for i, pk := range got {
			gotStrs[i] = string(pk)
		}
		sort.Strings(gotStrs)

		if len(gotStrs) != len(expected) {
			t.Errorf("module %s: got %d entries, want %d", mod, len(gotStrs), len(expected))
			continue
		}
		for i := range expected {
			if gotStrs[i] != expected[i] {
				t.Errorf("module %s[%d]: got %q, want %q", mod, i, gotStrs[i], expected[i])
			}
		}
	}
}

func propVerifyTags(t *testing.T, tagIdx *index.SecondaryIndex, tagMap map[string][]string, allTags []string) {
	t.Helper()
	for _, tag := range allTags {
		var expected []string
		for pk, tags := range tagMap {
			for _, tt := range tags {
				if tt == tag {
					expected = append(expected, pk)
					break
				}
			}
		}
		sort.Strings(expected)

		got, err := tagIdx.Scan([]byte(tag))
		if err != nil {
			t.Fatalf("verify tag %s scan: %v", tag, err)
		}
		gotStrs := make([]string, len(got))
		for i, pk := range got {
			gotStrs[i] = string(pk)
		}
		sort.Strings(gotStrs)

		if len(gotStrs) != len(expected) {
			t.Errorf("tag %s: got %d entries, want %d", tag, len(gotStrs), len(expected))
			continue
		}
		for i := range expected {
			if gotStrs[i] != expected[i] {
				t.Errorf("tag %s[%d]: got %q, want %q", tag, i, gotStrs[i], expected[i])
			}
		}
	}
}

func propVerifyScanAll(t *testing.T, modIdx, tagIdx *index.SecondaryIndex, moduleMap map[string]string, tagMap map[string][]string) {
	t.Helper()
	allMod, err := modIdx.ScanAll()
	if err != nil {
		t.Fatalf("ScanAll module: %v", err)
	}
	if len(allMod) != len(moduleMap) {
		t.Errorf("module ScanAll: got %d, want %d", len(allMod), len(moduleMap))
	}

	totalTagEntries := 0
	for _, tags := range tagMap {
		totalTagEntries += len(tags)
	}
	allTag, err := tagIdx.ScanAll()
	if err != nil {
		t.Fatalf("ScanAll tag: %v", err)
	}
	if len(allTag) != totalTagEntries {
		t.Errorf("tag ScanAll: got %d, want %d", len(allTag), totalTagEntries)
	}
}

// TestCompositeKeyOrdering verifies that composite keys sort correctly.
func TestCompositeKeyOrdering(t *testing.T) {
	keys := []struct {
		indexKey   string
		primaryKey string
	}{
		{"a", "1"},
		{"a", "2"},
		{"b", "1"},
		{"b", "2"},
	}

	var encoded [][]byte
	for _, k := range keys {
		encoded = append(encoded, index.EncodeCompositeKey([]byte(k.indexKey), []byte(k.primaryKey)))
	}

	// Verify they are in sorted order.
	for i := 1; i < len(encoded); i++ {
		if bytes.Compare(encoded[i-1], encoded[i]) >= 0 {
			t.Errorf("key %d >= key %d: %q >= %q", i-1, i, encoded[i-1], encoded[i])
		}
	}
}

// TestByFileInsertScan verifies basic by_file index operations.
func TestByFileInsertScan(t *testing.T) {
	db := helperDB(t)
	pool, fl := db.TestSubsystems()

	idx := index.NewByFile(pool, fl, 0)

	// Insert nodes for different files.
	files := map[string][]string{
		"internal/kdb/db.go":   {"kn:internal/kdb/db.go"},
		"cmd/pcke/root.go":     {"kn:cmd/pcke/root.go"},
		"internal/kdb/meta.go": {"kn:internal/kdb/meta.go"},
	}

	for file, pks := range files {
		for _, pk := range pks {
			if err := idx.Insert([]byte(pk), index.FileKeys(file)); err != nil {
				t.Fatalf("insert %s: %v", pk, err)
			}
		}
	}

	// Scan each file.
	for file, expectedPKs := range files {
		got, err := idx.Scan([]byte(file))
		if err != nil {
			t.Fatalf("scan %s: %v", file, err)
		}
		if len(got) != len(expectedPKs) {
			t.Errorf("scan %s: got %d results, want %d", file, len(got), len(expectedPKs))
		}
	}

	// Scan missing file.
	got, err := idx.Scan([]byte("nonexistent.go"))
	if err != nil {
		t.Fatalf("scan missing: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("scan missing: got %d results, want 0", len(got))
	}
}

// TestByTypeInsertScan verifies basic by_type index operations.
func TestByTypeInsertScan(t *testing.T) {
	db := helperDB(t)
	pool, fl := db.TestSubsystems()

	idx := index.NewByType(pool, fl, 0)

	// Insert nodes of type "file" (Phase 0 nodes).
	for i := 0; i < 5; i++ {
		pk := []byte(fmt.Sprintf("kn:file-%03d.go", i))
		if err := idx.Insert(pk, index.TypeKeys("file")); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Insert nodes of type "function" (Phase 2 entities).
	for i := 0; i < 3; i++ {
		pk := []byte(fmt.Sprintf("kn:func-%03d", i))
		if err := idx.Insert(pk, index.TypeKeys("function")); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Scan "file": should find 5.
	pks, err := idx.Scan([]byte("file"))
	if err != nil {
		t.Fatalf("scan file: %v", err)
	}
	if len(pks) != 5 {
		t.Errorf("scan file: got %d results, want 5", len(pks))
	}

	// Scan "function": should find 3.
	pks, err = idx.Scan([]byte("function"))
	if err != nil {
		t.Fatalf("scan function: %v", err)
	}
	if len(pks) != 3 {
		t.Errorf("scan function: got %d results, want 3", len(pks))
	}

	// Scan "struct": should find 0.
	pks, err = idx.Scan([]byte("struct"))
	if err != nil {
		t.Fatalf("scan struct: %v", err)
	}
	if len(pks) != 0 {
		t.Errorf("scan struct: got %d results, want 0", len(pks))
	}
}

// TestFileKeysEmpty verifies that FileKeys returns nil for empty path.
func TestFileKeysEmpty(t *testing.T) {
	if keys := index.FileKeys(""); keys != nil {
		t.Errorf("FileKeys empty: got %v, want nil", keys)
	}
}

// TestTypeKeysEmpty verifies that TypeKeys returns nil for empty type.
func TestTypeKeysEmpty(t *testing.T) {
	if keys := index.TypeKeys(""); keys != nil {
		t.Errorf("TypeKeys empty: got %v, want nil", keys)
	}
}

// TestIndexPersistenceThroughDB verifies that secondary index roots survive
// a close/reopen cycle via the DB meta persistence.
func TestIndexPersistenceThroughDB(t *testing.T) {
	dir := t.TempDir()

	indexPersistPopulate(t, dir)
	indexPersistVerify(t, dir)
}

func indexPersistPopulate(t *testing.T, dir string) {
	t.Helper()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	pk := []byte("kn:internal/kdb/db.go")
	if err := db.ModuleIndex().Insert(pk, index.ModuleKeys("internal/kdb")); err != nil {
		t.Fatalf("insert module: %v", err)
	}
	if err := db.FileIndex().Insert(pk, index.FileKeys("internal/kdb/db.go")); err != nil {
		t.Fatalf("insert file: %v", err)
	}
	if err := db.TypeIndex().Insert(pk, index.TypeKeys("file")); err != nil {
		t.Fatalf("insert type: %v", err)
	}

	if err := db.Checkpoint(context.Background()); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func indexPersistVerify(t *testing.T, dir string) {
	t.Helper()
	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = db.Close() }()

	pk := []byte("kn:internal/kdb/db.go")

	pks, err := db.ModuleIndex().Scan([]byte("internal/kdb"))
	if err != nil {
		t.Fatalf("scan module: %v", err)
	}
	if len(pks) != 1 || string(pks[0]) != string(pk) {
		t.Errorf("module index after reopen: got %v, want [%s]", pks, pk)
	}

	pks, err = db.FileIndex().Scan([]byte("internal/kdb/db.go"))
	if err != nil {
		t.Fatalf("scan file: %v", err)
	}
	if len(pks) != 1 || string(pks[0]) != string(pk) {
		t.Errorf("file index after reopen: got %v, want [%s]", pks, pk)
	}

	pks, err = db.TypeIndex().Scan([]byte("file"))
	if err != nil {
		t.Fatalf("scan type: %v", err)
	}
	if len(pks) != 1 || string(pks[0]) != string(pk) {
		t.Errorf("type index after reopen: got %v, want [%s]", pks, pk)
	}
}

package btree_test

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"sort"
	"sync"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/bufpool"
	"github.com/jenaiz/pcke/internal/kdb/freelist"
	"github.com/jenaiz/pcke/internal/kdb/page"
)

// ── Test infrastructure ──

// memPageIO implements bufpool.PageIO and freelist.PageIO using in-memory map.
type memPageIO struct {
	mu    sync.Mutex
	pages map[uint64][]byte
}

func newMemPageIO() *memPageIO {
	return &memPageIO{pages: make(map[uint64][]byte)}
}

func (m *memPageIO) ReadPage(pageID uint64) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	buf, ok := m.pages[pageID]
	if !ok {
		// Return a zeroed page for newly allocated pages.
		buf = make([]byte, page.Size)
		m.pages[pageID] = buf
	}

	cp := make([]byte, len(buf))
	copy(cp, buf)
	return cp, nil
}

func (m *memPageIO) WritePage(pageID uint64, buf []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cp := make([]byte, len(buf))
	copy(cp, buf)
	m.pages[pageID] = cp
	return nil
}

func (m *memPageIO) Sync() error { return nil }

// testEnv sets up a btree with an in-memory backend. It pre-populates the
// freelist with page IDs [startPage..startPage+count).
func testEnv(t *testing.T, freePages int) (*btree.Tree, *bufpool.Pool, *freelist.Freelist) {
	t.Helper()

	pio := newMemPageIO()
	pool := bufpool.New(pio, 1024)

	fl, err := freelist.New(pio, 0)
	if err != nil {
		t.Fatalf("freelist.New: %v", err)
	}

	// Seed freelist with pages (starting from 10 to avoid meta page range).
	for i := range freePages {
		if err := fl.Free(uint64(i + 10)); err != nil {
			t.Fatalf("freelist.Free(%d): %v", i+10, err)
		}
	}

	tree := btree.New(0, pool, fl)
	return tree, pool, fl
}

// ── Basic Get/Put/Delete tests ──

func TestPutGetSingle(t *testing.T) {
	tree, _, _ := testEnv(t, 1000)

	if err := tree.Put([]byte("hello"), []byte("world")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	val, err := tree.Get([]byte("hello"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(val) != "world" {
		t.Errorf("Get = %q, want %q", val, "world")
	}
}

func TestGetNotFound(t *testing.T) {
	tree, _, _ := testEnv(t, 1000)

	_, err := tree.Get([]byte("missing"))
	if err != btree.ErrKeyNotFound {
		t.Errorf("Get = %v, want ErrKeyNotFound", err)
	}
}

func TestGetEmptyTree(t *testing.T) {
	tree, _, _ := testEnv(t, 1000)

	_, err := tree.Get([]byte("anything"))
	if err != btree.ErrKeyNotFound {
		t.Errorf("Get on empty tree = %v, want ErrKeyNotFound", err)
	}
}

func TestEmptyKey(t *testing.T) {
	tree, _, _ := testEnv(t, 1000)

	if err := tree.Put(nil, []byte("val")); err != btree.ErrEmptyKey {
		t.Errorf("Put(nil) = %v, want ErrEmptyKey", err)
	}
	if err := tree.Put([]byte{}, []byte("val")); err != btree.ErrEmptyKey {
		t.Errorf("Put([]) = %v, want ErrEmptyKey", err)
	}
	if _, err := tree.Get(nil); err != btree.ErrEmptyKey {
		t.Errorf("Get(nil) = %v, want ErrEmptyKey", err)
	}
	if err := tree.Delete(nil); err != btree.ErrEmptyKey {
		t.Errorf("Delete(nil) = %v, want ErrEmptyKey", err)
	}
}

func TestPutUpdate(t *testing.T) {
	tree, _, _ := testEnv(t, 1000)

	if err := tree.Put([]byte("key"), []byte("v1")); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	if err := tree.Put([]byte("key"), []byte("v2")); err != nil {
		t.Fatalf("Put v2: %v", err)
	}

	val, err := tree.Get([]byte("key"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(val) != "v2" {
		t.Errorf("Get = %q, want %q", val, "v2")
	}
}

func TestDeleteBasic(t *testing.T) {
	tree, _, _ := testEnv(t, 1000)

	if err := tree.Put([]byte("a"), []byte("1")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := tree.Delete([]byte("a")); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := tree.Get([]byte("a"))
	if err != btree.ErrKeyNotFound {
		t.Errorf("Get after Delete = %v, want ErrKeyNotFound", err)
	}
}

func TestDeleteNotFound(t *testing.T) {
	tree, _, _ := testEnv(t, 1000)

	if err := tree.Put([]byte("a"), []byte("1")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	err := tree.Delete([]byte("missing"))
	if err != btree.ErrKeyNotFound {
		t.Errorf("Delete(missing) = %v, want ErrKeyNotFound", err)
	}
}

// ── Multiple keys / ordering ──

func TestPutGetMultipleKeys(t *testing.T) {
	tree, _, _ := testEnv(t, 10000)
	n := 200

	for i := range n {
		k := fmt.Sprintf("key-%04d", i)
		v := fmt.Sprintf("val-%04d", i)
		if err := tree.Put([]byte(k), []byte(v)); err != nil {
			t.Fatalf("Put(%s): %v", k, err)
		}
	}

	for i := range n {
		k := fmt.Sprintf("key-%04d", i)
		v := fmt.Sprintf("val-%04d", i)
		got, err := tree.Get([]byte(k))
		if err != nil {
			t.Fatalf("Get(%s): %v", k, err)
		}
		if string(got) != v {
			t.Errorf("Get(%s) = %q, want %q", k, got, v)
		}
	}
}

func TestPutReverseOrder(t *testing.T) {
	tree, _, _ := testEnv(t, 10000)
	n := 200

	for i := n - 1; i >= 0; i-- {
		k := fmt.Sprintf("key-%04d", i)
		v := fmt.Sprintf("val-%04d", i)
		if err := tree.Put([]byte(k), []byte(v)); err != nil {
			t.Fatalf("Put(%s): %v", k, err)
		}
	}

	for i := range n {
		k := fmt.Sprintf("key-%04d", i)
		v := fmt.Sprintf("val-%04d", i)
		got, err := tree.Get([]byte(k))
		if err != nil {
			t.Fatalf("Get(%s): %v", k, err)
		}
		if string(got) != v {
			t.Errorf("Get(%s) = %q, want %q", k, got, v)
		}
	}
}

// ── Splits ──

func TestLeafSplit(t *testing.T) {
	tree, _, _ := testEnv(t, 10000)

	// Insert enough keys to force at least one leaf split.
	// Each cell is ~4+8+8 = ~20 bytes min, so ~200 cells per page max.
	// With 100-byte keys + 100-byte values, we get ~48 cells per page.
	n := 500
	for i := range n {
		k := fmt.Sprintf("split-test-key-%06d", i)
		v := fmt.Sprintf("split-test-val-%06d", i)
		if err := tree.Put([]byte(k), []byte(v)); err != nil {
			t.Fatalf("Put(%s): %v", k, err)
		}
	}

	// Verify all keys are still retrievable.
	for i := range n {
		k := fmt.Sprintf("split-test-key-%06d", i)
		v := fmt.Sprintf("split-test-val-%06d", i)
		got, err := tree.Get([]byte(k))
		if err != nil {
			t.Fatalf("Get(%s): %v", k, err)
		}
		if string(got) != v {
			t.Errorf("Get(%s) = %q, want %q", k, got, v)
		}
	}
}

func TestInternalNodeSplit(t *testing.T) {
	tree, _, _ := testEnv(t, 50000)

	// Use longer keys to force internal splits sooner.
	n := 2000
	for i := range n {
		k := fmt.Sprintf("internal-split-long-key-name-%06d", i)
		v := fmt.Sprintf("v%d", i)
		if err := tree.Put([]byte(k), []byte(v)); err != nil {
			t.Fatalf("Put(%s): %v", k, err)
		}
	}

	for i := range n {
		k := fmt.Sprintf("internal-split-long-key-name-%06d", i)
		v := fmt.Sprintf("v%d", i)
		got, err := tree.Get([]byte(k))
		if err != nil {
			t.Fatalf("Get(%s): %v", k, err)
		}
		if string(got) != v {
			t.Errorf("Get(%s) = %q, want %q", k, got, v)
		}
	}
}

// ── Overflow pages ──

func TestOverflowValue(t *testing.T) {
	tree, _, _ := testEnv(t, 10000)

	// Create a value larger than the overflow threshold.
	bigVal := make([]byte, 3000)
	for i := range bigVal {
		bigVal[i] = byte(i % 256)
	}

	if err := tree.Put([]byte("big"), bigVal); err != nil {
		t.Fatalf("Put big: %v", err)
	}

	got, err := tree.Get([]byte("big"))
	if err != nil {
		t.Fatalf("Get big: %v", err)
	}
	if !bytes.Equal(got, bigVal) {
		t.Errorf("Get big: length %d, want %d", len(got), len(bigVal))
	}
}

func TestOverflowMultiPage(t *testing.T) {
	tree, _, _ := testEnv(t, 10000)

	// Value spanning multiple overflow pages (> 4064 bytes each).
	bigVal := make([]byte, 15000)
	for i := range bigVal {
		bigVal[i] = byte((i * 7) % 256)
	}

	if err := tree.Put([]byte("huge"), bigVal); err != nil {
		t.Fatalf("Put huge: %v", err)
	}

	got, err := tree.Get([]byte("huge"))
	if err != nil {
		t.Fatalf("Get huge: %v", err)
	}
	if !bytes.Equal(got, bigVal) {
		t.Errorf("Get huge: length %d, want %d; first byte: %d vs %d",
			len(got), len(bigVal), got[0], bigVal[0])
	}
}

func TestOverflowUpdateAndDelete(t *testing.T) {
	tree, _, _ := testEnv(t, 10000)

	bigVal1 := bytes.Repeat([]byte("A"), 3000)
	bigVal2 := bytes.Repeat([]byte("B"), 4000)

	// Insert.
	if err := tree.Put([]byte("ov"), bigVal1); err != nil {
		t.Fatalf("Put v1: %v", err)
	}

	// Update with different overflow value.
	if err := tree.Put([]byte("ov"), bigVal2); err != nil {
		t.Fatalf("Put v2: %v", err)
	}

	got, err := tree.Get([]byte("ov"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, bigVal2) {
		t.Errorf("Get length %d, want %d", len(got), len(bigVal2))
	}

	// Delete.
	if err := tree.Delete([]byte("ov")); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = tree.Get([]byte("ov"))
	if err != btree.ErrKeyNotFound {
		t.Errorf("Get after delete: %v, want ErrKeyNotFound", err)
	}
}

func TestOverflowRoundtrip(t *testing.T) {
	tree, _, _ := testEnv(t, 10000)

	// Insert mix of small and large values.
	for i := range 50 {
		k := fmt.Sprintf("k%04d", i)
		size := 10 + (i * 100) // ranges from 10 to 4910
		v := make([]byte, size)
		for j := range v {
			v[j] = byte((i + j) % 256)
		}
		if err := tree.Put([]byte(k), v); err != nil {
			t.Fatalf("Put(%s, len=%d): %v", k, size, err)
		}
	}

	// Verify all roundtrip.
	for i := range 50 {
		k := fmt.Sprintf("k%04d", i)
		size := 10 + (i * 100)
		expected := make([]byte, size)
		for j := range expected {
			expected[j] = byte((i + j) % 256)
		}
		got, err := tree.Get([]byte(k))
		if err != nil {
			t.Fatalf("Get(%s): %v", k, err)
		}
		if !bytes.Equal(got, expected) {
			t.Errorf("Get(%s): len=%d, want len=%d", k, len(got), len(expected))
		}
	}
}

// ── Cursor tests ──

func TestCursorEmptyTree(t *testing.T) {
	tree, _, _ := testEnv(t, 1000)

	c := tree.Cursor()
	if c.First() {
		t.Error("First() on empty tree should return false")
	}
	if c.Seek([]byte("anything")) {
		t.Error("Seek() on empty tree should return false")
	}
}

func TestCursorFirstNext(t *testing.T) {
	tree, _, _ := testEnv(t, 10000)
	n := 100

	keys := make([]string, n)
	for i := range n {
		keys[i] = fmt.Sprintf("cursor-%04d", i)
	}

	// Insert in random order.
	perm := rand.Perm(n)
	for _, idx := range perm {
		k := keys[idx]
		if err := tree.Put([]byte(k), []byte("v")); err != nil {
			t.Fatalf("Put(%s): %v", k, err)
		}
	}

	// Sort expected keys.
	sort.Strings(keys)

	// Iterate with cursor.
	c := tree.Cursor()
	if !c.First() {
		t.Fatal("First() returned false")
	}

	var got []string
	for c.Valid() {
		got = append(got, string(c.Key()))
		c.Next()
	}

	if len(got) != len(keys) {
		t.Fatalf("cursor iterated %d keys, want %d", len(got), len(keys))
	}

	for i := range keys {
		if got[i] != keys[i] {
			t.Errorf("key[%d] = %q, want %q", i, got[i], keys[i])
			break
		}
	}
}

func TestCursorSeek(t *testing.T) {
	tree, _, _ := testEnv(t, 10000)

	for i := range 100 {
		k := fmt.Sprintf("seek-%04d", i*2) // even numbers only
		if err := tree.Put([]byte(k), []byte("v")); err != nil {
			t.Fatalf("Put(%s): %v", k, err)
		}
	}

	c := tree.Cursor()

	// Seek to existing key.
	if !c.Seek([]byte("seek-0010")) {
		t.Fatal("Seek(seek-0010) returned false")
	}
	if string(c.Key()) != "seek-0010" {
		t.Errorf("Key = %q, want %q", c.Key(), "seek-0010")
	}

	// Seek to gap (seek-0011 doesn't exist, should land on seek-0012).
	if !c.Seek([]byte("seek-0011")) {
		t.Fatal("Seek(seek-0011) returned false")
	}
	if string(c.Key()) != "seek-0012" {
		t.Errorf("Key = %q, want %q", c.Key(), "seek-0012")
	}

	// Seek past all keys.
	if c.Seek([]byte("zzz")) {
		t.Error("Seek(zzz) should return false")
	}
}

func TestCursorLexOrder(t *testing.T) {
	tree, _, _ := testEnv(t, 50000)
	n := 1000

	// Insert random keys.
	rng := rand.New(rand.NewPCG(42, 0)) //nolint:gosec // deterministic test RNG
	inserted := make(map[string]bool)
	for len(inserted) < n {
		k := randKey(rng, 30)
		ks := string(k)
		if inserted[ks] {
			continue
		}
		inserted[ks] = true
		if err := tree.Put(k, []byte("v")); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	expected := sortedMapKeys(inserted)
	got := collectCursorKeys(t, tree)
	assertKeysEqual(t, got, expected)
}

// ── Test helpers for property tests ──

// randKey generates a random key of length 1..maxLen using lowercase letters.
func randKey(rng *rand.Rand, maxLen int) []byte {
	kLen := rng.IntN(maxLen) + 1
	k := make([]byte, kLen)
	for j := range k {
		k[j] = 'a' + byte(rng.IntN(26)) //nolint:gosec // test-only; no overflow risk
	}
	return k
}

// randVal generates a random value of length 1..maxLen.
func randVal(rng *rand.Rand, maxLen int) []byte {
	vLen := rng.IntN(maxLen) + 1
	v := make([]byte, vLen)
	for j := range v {
		v[j] = byte(rng.IntN(256)) //nolint:gosec // test-only; no overflow risk
	}
	return v
}

// sortedMapKeys returns the sorted keys of a map[string]bool.
func sortedMapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// collectCursorKeys returns all keys from the tree's cursor in order.
func collectCursorKeys(t *testing.T, tree *btree.Tree) []string {
	t.Helper()
	c := tree.Cursor()
	var keys []string
	if c.First() {
		for c.Valid() {
			keys = append(keys, string(c.Key()))
			c.Next()
		}
	}
	return keys
}

// assertKeysEqual asserts that got and want slices are identical.
func assertKeysEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("key count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("key[%d] = %q, want %q", i, got[i], want[i])
			return
		}
	}
}

// runRandomOp executes a single random Put/Get/Delete against the tree,
// validating against reference.
func runRandomOp(
	t *testing.T, tree *btree.Tree, rng *rand.Rand,
	reference map[string]string, i int, maxKeyLen, maxValLen int,
) {
	t.Helper()
	op := rng.IntN(3)
	k := randKey(rng, maxKeyLen)
	ks := string(k)

	switch op {
	case 0: // Put
		v := randVal(rng, maxValLen)
		if err := tree.Put(k, v); err != nil {
			t.Fatalf("op %d: Put(%q): %v", i, ks, err)
		}
		reference[ks] = string(v)
	case 1: // Get
		assertGet(t, tree, k, reference, i)
	case 2: // Delete
		assertDelete(t, tree, k, reference, i)
	}
}

// assertGet checks a Get against the reference map.
func assertGet(t *testing.T, tree *btree.Tree, k []byte, ref map[string]string, op int) {
	t.Helper()
	ks := string(k)
	got, err := tree.Get(k)
	expected, exists := ref[ks]
	if !exists {
		if err != btree.ErrKeyNotFound {
			t.Fatalf("op %d: Get(%q) = %v, want ErrKeyNotFound", op, ks, err)
		}
	} else {
		if err != nil {
			t.Fatalf("op %d: Get(%q): %v", op, ks, err)
		}
		if string(got) != expected {
			t.Fatalf("op %d: Get(%q) len=%d, want len=%d", op, ks, len(got), len(expected))
		}
	}
}

// assertDelete checks a Delete against the reference map.
func assertDelete(t *testing.T, tree *btree.Tree, k []byte, ref map[string]string, op int) {
	t.Helper()
	ks := string(k)
	err := tree.Delete(k)
	_, exists := ref[ks]
	if !exists {
		if err != btree.ErrKeyNotFound {
			t.Fatalf("op %d: Delete(%q) = %v, want ErrKeyNotFound", op, ks, err)
		}
	} else {
		if err != nil {
			t.Fatalf("op %d: Delete(%q): %v", op, ks, err)
		}
		delete(ref, ks)
	}
}

// assertTreeMatchesRef checks that every key in the reference map exists in
// the tree with the correct value.
func assertTreeMatchesRef(t *testing.T, tree *btree.Tree, ref map[string]string) {
	t.Helper()
	for ks, vs := range ref {
		got, err := tree.Get([]byte(ks))
		if err != nil {
			t.Errorf("final: Get(%q): %v", ks, err)
			continue
		}
		if string(got) != vs {
			t.Errorf("final: Get(%q) len=%d, want len=%d", ks, len(got), len(vs))
		}
	}
}

// ── Property test: 10K random ops ──

func TestPropertyRandomOps(t *testing.T) {
	tree, _, _ := testEnv(t, 100000)

	rng := rand.New(rand.NewPCG(12345, 0)) //nolint:gosec // deterministic test RNG
	reference := make(map[string]string)

	for i := range 10000 {
		runRandomOp(t, tree, rng, reference, i, 20, 100)
	}

	assertTreeMatchesRef(t, tree, reference)

	// Verify cursor order matches reference.
	expectedKeys := make([]string, 0, len(reference))
	for k := range reference {
		expectedKeys = append(expectedKeys, k)
	}
	sort.Strings(expectedKeys)

	cursorKeys := collectCursorKeys(t, tree)
	assertKeysEqual(t, cursorKeys, expectedKeys)

	// Verify strict sorted order.
	for i := 1; i < len(cursorKeys); i++ {
		if cursorKeys[i] <= cursorKeys[i-1] {
			t.Errorf("cursor: key[%d] %q <= key[%d] %q (not sorted)",
				i, cursorKeys[i], i-1, cursorKeys[i-1])
			break
		}
	}
}

// ── Property test with large values (overflow) ──

func TestPropertyRandomOpsWithOverflow(t *testing.T) {
	tree, _, _ := testEnv(t, 100000)

	rng := rand.New(rand.NewPCG(99999, 0)) //nolint:gosec // deterministic test RNG
	reference := make(map[string]string)

	for i := range 2000 {
		runRandomOp(t, tree, rng, reference, i, 15, 6000)
	}

	assertTreeMatchesRef(t, tree, reference)
}

func TestCursorValue(t *testing.T) {
	tree, _, _ := testEnv(t, 10000)

	pairs := map[string]string{
		"alpha": "val-alpha",
		"beta":  "val-beta",
		"gamma": "val-gamma",
	}

	for k, v := range pairs {
		if err := tree.Put([]byte(k), []byte(v)); err != nil {
			t.Fatalf("Put(%s): %v", k, err)
		}
	}

	c := tree.Cursor()
	if !c.First() {
		t.Fatal("First() returned false")
	}

	for c.Valid() {
		k := string(c.Key())
		v := string(c.Value())
		expected, ok := pairs[k]
		if !ok {
			t.Errorf("unexpected key %q", k)
		} else if v != expected {
			t.Errorf("Value(%s) = %q, want %q", k, v, expected)
		}
		c.Next()
	}
}

func TestDeleteAllKeys(t *testing.T) {
	tree, _, _ := testEnv(t, 10000)
	n := 200

	keys := make([]string, n)
	for i := range n {
		keys[i] = fmt.Sprintf("del-%04d", i)
		if err := tree.Put([]byte(keys[i]), []byte("v")); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	for _, k := range keys {
		if err := tree.Delete([]byte(k)); err != nil {
			t.Fatalf("Delete(%s): %v", k, err)
		}
	}

	for _, k := range keys {
		if _, err := tree.Get([]byte(k)); err != btree.ErrKeyNotFound {
			t.Errorf("Get(%s) after delete-all = %v, want ErrKeyNotFound", k, err)
		}
	}
}

func TestRootPageNonZero(t *testing.T) {
	tree, _, _ := testEnv(t, 1000)

	if tree.Root() != 0 {
		t.Errorf("Root before Put = %d, want 0", tree.Root())
	}

	if err := tree.Put([]byte("a"), []byte("1")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if tree.Root() == 0 {
		t.Error("Root after Put = 0, want non-zero")
	}
}

// TestInternalNodeSplitDeep forces deep internal splits by using long keys
// that reduce the per-node capacity. 500-byte keys ≈ 7 cells per node,
// so 500 entries should produce a tree of depth ≥ 3 with internal splits.
func TestInternalNodeSplitDeep(t *testing.T) {
	tree, _, _ := testEnv(t, 100000)

	n := 500
	for i := range n {
		// 490-byte key + 10-char prefix = ~500 bytes total.
		k := fmt.Sprintf("deep-isplit-key-%0480d", i)
		v := fmt.Sprintf("v%d", i)
		if err := tree.Put([]byte(k), []byte(v)); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}

	for i := range n {
		k := fmt.Sprintf("deep-isplit-key-%0480d", i)
		v := fmt.Sprintf("v%d", i)
		got, err := tree.Get([]byte(k))
		if err != nil {
			t.Fatalf("Get(%d): %v", i, err)
		}
		if string(got) != v {
			t.Errorf("Get(%d) = %q, want %q", i, got, v)
		}
	}

	// Verify cursor order.
	keys := collectCursorKeys(t, tree)
	if len(keys) != n {
		t.Fatalf("cursor: %d keys, want %d", len(keys), n)
	}
	for i := 1; i < len(keys); i++ {
		if keys[i] <= keys[i-1] {
			t.Errorf("not sorted at %d: %q <= %q", i, keys[i], keys[i-1])
			break
		}
	}
}

// TestCursorValueOverflow tests cursor Value() with overflow values.
func TestCursorValueOverflow(t *testing.T) {
	tree, _, _ := testEnv(t, 10000)

	bigVal := bytes.Repeat([]byte("X"), 5000)
	if err := tree.Put([]byte("big"), bigVal); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := tree.Put([]byte("small"), []byte("tiny")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	c := tree.Cursor()
	if !c.First() {
		t.Fatal("First() = false")
	}

	found := false
	for c.Valid() {
		if string(c.Key()) == "big" {
			v := c.Value()
			if !bytes.Equal(v, bigVal) {
				t.Errorf("cursor Value(big): len=%d, want %d", len(v), len(bigVal))
			}
			found = true
		}
		c.Next()
	}
	if !found {
		t.Error("cursor never visited key 'big'")
	}
}

// TestKeyTooLarge verifies the key size limit.
func TestKeyTooLarge(t *testing.T) {
	tree, _, _ := testEnv(t, 1000)
	bigKey := bytes.Repeat([]byte("K"), 2100)
	err := tree.Put(bigKey, []byte("v"))
	if err != btree.ErrKeyTooLarge {
		t.Errorf("Put(bigKey) = %v, want ErrKeyTooLarge", err)
	}
}

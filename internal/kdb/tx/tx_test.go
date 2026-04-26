package tx_test

import (
	"fmt"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/bufpool"
	"github.com/jenaiz/pcke/internal/kdb/freelist"
	"github.com/jenaiz/pcke/internal/kdb/page"
	"github.com/jenaiz/pcke/internal/kdb/tx"
	"github.com/jenaiz/pcke/internal/kdb/wal"
)

// memPageIO implements bufpool.PageIO using an in-memory map.
type memPageIO struct {
	pages map[uint64][]byte
}

func newMemPageIO() *memPageIO {
	return &memPageIO{pages: make(map[uint64][]byte)}
}

func (m *memPageIO) ReadPage(pageID uint64) ([]byte, error) {
	buf, ok := m.pages[pageID]
	if !ok {
		buf = make([]byte, page.Size)
		m.pages[pageID] = buf
	}
	cp := make([]byte, len(buf))
	copy(cp, buf)
	return cp, nil
}

func (m *memPageIO) WritePage(pageID uint64, buf []byte) error {
	cp := make([]byte, len(buf))
	copy(cp, buf)
	m.pages[pageID] = cp
	return nil
}

func (m *memPageIO) Sync() error { return nil }

func setupTestEnv(t *testing.T) (*btree.Tree, *wal.WAL, *bufpool.Pool) {
	t.Helper()

	pio := newMemPageIO()
	pool := bufpool.New(pio, 1024)

	// Pre-populate pages.
	for i := uint64(0); i < 10000; i++ {
		buf := make([]byte, page.Size)
		_ = pio.WritePage(i, buf)
	}

	// Reserve for BTreeFreelist.
	reserve := make([]uint64, 20)
	for i := range reserve {
		reserve[i] = uint64(5000 + i)
	}
	fl := freelist.OpenBTreeFreelist(pool, 0, reserve)

	// Seed freelist.
	for i := uint64(100); i < 500; i++ {
		if err := fl.Free(i); err != nil {
			t.Fatalf("Free(%d): %v", i, err)
		}
	}

	tree := btree.New(0, pool, fl)

	dir := t.TempDir()
	w, err := wal.Open(dir)
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	return tree, w, pool
}

func TestReadTxGet(t *testing.T) {
	tree, w, pool := setupTestEnv(t)

	// Insert a key via WriteTx.
	wtx := tx.NewWriteTx(tree, w, pool)
	if err := wtx.Put([]byte("hello"), []byte("world")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := wtx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Read via ReadTx.
	rtx := tx.NewReadTx(tree)
	defer rtx.Close()

	val, err := rtx.Get([]byte("hello"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(val) != "world" {
		t.Errorf("Get = %q, want %q", val, "world")
	}
}

func TestReadTxGetNotFound(t *testing.T) {
	tree, _, _ := setupTestEnv(t)

	rtx := tx.NewReadTx(tree)
	defer rtx.Close()

	_, err := rtx.Get([]byte("missing"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestReadTxClosed(t *testing.T) {
	tree, _, _ := setupTestEnv(t)

	rtx := tx.NewReadTx(tree)
	rtx.Close()

	_, err := rtx.Get([]byte("hello"))
	if err != tx.ErrTxClosed {
		t.Errorf("Get on closed tx: got %v, want ErrTxClosed", err)
	}
}

func TestWriteTxPutCommit(t *testing.T) {
	tree, w, pool := setupTestEnv(t)

	wtx := tx.NewWriteTx(tree, w, pool)
	if err := wtx.Put([]byte("key1"), []byte("val1")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := wtx.Put([]byte("key2"), []byte("val2")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := wtx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Verify via tree directly.
	val, err := tree.Get([]byte("key1"))
	if err != nil {
		t.Fatalf("Get key1: %v", err)
	}
	if string(val) != "val1" {
		t.Errorf("key1 = %q, want %q", val, "val1")
	}

	val, err = tree.Get([]byte("key2"))
	if err != nil {
		t.Fatalf("Get key2: %v", err)
	}
	if string(val) != "val2" {
		t.Errorf("key2 = %q, want %q", val, "val2")
	}
}

func TestWriteTxDelete(t *testing.T) {
	tree, w, pool := setupTestEnv(t)

	// Insert.
	wtx := tx.NewWriteTx(tree, w, pool)
	if err := wtx.Put([]byte("del-me"), []byte("bye")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := wtx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Delete.
	wtx2 := tx.NewWriteTx(tree, w, pool)
	if err := wtx2.Delete([]byte("del-me")); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := wtx2.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Should be gone.
	_, err := tree.Get([]byte("del-me"))
	if err == nil {
		t.Fatal("expected ErrKeyNotFound after delete")
	}
}

func TestWriteTxRollback(t *testing.T) {
	tree, w, pool := setupTestEnv(t)

	wtx := tx.NewWriteTx(tree, w, pool)
	if err := wtx.Put([]byte("ephemeral"), []byte("value")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	wtx.Rollback()

	if wtx.Committed() {
		t.Error("should not be committed after rollback")
	}
}

func TestWriteTxClosedOps(t *testing.T) {
	tree, w, pool := setupTestEnv(t)

	wtx := tx.NewWriteTx(tree, w, pool)
	if err := wtx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if err := wtx.Put([]byte("k"), []byte("v")); err != tx.ErrTxClosed {
		t.Errorf("Put on closed tx: got %v, want ErrTxClosed", err)
	}
	if err := wtx.Delete([]byte("k")); err != tx.ErrTxClosed {
		t.Errorf("Delete on closed tx: got %v, want ErrTxClosed", err)
	}
	if err := wtx.Commit(); err != tx.ErrTxClosed {
		t.Errorf("Commit on closed tx: got %v, want ErrTxClosed", err)
	}
}

func TestDecodeKV(t *testing.T) {
	tests := []struct {
		key, value string
	}{
		{"hello", "world"},
		{"", "empty-key"},
		{"k", ""},
		{"long-key-here", "long-value-here-too"},
	}

	for _, tt := range tests {
		k, v := tx.DecodeKV(encodeKVHelper([]byte(tt.key), []byte(tt.value)))
		if string(k) != tt.key {
			t.Errorf("key: got %q, want %q", k, tt.key)
		}
		if string(v) != tt.value {
			t.Errorf("value: got %q, want %q", v, tt.value)
		}
	}
}

func TestDecodeKVInvalid(t *testing.T) {
	k, v := tx.DecodeKV(nil)
	if k != nil || v != nil {
		t.Error("expected nil for nil input")
	}

	k, v = tx.DecodeKV([]byte{0, 0, 0})
	if k != nil || v != nil {
		t.Error("expected nil for short input")
	}
}

// encodeKVHelper mirrors tx.encodeKV for tests.
func encodeKVHelper(key, value []byte) []byte {
	buf := make([]byte, 4+len(key)+len(value))
	buf[0] = byte(len(key) >> 24) //nolint:gosec // G115: test helper, key length bounded.
	buf[1] = byte(len(key) >> 16) //nolint:gosec // G115
	buf[2] = byte(len(key) >> 8)  //nolint:gosec // G115
	buf[3] = byte(len(key))       //nolint:gosec // G115
	copy(buf[4:], key)
	copy(buf[4+len(key):], value)
	return buf
}

func TestGroupCommitTx_PutCommit(t *testing.T) {
	tree, w, pool := setupTestEnv(t)

	wtx := tx.NewGroupCommitTx(tree, w, pool)
	if err := wtx.Put([]byte("gk1"), []byte("gv1")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := wtx.Put([]byte("gk2"), []byte("gv2")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := wtx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	val, err := tree.Get([]byte("gk1"))
	if err != nil {
		t.Fatalf("Get gk1: %v", err)
	}
	if string(val) != "gv1" {
		t.Errorf("gk1 = %q, want %q", val, "gv1")
	}
}

func TestGroupCommitTx_Delete(t *testing.T) {
	tree, w, pool := setupTestEnv(t)

	// Insert first.
	wtx := tx.NewGroupCommitTx(tree, w, pool)
	if err := wtx.Put([]byte("del-gc"), []byte("val")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := wtx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Delete.
	wtx2 := tx.NewGroupCommitTx(tree, w, pool)
	if err := wtx2.Delete([]byte("del-gc")); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := wtx2.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	_, err := tree.Get([]byte("del-gc"))
	if err == nil {
		t.Fatal("expected ErrKeyNotFound after group-commit delete")
	}
}

func TestGroupCommitTx_ClosedOps(t *testing.T) {
	tree, w, pool := setupTestEnv(t)

	wtx := tx.NewGroupCommitTx(tree, w, pool)
	if err := wtx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if err := wtx.Put([]byte("k"), []byte("v")); err != tx.ErrTxClosed {
		t.Errorf("Put on closed group-commit tx: got %v, want ErrTxClosed", err)
	}
	if err := wtx.Delete([]byte("k")); err != tx.ErrTxClosed {
		t.Errorf("Delete on closed group-commit tx: got %v, want ErrTxClosed", err)
	}
}

func TestReadTxCursor(t *testing.T) {
	tree, w, pool := setupTestEnv(t)

	// Insert a few keys.
	wtx := tx.NewWriteTx(tree, w, pool)
	for i := 0; i < 3; i++ {
		k := fmt.Sprintf("cur-%02d", i)
		if err := wtx.Put([]byte(k), []byte("v")); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	if err := wtx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	rtx := tx.NewReadTx(tree)
	defer rtx.Close()

	cur := rtx.Cursor()
	if cur == nil {
		t.Fatal("Cursor returned nil")
	}

	var count int
	for cur.First(); cur.Valid(); cur.Next() {
		count++
	}
	if count < 3 {
		t.Errorf("cursor iterated %d keys, want >= 3", count)
	}
}

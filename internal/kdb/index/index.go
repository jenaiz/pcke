// Package index implements B+tree-backed secondary indexes for kdb.
//
// Secondary indexes maintain derived key mappings alongside the primary
// key-value store, updated atomically within the same transaction.
// Phase 0 provides two indexes: by_module (module name → node ID) and
// by_tag (tag → note ID).
//
// Each index is a separate [btree.Tree] instance using composite keys
// of the form "field_value\x00primary_key" with empty values, enabling
// efficient prefix range scans.
//
// Concurrency: indexes are modified exclusively within [tx.WriteTx] and
// read within [tx.ReadTx]. The caller (kdb.DB) manages locking.
//
// Phase 0 — Task T14. See PRD §4.9.
package index

import (
	"bytes"

	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/bufpool"
)

// SecondaryIndex is a B+tree-backed secondary index that maps derived keys
// to primary keys using composite keys of the form "index_key\x00primary_key".
type SecondaryIndex struct {
	tree *btree.Tree
	name string
}

// New creates a SecondaryIndex backed by a B+tree with the given root page ID.
// If root is 0, the index is empty.
func New(name string, pool *bufpool.Pool, fl btree.Allocator, root uint64) *SecondaryIndex {
	return &SecondaryIndex{
		tree: btree.New(root, pool, fl),
		name: name,
	}
}

// Name returns the index name (e.g., "by_module", "by_tag").
func (idx *SecondaryIndex) Name() string {
	return idx.name
}

// Root returns the root page ID of the index B+tree.
func (idx *SecondaryIndex) Root() uint64 {
	return idx.tree.Root()
}

// Update removes old composite entries and inserts new ones for the given
// primary key. Both oldKeys and newKeys are the index field values (e.g.,
// module names or tags). This must be called within the same write
// transaction as the primary mutation.
func (idx *SecondaryIndex) Update(primaryKey []byte, oldKeys, newKeys [][]byte) error {
	// Remove old entries.
	for _, ok := range oldKeys {
		ck := EncodeCompositeKey(ok, primaryKey)
		if err := idx.tree.Delete(ck); err != nil && err != btree.ErrKeyNotFound {
			return err
		}
	}

	// Insert new entries (empty value — the composite key is the data).
	for _, nk := range newKeys {
		ck := EncodeCompositeKey(nk, primaryKey)
		if err := idx.tree.Put(ck, nil); err != nil {
			return err
		}
	}

	return nil
}

// Insert adds composite entries for the given primary key without removing
// any old entries. Use this when creating a new record.
func (idx *SecondaryIndex) Insert(primaryKey []byte, keys [][]byte) error {
	for _, k := range keys {
		ck := EncodeCompositeKey(k, primaryKey)
		if err := idx.tree.Put(ck, nil); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes composite entries for the given primary key.
func (idx *SecondaryIndex) Delete(primaryKey []byte, keys [][]byte) error {
	for _, k := range keys {
		ck := EncodeCompositeKey(k, primaryKey)
		if err := idx.tree.Delete(ck); err != nil && err != btree.ErrKeyNotFound {
			return err
		}
	}
	return nil
}

// Scan returns all primary keys whose index field matches the given value.
// The returned primary keys are sorted by composite key order.
func (idx *SecondaryIndex) Scan(indexKey []byte) ([][]byte, error) {
	prefix := make([]byte, len(indexKey)+1)
	copy(prefix, indexKey)
	prefix[len(indexKey)] = 0 // null separator

	var results [][]byte

	cur := idx.tree.Cursor()
	if !cur.Seek(prefix) {
		return nil, nil
	}

	for cur.Valid() {
		k := cur.Key()
		if !bytes.HasPrefix(k, prefix) {
			break
		}

		_, pk := DecodeCompositeKey(k)
		pkCopy := make([]byte, len(pk))
		copy(pkCopy, pk)
		results = append(results, pkCopy)

		if !cur.Next() {
			break
		}
	}

	return results, nil
}

// ScanAll returns all (indexKey, primaryKey) pairs in the index.
// Primarily useful for testing and diagnostics.
func (idx *SecondaryIndex) ScanAll() ([][2][]byte, error) {
	var results [][2][]byte

	cur := idx.tree.Cursor()
	if !cur.First() {
		return nil, nil
	}

	for cur.Valid() {
		k := cur.Key()
		ik, pk := DecodeCompositeKey(k)

		ikCopy := make([]byte, len(ik))
		copy(ikCopy, ik)
		pkCopy := make([]byte, len(pk))
		copy(pkCopy, pk)

		results = append(results, [2][]byte{ikCopy, pkCopy})

		if !cur.Next() {
			break
		}
	}

	return results, nil
}

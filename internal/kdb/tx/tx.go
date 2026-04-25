// Package tx implements read and write transactions for kdb.
//
// Transactions provide isolated access to the B+tree through the buffer pool.
// ReadTx offers read-only access via a snapshot of the meta page. WriteTx
// extends ReadTx with mutation support: each Put/Delete is logged to the WAL,
// mutates the B+tree, and on Commit flushes dirty pages and swaps the meta.
//
// Concurrency contract (Phase 0 §4.6): a single Update (WriteTx) is active at
// a time, enforced by a writer mutex. View (ReadTx) readers are concurrent but
// serialised against writers via an RWMutex.
//
// Phase 0 — Task T10.
package tx

import (
	"errors"

	"github.com/jenaiz/pcke/internal/kdb/btree"
	"github.com/jenaiz/pcke/internal/kdb/bufpool"
	"github.com/jenaiz/pcke/internal/kdb/wal"
)

// Sentinel errors.
var (
	// ErrTxClosed indicates the transaction has already been committed or rolled back.
	ErrTxClosed = errors.New("tx: transaction is closed")

	// ErrReadOnly indicates a write operation was attempted on a read-only transaction.
	ErrReadOnly = errors.New("tx: read-only transaction")
)

// ReadTx provides read-only access to the B+tree.
type ReadTx struct {
	tree   *btree.Tree
	closed bool
}

// NewReadTx creates a read-only transaction.
func NewReadTx(tree *btree.Tree) *ReadTx {
	return &ReadTx{tree: tree}
}

// Get looks up a key. Returns btree.ErrKeyNotFound if absent.
func (tx *ReadTx) Get(key []byte) ([]byte, error) {
	if tx.closed {
		return nil, ErrTxClosed
	}
	return tx.tree.Get(key)
}

// Close marks the transaction as closed. Idempotent.
func (tx *ReadTx) Close() {
	tx.closed = true
}

// WriteTx provides read-write access to the B+tree with WAL integration.
type WriteTx struct {
	ReadTx
	wal       *wal.WAL
	pool      *bufpool.Pool
	committed bool
}

// NewWriteTx creates a read-write transaction.
func NewWriteTx(tree *btree.Tree, w *wal.WAL, pool *bufpool.Pool) *WriteTx {
	return &WriteTx{
		ReadTx: ReadTx{tree: tree},
		wal:    w,
		pool:   pool,
	}
}

// Put inserts or updates a key-value pair. The operation is logged to the WAL
// before mutating the B+tree.
func (tx *WriteTx) Put(key, value []byte) error {
	if tx.closed {
		return ErrTxClosed
	}

	// WAL: log the insert.
	payload := encodeKV(key, value)
	if _, err := tx.wal.Append(wal.TypeInsert, payload); err != nil {
		return err
	}

	return tx.tree.Put(key, value)
}

// Delete removes a key. The operation is logged to the WAL before mutating.
func (tx *WriteTx) Delete(key []byte) error {
	if tx.closed {
		return ErrTxClosed
	}

	// WAL: log the delete.
	if _, err := tx.wal.Append(wal.TypeDelete, key); err != nil {
		return err
	}

	return tx.tree.Delete(key)
}

// Commit flushes dirty pages, writes a WAL commit record, and returns.
// The caller (DB.Update) handles the meta swap.
func (tx *WriteTx) Commit() error {
	if tx.closed {
		return ErrTxClosed
	}

	// WAL commit marker.
	if _, err := tx.wal.Append(wal.TypeCommit, nil); err != nil {
		return err
	}

	// Flush all dirty pages to disk.
	if err := tx.pool.FlushDirty(); err != nil {
		return err
	}

	tx.committed = true
	tx.closed = true
	return nil
}

// Rollback discards changes. Dirty pages in the buffer pool are not flushed;
// they will be re-read from disk on the next access after eviction.
func (tx *WriteTx) Rollback() {
	tx.closed = true
}

// Committed reports whether the transaction was committed.
func (tx *WriteTx) Committed() bool {
	return tx.committed
}

// encodeKV encodes a key-value pair as [keyLen(4) | key | value].
func encodeKV(key, value []byte) []byte {
	buf := make([]byte, 4+len(key)+len(value))
	buf[0] = byte(len(key) >> 24) //nolint:gosec // G115: len(key) bounded by maxKeySize.
	buf[1] = byte(len(key) >> 16) //nolint:gosec // G115
	buf[2] = byte(len(key) >> 8)  //nolint:gosec // G115
	buf[3] = byte(len(key))       //nolint:gosec // G115
	copy(buf[4:], key)
	copy(buf[4+len(key):], value)
	return buf
}

// DecodeKV decodes a key-value pair from the WAL payload format.
func DecodeKV(payload []byte) (key, value []byte) {
	if len(payload) < 4 {
		return nil, nil
	}
	kl := int(payload[0])<<24 | int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
	if 4+kl > len(payload) {
		return nil, nil
	}
	key = payload[4 : 4+kl]
	value = payload[4+kl:]
	return key, value
}

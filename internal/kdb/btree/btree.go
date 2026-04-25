// Package btree implements a B+tree persisted on kdb pages.
//
// Each node occupies exactly one page (4096 bytes). Internal nodes store sorted
// keys with child page IDs. Leaf nodes store sorted key-value pairs and are
// linked via a next-leaf pointer for sequential scans.
//
// Values larger than ~½ page are stored in overflow page chains (page.TypeOverflow).
//
// The tree uses freelist.Alloc/Free for page management and bufpool.Pin/Unpin/
// MarkDirty for I/O. Splits are 50/50; merges are deferred to Phase 1 (F1.T11).
//
// Phase 0 — Task T7.
package btree

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/jenaiz/pcke/internal/kdb/bufpool"
	"github.com/jenaiz/pcke/internal/kdb/encoding"
	"github.com/jenaiz/pcke/internal/kdb/page"
)

// Allocator abstracts page allocation for the B+tree. Both the bootstrap
// linked-list freelist and the B+tree-based freelist implement this interface.
type Allocator interface {
	Alloc() (uint64, error)
	Free(pageID uint64) error
}

// Sentinel errors.
var (
	// ErrKeyNotFound indicates the key does not exist in the tree.
	ErrKeyNotFound = errors.New("btree: key not found")

	// ErrKeyTooLarge indicates the key exceeds the maximum allowed size.
	ErrKeyTooLarge = errors.New("btree: key too large")

	// ErrEmptyKey indicates an empty key was provided.
	ErrEmptyKey = errors.New("btree: empty key")
)

// Node layout constants.
//
// Leaf node data area (after 24-byte page header):
//
//	Offset  Size  Field
//	------  ----  -----
//	24       8    NextLeaf   (page ID of next leaf, 0 = none)
//	32       2    Count      (number of key-value pairs)
//	34       N    Cells      (count × cell: keyLen(2) + valLen(2) + key + value)
//
// Internal node data area (Convention A):
//
//	Offset  Size  Field
//	------  ----  -----
//	24       8    FirstChild (leftmost child page ID)
//	32       2    Count      (number of separator keys)
//	34       N    Cells      (count × cell: keyLen(2) + key + childPageID(8))
//
// Each cell[i] stores (key_i, rightChild_i). The child to the LEFT of key[0]
// is FirstChild. The child to the RIGHT of key[i] is cell[i].childPageID.
//
// For leaf cells, if valLen has the high bit set (0x8000), the value is stored
// in an overflow chain. The actual valLen in that case is the remaining 15 bits,
// and the first 8 bytes of the cell value area are the overflow page ID.
const (
	// offNextLeaf is the offset of the next-leaf pointer in leaf nodes.
	offNextLeaf = page.HeaderSize // 24

	// offFirstChild is the offset of the leftmost child pointer in internal nodes.
	offFirstChild = page.HeaderSize // 24

	// offCount is the offset of the cell count (shared between leaf and internal).
	offCount = page.HeaderSize + 8 // 32

	// offCells is the start of the cell array.
	offCells = offCount + 2 // 34

	// maxCellArea is the space available for cells.
	maxCellArea = page.Size - offCells // 4062

	// leafCellOverhead is the per-cell overhead in a leaf: keyLen(2) + valLen(2).
	leafCellOverhead = 4

	// internalCellOverhead is the per-cell overhead in internal node: keyLen(2) + childPageID(8).
	internalCellOverhead = 10

	// overflowFlag is set in the valLen field to indicate an overflow chain.
	overflowFlag = 0x8000

	// maxKeySize is the maximum key size. Must fit in a single internal cell
	// with enough room for at least 2 cells per node (for splits to work).
	// (maxCellArea / 2) - internalCellOverhead = 2021
	maxKeySize = (maxCellArea / 2) - internalCellOverhead

	// overflowThreshold is the maximum inline value size. Values larger than
	// this are stored in overflow pages.
	// We allow inline values up to ~half the cell area minus overhead to ensure
	// at least 2 leaf cells per node.
	overflowThreshold = (maxCellArea / 2) - leafCellOverhead - maxKeySize

	// overflowPayload is the amount of data per overflow page.
	overflowPayload = page.UsableSize - 8 // 4064 bytes (8 bytes for next-page pointer)
)

// Tree is a B+tree backed by kdb pages.
type Tree struct {
	root  uint64
	pool  *bufpool.Pool
	fl    Allocator
	count int64 // total key count (approximate, updated on put/delete)
}

// New creates a new Tree with the given root page ID. If root is 0, the tree
// is empty and a root leaf will be created on the first Put.
func New(root uint64, pool *bufpool.Pool, fl Allocator) *Tree {
	return &Tree{
		root: root,
		pool: pool,
		fl:   fl,
	}
}

// Root returns the current root page ID.
func (t *Tree) Root() uint64 {
	return t.root
}

// Get looks up a key and returns its value. Returns ErrKeyNotFound if the key
// does not exist.
func (t *Tree) Get(key []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, ErrEmptyKey
	}
	if t.root == 0 {
		return nil, ErrKeyNotFound
	}

	leafID, err := t.findLeaf(key)
	if err != nil {
		return nil, err
	}

	frame, err := t.pool.Pin(leafID)
	if err != nil {
		return nil, fmt.Errorf("btree: pin leaf %d: %w", leafID, err)
	}

	idx, found := leafSearch(frame.Buf, key)
	if !found {
		t.pool.Unpin(leafID)
		return nil, ErrKeyNotFound
	}

	val, isOverflow, overflowID := leafCellValue(frame.Buf, idx)
	if !isOverflow {
		// Return a copy so the caller doesn't hold a reference to the buffer.
		result := make([]byte, len(val))
		copy(result, val)
		t.pool.Unpin(leafID)
		return result, nil
	}

	// val contains [pageID(8) + totalLen(4)]; copy before unpin.
	inlineCopy := make([]byte, len(val))
	copy(inlineCopy, val)
	t.pool.Unpin(leafID)

	return t.readOverflow(overflowID, inlineCopy)
}

// Put inserts or updates a key-value pair in the tree.
func (t *Tree) Put(key, value []byte) error {
	if len(key) == 0 {
		return ErrEmptyKey
	}
	if len(key) > maxKeySize {
		return ErrKeyTooLarge
	}

	// Create root leaf if empty.
	if t.root == 0 {
		rootID, err := t.allocPage()
		if err != nil {
			return fmt.Errorf("btree: alloc root: %w", err)
		}
		frame, err := t.pool.Pin(rootID)
		if err != nil {
			return fmt.Errorf("btree: pin new root: %w", err)
		}
		initLeafNode(frame.Buf)
		t.pool.MarkDirty(rootID)
		t.pool.Unpin(rootID)
		t.root = rootID
	}

	splitKey, splitChild, err := t.insert(t.root, key, value)
	if err != nil {
		return err
	}

	// If insert caused the root to split, create a new root.
	if splitKey != nil {
		newRootID, err := t.allocPage()
		if err != nil {
			return fmt.Errorf("btree: alloc new root: %w", err)
		}
		frame, err := t.pool.Pin(newRootID)
		if err != nil {
			return fmt.Errorf("btree: pin new root: %w", err)
		}
		initInternalNode(frame.Buf, t.root)
		internalInsertCell(frame.Buf, 0, splitKey, splitChild)
		t.pool.MarkDirty(newRootID)
		t.pool.Unpin(newRootID)
		t.root = newRootID
	}

	return nil
}

// Delete removes a key from the tree. Returns ErrKeyNotFound if the key does
// not exist. Merges/redistribution are deferred to Phase 1.
func (t *Tree) Delete(key []byte) error {
	if len(key) == 0 {
		return ErrEmptyKey
	}
	if t.root == 0 {
		return ErrKeyNotFound
	}

	return t.delete(t.root, key)
}

// insert recursively inserts into the subtree rooted at pageID.
// Returns (splitKey, splitChildPageID) if the node was split, or (nil, 0).
func (t *Tree) insert(pageID uint64, key, value []byte) ([]byte, uint64, error) {
	frame, err := t.pool.Pin(pageID)
	if err != nil {
		return nil, 0, fmt.Errorf("btree: pin %d: %w", pageID, err)
	}

	pt := page.GetType(frame.Buf)
	t.pool.Unpin(pageID)

	switch pt {
	case page.TypeLeaf:
		return t.insertLeaf(pageID, key, value)
	case page.TypeInternal:
		return t.insertInternal(pageID, key, value)
	default:
		return nil, 0, fmt.Errorf("btree: unexpected page type %s at page %d", pt, pageID)
	}
}

// insertLeaf inserts a key-value pair into a leaf node.
func (t *Tree) insertLeaf(pageID uint64, key, value []byte) ([]byte, uint64, error) {
	frame, err := t.pool.Pin(pageID)
	if err != nil {
		return nil, 0, fmt.Errorf("btree: pin leaf %d: %w", pageID, err)
	}

	idx, found := leafSearch(frame.Buf, key)

	// Handle overflow value.
	inlineVal, overflowID, err := t.prepareValue(value)
	if err != nil {
		t.pool.Unpin(pageID)
		return nil, 0, err
	}

	isOverflow := overflowID != 0

	if found {
		// Update existing: free old overflow if any, replace cell.
		_, oldIsOverflow, oldOverflowID := leafCellValue(frame.Buf, idx)
		if oldIsOverflow {
			if err := t.freeOverflow(oldOverflowID); err != nil {
				t.pool.Unpin(pageID)
				return nil, 0, err
			}
		}
		leafDeleteCell(frame.Buf, idx)
		if leafHasSpace(frame.Buf, key, inlineVal) {
			leafInsertCell(frame.Buf, idx, key, inlineVal, isOverflow)
			t.pool.MarkDirty(pageID)
			t.pool.Unpin(pageID)
			return nil, 0, nil
		}
		// Doesn't fit after delete+reinsert, need to split (rare case).
		// Re-add and fall through to split below.
	} else {
		// Check if it fits.
		if leafHasSpace(frame.Buf, key, inlineVal) {
			leafInsertCell(frame.Buf, idx, key, inlineVal, isOverflow)
			t.pool.MarkDirty(pageID)
			t.pool.Unpin(pageID)
			t.count++
			return nil, 0, nil
		}
	}

	// Need to split. Insert temporarily into a virtual list, then split 50/50.
	t.pool.Unpin(pageID)
	return t.splitLeaf(pageID, key, inlineVal, isOverflow)
}

// insertInternal inserts into an internal node's appropriate child subtree.
func (t *Tree) insertInternal(pageID uint64, key, value []byte) ([]byte, uint64, error) {
	frame, err := t.pool.Pin(pageID)
	if err != nil {
		return nil, 0, fmt.Errorf("btree: pin internal %d: %w", pageID, err)
	}

	childIdx := internalSearch(frame.Buf, key)
	childID := internalChildAt(frame.Buf, childIdx)
	t.pool.Unpin(pageID)

	splitKey, splitChild, err := t.insert(childID, key, value)
	if err != nil {
		return nil, 0, err
	}

	if splitKey == nil {
		return nil, 0, nil
	}

	// Child was split; insert the promoted key into this internal node.
	frame, err = t.pool.Pin(pageID)
	if err != nil {
		return nil, 0, fmt.Errorf("btree: pin internal %d: %w", pageID, err)
	}

	if internalHasSpace(frame.Buf, splitKey) {
		insertIdx, _ := internalKeySearch(frame.Buf, splitKey)
		// Shift right child: the new splitChild becomes child for keys >= splitKey.
		internalInsertCell(frame.Buf, insertIdx, splitKey, splitChild)
		t.pool.MarkDirty(pageID)
		t.pool.Unpin(pageID)
		return nil, 0, nil
	}

	// Need to split the internal node.
	t.pool.Unpin(pageID)
	return t.splitInternal(pageID, splitKey, splitChild)
}

// delete recursively deletes a key from the subtree rooted at pageID.
func (t *Tree) delete(pageID uint64, key []byte) error {
	frame, err := t.pool.Pin(pageID)
	if err != nil {
		return fmt.Errorf("btree: pin %d: %w", pageID, err)
	}

	pt := page.GetType(frame.Buf)
	t.pool.Unpin(pageID)

	switch pt {
	case page.TypeLeaf:
		return t.deleteLeaf(pageID, key)
	case page.TypeInternal:
		return t.deleteInternal(pageID, key)
	default:
		return fmt.Errorf("btree: unexpected page type %s at page %d", pt, pageID)
	}
}

// deleteLeaf removes a key from a leaf node.
func (t *Tree) deleteLeaf(pageID uint64, key []byte) error {
	frame, err := t.pool.Pin(pageID)
	if err != nil {
		return fmt.Errorf("btree: pin leaf %d: %w", pageID, err)
	}
	defer t.pool.Unpin(pageID)

	idx, found := leafSearch(frame.Buf, key)
	if !found {
		return ErrKeyNotFound
	}

	// Free overflow if present.
	_, isOverflow, overflowID := leafCellValue(frame.Buf, idx)
	if isOverflow {
		if err := t.freeOverflow(overflowID); err != nil {
			return err
		}
	}

	leafDeleteCell(frame.Buf, idx)
	t.pool.MarkDirty(pageID)
	t.count--

	return nil
}

// deleteInternal routes delete to the appropriate child.
func (t *Tree) deleteInternal(pageID uint64, key []byte) error {
	frame, err := t.pool.Pin(pageID)
	if err != nil {
		return fmt.Errorf("btree: pin internal %d: %w", pageID, err)
	}

	childIdx := internalSearch(frame.Buf, key)
	childID := internalChildAt(frame.Buf, childIdx)
	t.pool.Unpin(pageID)

	return t.delete(childID, key)
}

// findLeaf traverses the tree from root to find the leaf page containing key.
func (t *Tree) findLeaf(key []byte) (uint64, error) {
	pageID := t.root

	for {
		frame, err := t.pool.Pin(pageID)
		if err != nil {
			return 0, fmt.Errorf("btree: pin %d: %w", pageID, err)
		}

		pt := page.GetType(frame.Buf)
		if pt == page.TypeLeaf {
			t.pool.Unpin(pageID)
			return pageID, nil
		}

		childIdx := internalSearch(frame.Buf, key)
		nextID := internalChildAt(frame.Buf, childIdx)
		t.pool.Unpin(pageID)
		pageID = nextID
	}
}

// allocPage allocates a new page from the freelist and returns its ID.
func (t *Tree) allocPage() (uint64, error) {
	id, err := t.fl.Alloc()
	if err != nil {
		return 0, fmt.Errorf("btree: alloc page: %w", err)
	}
	return id, nil
}

// freePage returns a page to the freelist.
func (t *Tree) freePage(pageID uint64) error {
	return t.fl.Free(pageID)
}

// ── Leaf node operations ──

// initLeafNode initialises a page buffer as an empty leaf node.
func initLeafNode(buf []byte) {
	page.Init(buf, page.TypeLeaf, 0)
	// NextLeaf = 0 (already zeroed by Init).
	// Count = 0 (already zeroed).
}

// leafCount returns the number of cells in a leaf node.
func leafCount(buf []byte) int {
	return int(encoding.Uint16(buf[offCount:]))
}

// setLeafCount sets the cell count of a leaf node.
func setLeafCount(buf []byte, n int) {
	encoding.PutUint16(buf[offCount:], uint16(n)) //nolint:gosec
}

// leafNextLeaf returns the next-leaf page ID.
func leafNextLeaf(buf []byte) uint64 {
	return encoding.Uint64(buf[offNextLeaf:])
}

// setLeafNextLeaf sets the next-leaf page ID.
func setLeafNextLeaf(buf []byte, id uint64) {
	encoding.PutUint64(buf[offNextLeaf:], id)
}

// leafCellOffset returns the byte offset of cell i in the data area.
func leafCellOffset(buf []byte, i int) int {
	off := offCells
	for j := range i {
		kl := int(encoding.Uint16(buf[off:]))
		vl := int(encoding.Uint16(buf[off+2:])) & 0x7FFF
		off += leafCellOverhead + kl + vl
		_ = j
	}
	return off
}

// leafCellKey returns the key at cell index i.
func leafCellKey(buf []byte, i int) []byte {
	off := leafCellOffset(buf, i)
	kl := int(encoding.Uint16(buf[off:]))
	return buf[off+leafCellOverhead : off+leafCellOverhead+kl]
}

// leafCellValue returns the value at cell index i, along with overflow info.
// If isOverflow is true, overflowID is the first overflow page ID and val
// contains the inline portion: [pageID(8) + totalLen(4)] = 12 bytes.
func leafCellValue(buf []byte, i int) (val []byte, isOverflow bool, overflowID uint64) {
	off := leafCellOffset(buf, i)
	kl := int(encoding.Uint16(buf[off:]))
	vlRaw := encoding.Uint16(buf[off+2:])
	isOverflow = vlRaw&overflowFlag != 0
	vl := int(vlRaw & 0x7FFF)
	valStart := off + leafCellOverhead + kl
	val = buf[valStart : valStart+vl]
	if isOverflow && vl >= 12 {
		overflowID = encoding.Uint64(val[:8])
	}
	return val, isOverflow, overflowID
}

// leafCellSize returns the total size of a cell with the given key and value.
func leafCellSize(keyLen, valLen int) int {
	return leafCellOverhead + keyLen + valLen
}

// leafUsedSpace returns the total bytes used by cells in the leaf.
func leafUsedSpace(buf []byte) int {
	count := leafCount(buf)
	if count == 0 {
		return 0
	}
	// Walk to the end of the last cell.
	off := offCells
	for range count {
		kl := int(encoding.Uint16(buf[off:]))
		vl := int(encoding.Uint16(buf[off+2:])) & 0x7FFF
		off += leafCellOverhead + kl + vl
	}
	return off - offCells
}

// leafHasSpace reports whether a new cell with the given key and value fits.
func leafHasSpace(buf []byte, key, val []byte) bool {
	used := leafUsedSpace(buf)
	needed := leafCellSize(len(key), len(val))
	return used+needed <= maxCellArea
}

// leafSearch performs binary search on a leaf's keys. Returns the index where
// key should be and whether it was found.
func leafSearch(buf []byte, key []byte) (int, bool) {
	count := leafCount(buf)
	lo, hi := 0, count
	for lo < hi {
		mid := lo + (hi-lo)/2
		k := leafCellKey(buf, mid)
		cmp := bytes.Compare(k, key)
		if cmp == 0 {
			return mid, true
		}
		if cmp < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo, false
}

// leafInsertCell inserts a cell at index i, shifting subsequent cells right.
func leafInsertCell(buf []byte, i int, key, val []byte, isOverflow bool) {
	count := leafCount(buf)
	insertOff := leafCellOffset(buf, i)
	endOff := leafCellOffset(buf, count)
	cellSize := leafCellSize(len(key), len(val))

	// Shift data right.
	copy(buf[insertOff+cellSize:endOff+cellSize], buf[insertOff:endOff])

	// Write cell header.
	encoding.PutUint16(buf[insertOff:], uint16(len(key))) //nolint:gosec
	vlField := uint16(len(val))                           //nolint:gosec
	if isOverflow {
		vlField |= overflowFlag
	}
	encoding.PutUint16(buf[insertOff+2:], vlField)

	// Write key and value.
	copy(buf[insertOff+leafCellOverhead:], key)
	copy(buf[insertOff+leafCellOverhead+len(key):], val)

	setLeafCount(buf, count+1)
}

// leafDeleteCell removes the cell at index i, shifting subsequent cells left.
func leafDeleteCell(buf []byte, i int) {
	count := leafCount(buf)
	cellOff := leafCellOffset(buf, i)
	kl := int(encoding.Uint16(buf[cellOff:]))
	vl := int(encoding.Uint16(buf[cellOff+2:])) & 0x7FFF
	cellSize := leafCellOverhead + kl + vl
	endOff := leafCellOffset(buf, count)

	// Shift data left.
	copy(buf[cellOff:], buf[cellOff+cellSize:endOff])

	// Zero the freed space.
	clear(buf[endOff-cellSize : endOff])

	setLeafCount(buf, count-1)
}

// ── Internal node operations ──

// initInternalNode initialises a page buffer as an internal node.
// firstChild is the leftmost child (for keys less than any separator key).
func initInternalNode(buf []byte, firstChild uint64) {
	page.Init(buf, page.TypeInternal, 0)
	encoding.PutUint64(buf[offFirstChild:], firstChild)
	// Count = 0: no separator keys yet, just the leftmost child.
}

// internalCount returns the number of separator keys.
func internalCount(buf []byte) int {
	return int(encoding.Uint16(buf[offCount:]))
}

// setInternalCount sets the separator key count.
func setInternalCount(buf []byte, n int) {
	encoding.PutUint16(buf[offCount:], uint16(n)) //nolint:gosec
}

// internalFirstChild returns the leftmost child page ID.
func internalFirstChild(buf []byte) uint64 {
	return encoding.Uint64(buf[offFirstChild:])
}

// setInternalFirstChild sets the leftmost child page ID.
//
//nolint:unused // Will be used by T8 (freelist B+tree migration).
func setInternalFirstChild(buf []byte, id uint64) {
	encoding.PutUint64(buf[offFirstChild:], id)
}

// internalCellOffset returns the byte offset of cell i.
func internalCellOffset(buf []byte, i int) int {
	off := offCells
	for j := range i {
		kl := int(encoding.Uint16(buf[off:]))
		off += internalCellOverhead + kl
		_ = j
	}
	return off
}

// internalCellKey returns the separator key at index i.
func internalCellKey(buf []byte, i int) []byte {
	off := internalCellOffset(buf, i)
	kl := int(encoding.Uint16(buf[off:]))
	return buf[off+2 : off+2+kl]
}

// internalCellChild returns the child page ID at index i (the left child of key i).
func internalCellChild(buf []byte, i int) uint64 {
	off := internalCellOffset(buf, i)
	kl := int(encoding.Uint16(buf[off:]))
	return encoding.Uint64(buf[off+2+kl:])
}

// internalKeySearch performs binary search on internal node keys.
// Returns (index, found) where index is the position for insertion.
func internalKeySearch(buf []byte, key []byte) (int, bool) {
	count := internalCount(buf)
	lo, hi := 0, count
	for lo < hi {
		mid := lo + (hi-lo)/2
		k := internalCellKey(buf, mid)
		cmp := bytes.Compare(k, key)
		if cmp == 0 {
			return mid, true
		}
		if cmp < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo, false
}

// internalSearch returns the child index for the given key.
// Child index i means: follow the child pointer of cell i (for i < count),
// or the rightmost child (for i == count).
func internalSearch(buf []byte, key []byte) int {
	count := internalCount(buf)
	lo, hi := 0, count
	for lo < hi {
		mid := lo + (hi-lo)/2
		k := internalCellKey(buf, mid)
		cmp := bytes.Compare(key, k)
		if cmp < 0 {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// internalChildAt returns the child page ID for the given search index.
// idx 0 returns firstChild; idx i (i>0) returns cell[i-1].child (right child of key[i-1]).
func internalChildAt(buf []byte, idx int) uint64 {
	if idx == 0 {
		return internalFirstChild(buf)
	}
	return internalCellChild(buf, idx-1)
}

// internalHasSpace reports whether a new separator key fits in the internal node.
func internalHasSpace(buf []byte, key []byte) bool {
	used := internalUsedSpace(buf)
	needed := internalCellOverhead + len(key)
	return used+needed <= maxCellArea
}

// internalUsedSpace returns the total bytes used by cells.
func internalUsedSpace(buf []byte) int {
	count := internalCount(buf)
	if count == 0 {
		return 0
	}
	off := offCells
	for range count {
		kl := int(encoding.Uint16(buf[off:]))
		off += internalCellOverhead + kl
	}
	return off - offCells
}

// internalInsertCell inserts a separator key and its right child at index i.
//
// Layout: each cell is [keyLen(2)] [key(keyLen)] [childPageID(8)].
// The child in cell[i] is the child to the RIGHT of key[i].
func internalInsertCell(buf []byte, i int, key []byte, childPageID uint64) {
	count := internalCount(buf)
	insertOff := internalCellOffset(buf, i)
	endOff := internalCellOffset(buf, count)
	cellSize := internalCellOverhead + len(key)

	// Shift data right.
	copy(buf[insertOff+cellSize:endOff+cellSize], buf[insertOff:endOff])

	// Write cell.
	encoding.PutUint16(buf[insertOff:], uint16(len(key))) //nolint:gosec
	copy(buf[insertOff+2:], key)
	encoding.PutUint64(buf[insertOff+2+len(key):], childPageID)

	setInternalCount(buf, count+1)
}

// internalDeleteCell removes the cell at index i.
//
//nolint:unused // Will be used by F1.T11 (merges/redistribution).
func internalDeleteCell(buf []byte, i int) {
	count := internalCount(buf)
	cellOff := internalCellOffset(buf, i)
	kl := int(encoding.Uint16(buf[cellOff:]))
	cellSize := internalCellOverhead + kl
	endOff := internalCellOffset(buf, count)

	copy(buf[cellOff:], buf[cellOff+cellSize:endOff])
	clear(buf[endOff-cellSize : endOff])
	setInternalCount(buf, count-1)
}

// ── Overflow pages ──

// prepareValue determines inline value and overflow page ID for a Put.
// If value fits inline, returns (value, 0, nil).
// If overflow is needed, writes overflow chain and returns (12-byte inline [pageID+len], overflowID, nil).
func (t *Tree) prepareValue(value []byte) (inlineVal []byte, overflowID uint64, err error) {
	if len(value) <= overflowThreshold {
		return value, 0, nil
	}

	firstID, err := t.writeOverflow(value)
	if err != nil {
		return nil, 0, err
	}

	// Inline portion: 8 bytes page ID + 4 bytes total length.
	inline := make([]byte, 12)
	encoding.PutUint64(inline, firstID)
	encoding.PutUint32(inline[8:], uint32(len(value))) //nolint:gosec
	return inline, firstID, nil
}

// writeOverflow writes a value to a chain of overflow pages and returns the
// first page ID.
func (t *Tree) writeOverflow(value []byte) (uint64, error) {
	var firstID uint64
	var prevFrame *bufpool.Frame
	var prevID uint64

	remaining := value
	for len(remaining) > 0 {
		pgID, err := t.allocPage()
		if err != nil {
			return 0, fmt.Errorf("btree: alloc overflow: %w", err)
		}

		frame, err := t.pool.Pin(pgID)
		if err != nil {
			return 0, fmt.Errorf("btree: pin overflow %d: %w", pgID, err)
		}

		page.Init(frame.Buf, page.TypeOverflow, 0)

		chunk := remaining
		if len(chunk) > overflowPayload {
			chunk = chunk[:overflowPayload]
		}
		remaining = remaining[len(chunk):]

		// Next pointer is at offset 24 (page header).
		// Set to 0 for now; updated if there's another page.
		encoding.PutUint64(frame.Buf[page.HeaderSize:], 0)
		copy(frame.Buf[page.HeaderSize+8:], chunk)

		t.pool.MarkDirty(pgID)

		if firstID == 0 {
			firstID = pgID
		}

		// Link previous page to this one.
		if prevFrame != nil {
			encoding.PutUint64(prevFrame.Buf[page.HeaderSize:], pgID)
			t.pool.MarkDirty(prevID)
			t.pool.Unpin(prevID)
		}

		prevFrame = frame
		prevID = pgID
	}

	if prevFrame != nil {
		t.pool.Unpin(prevID)
	}

	return firstID, nil
}

// readOverflow reads a complete value from an overflow chain.
// inlineVal contains [pageID(8) + totalLen(4)].
func (t *Tree) readOverflow(firstID uint64, inlineVal []byte) ([]byte, error) {
	totalLen := int(encoding.Uint32(inlineVal[8:12]))
	result := make([]byte, 0, totalLen)
	pgID := firstID

	for pgID != 0 && len(result) < totalLen {
		frame, err := t.pool.Pin(pgID)
		if err != nil {
			return nil, fmt.Errorf("btree: pin overflow %d: %w", pgID, err)
		}

		nextID := encoding.Uint64(frame.Buf[page.HeaderSize:])
		payload := frame.Buf[page.HeaderSize+8:]

		need := totalLen - len(result)
		if need > len(payload) {
			need = len(payload)
		}
		result = append(result, payload[:need]...)

		t.pool.Unpin(pgID)
		pgID = nextID
	}

	return result, nil
}

// freeOverflow frees all pages in an overflow chain.
func (t *Tree) freeOverflow(firstID uint64) error {
	pgID := firstID

	for pgID != 0 {
		frame, err := t.pool.Pin(pgID)
		if err != nil {
			return fmt.Errorf("btree: pin overflow %d for free: %w", pgID, err)
		}

		nextID := encoding.Uint64(frame.Buf[page.HeaderSize:])
		t.pool.Unpin(pgID)

		if err := t.freePage(pgID); err != nil {
			return fmt.Errorf("btree: free overflow page %d: %w", pgID, err)
		}

		pgID = nextID
	}

	return nil
}

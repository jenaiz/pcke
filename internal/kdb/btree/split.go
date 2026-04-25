package btree

import (
	"fmt"
)

// leafCell is an in-memory representation of a leaf cell used during splits.
type leafCell struct {
	key        []byte
	val        []byte
	isOverflow bool
}

// internalCell is an in-memory representation of an internal cell used during splits.
type internalCell struct {
	key     []byte
	childID uint64
}

// splitLeaf splits a full leaf node and inserts the new key-value pair.
// Returns the promoted key and the new right sibling page ID.
func (t *Tree) splitLeaf(pageID uint64, key, val []byte, isOverflow bool) ([]byte, uint64, error) {
	checkCrashHook("btree-pre-split-leaf")

	frame, err := t.pool.Pin(pageID)
	if err != nil {
		return nil, 0, fmt.Errorf("btree: pin leaf for split %d: %w", pageID, err)
	}

	// Collect all existing cells.
	count := leafCount(frame.Buf)
	cells := make([]leafCell, 0, count+1)
	for i := range count {
		k := leafCellKey(frame.Buf, i)
		v, ov, _ := leafCellValue(frame.Buf, i)
		// Copy key and value out of the buffer.
		kc := make([]byte, len(k))
		copy(kc, k)
		vc := make([]byte, len(v))
		copy(vc, v)
		cells = append(cells, leafCell{key: kc, val: vc, isOverflow: ov})
	}

	// Remember next-leaf.
	nextLeaf := leafNextLeaf(frame.Buf)
	t.pool.Unpin(pageID)

	// Insert new cell into sorted position.
	insertIdx := 0
	for insertIdx < len(cells) {
		if string(key) <= string(cells[insertIdx].key) {
			break
		}
		insertIdx++
	}

	// Check if it's an update (same key).
	isUpdate := insertIdx < len(cells) && string(cells[insertIdx].key) == string(key)
	if isUpdate {
		cells[insertIdx].val = val
		cells[insertIdx].isOverflow = isOverflow
	} else {
		cells = append(cells, leafCell{})
		copy(cells[insertIdx+1:], cells[insertIdx:])
		cells[insertIdx] = leafCell{key: key, val: val, isOverflow: isOverflow}
		t.count++
	}

	// Split 50/50 by cell count.
	mid := len(cells) / 2

	// Allocate new right sibling.
	rightID, err := t.allocPage()
	if err != nil {
		return nil, 0, fmt.Errorf("btree: alloc right leaf: %w", err)
	}

	// Rewrite left page.
	frame, err = t.pool.Pin(pageID)
	if err != nil {
		return nil, 0, fmt.Errorf("btree: pin left leaf: %w", err)
	}
	initLeafNode(frame.Buf)
	setLeafNextLeaf(frame.Buf, rightID)
	for _, c := range cells[:mid] {
		idx := leafCount(frame.Buf)
		leafInsertCell(frame.Buf, idx, c.key, c.val, c.isOverflow)
	}
	t.pool.MarkDirty(pageID)
	t.pool.Unpin(pageID)

	// Write right page.
	rFrame, err := t.pool.Pin(rightID)
	if err != nil {
		return nil, 0, fmt.Errorf("btree: pin right leaf: %w", err)
	}
	initLeafNode(rFrame.Buf)
	setLeafNextLeaf(rFrame.Buf, nextLeaf)
	for _, c := range cells[mid:] {
		idx := leafCount(rFrame.Buf)
		leafInsertCell(rFrame.Buf, idx, c.key, c.val, c.isOverflow)
	}
	t.pool.MarkDirty(rightID)
	t.pool.Unpin(rightID)

	// Promote the first key of the right sibling.
	promotedKey := make([]byte, len(cells[mid].key))
	copy(promotedKey, cells[mid].key)

	return promotedKey, rightID, nil
}

// splitInternal splits a full internal node and inserts the new separator key.
// Returns the promoted key and the new right sibling page ID.
//
// Convention A: firstChild is the leftmost child, each cell stores (key, rightChild).
func (t *Tree) splitInternal(pageID uint64, newKey []byte, newChild uint64) ([]byte, uint64, error) {
	checkCrashHook("btree-pre-split-internal")

	frame, err := t.pool.Pin(pageID)
	if err != nil {
		return nil, 0, fmt.Errorf("btree: pin internal for split %d: %w", pageID, err)
	}

	// Collect firstChild and all cells.
	origFirstChild := internalFirstChild(frame.Buf)
	count := internalCount(frame.Buf)
	cells := make([]internalCell, 0, count+1)
	for i := range count {
		k := internalCellKey(frame.Buf, i)
		c := internalCellChild(frame.Buf, i)
		kc := make([]byte, len(k))
		copy(kc, k)
		cells = append(cells, internalCell{key: kc, childID: c})
	}
	t.pool.Unpin(pageID)

	// Insert new cell at sorted position.
	insertIdx := 0
	for insertIdx < len(cells) {
		if string(newKey) <= string(cells[insertIdx].key) {
			break
		}
		insertIdx++
	}
	cells = append(cells, internalCell{})
	copy(cells[insertIdx+1:], cells[insertIdx:])
	cells[insertIdx] = internalCell{key: newKey, childID: newChild}

	// Split: the middle key is promoted, not kept in either node.
	mid := len(cells) / 2
	promotedKey := make([]byte, len(cells[mid].key))
	copy(promotedKey, cells[mid].key)

	leftCells := cells[:mid]
	rightCells := cells[mid+1:] // skip promoted key

	// Right node's firstChild = the promoted key's right child.
	rightFirstChild := cells[mid].childID

	// Allocate new right sibling.
	newRightID, err := t.allocPage()
	if err != nil {
		return nil, 0, fmt.Errorf("btree: alloc right internal: %w", err)
	}

	// Rewrite left page (keeps original firstChild).
	frame, err = t.pool.Pin(pageID)
	if err != nil {
		return nil, 0, fmt.Errorf("btree: pin left internal: %w", err)
	}
	initInternalNode(frame.Buf, origFirstChild)
	for i, c := range leftCells {
		internalInsertCell(frame.Buf, i, c.key, c.childID)
	}
	t.pool.MarkDirty(pageID)
	t.pool.Unpin(pageID)

	// Write right page (firstChild = promoted key's right child).
	rFrame, err := t.pool.Pin(newRightID)
	if err != nil {
		return nil, 0, fmt.Errorf("btree: pin right internal: %w", err)
	}
	initInternalNode(rFrame.Buf, rightFirstChild)
	for i, c := range rightCells {
		internalInsertCell(rFrame.Buf, i, c.key, c.childID)
	}
	t.pool.MarkDirty(newRightID)
	t.pool.Unpin(newRightID)

	return promotedKey, newRightID, nil
}

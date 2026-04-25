package btree

import (
	"fmt"

	"github.com/jenaiz/pcke/internal/kdb/page"
)

// underflowThreshold is the minimum used space (in bytes) for a node to be
// considered balanced. A node using less than this is a candidate for
// redistribution or merging. Set to 25% of the cell area.
const underflowThreshold = maxCellArea / 4

// leafUnderflow reports whether a leaf node is underflowing (sparse enough
// that it should be merged or redistributed with a sibling).
func leafUnderflow(buf []byte) bool {
	return leafCount(buf) > 0 && leafUsedSpace(buf) < underflowThreshold
}

// internalUnderflow reports whether an internal node is underflowing.
func internalUnderflow(buf []byte) bool {
	return internalCount(buf) > 0 && internalUsedSpace(buf) < underflowThreshold
}

// rebalanceLeaf attempts to fix an underflowing leaf child. parentID is the
// parent internal node page, and childIdx is the child slot index (0-based)
// pointing to the underflowing leaf.
//
// Returns true if the parent itself is now underflowing (needs further
// rebalancing up the tree).
func (t *Tree) rebalanceLeaf(parentID uint64, childIdx int) (bool, error) {
	pFrame, err := t.pool.Pin(parentID)
	if err != nil {
		return false, fmt.Errorf("btree: pin parent %d: %w", parentID, err)
	}

	pCount := internalCount(pFrame.Buf)
	childID := internalChildAt(pFrame.Buf, childIdx)

	// Try right sibling first, then left.
	if childIdx < pCount {
		rightID := internalChildAt(pFrame.Buf, childIdx+1)
		// Separator between childAt(childIdx) and childAt(childIdx+1) is key[childIdx].
		t.pool.Unpin(parentID)
		return t.tryLeafMergeOrRedist(parentID, childID, rightID, childIdx, true)
	}

	if childIdx > 0 {
		leftID := internalChildAt(pFrame.Buf, childIdx-1)
		t.pool.Unpin(parentID)
		return t.tryLeafMergeOrRedist(parentID, leftID, childID, childIdx-1, false)
	}

	t.pool.Unpin(parentID)
	return false, nil
}

// tryLeafMergeOrRedist tries to redistribute or merge two adjacent leaf
// siblings. sepIdx is the index of the separator key in the parent between
// leftID and rightID. If rightIsTarget is true, the right leaf is the
// underflowing one; otherwise the left is.
func (t *Tree) tryLeafMergeOrRedist(parentID, leftID, rightID uint64, sepIdx int, rightIsTarget bool) (bool, error) {
	// Read both leaves.
	lFrame, err := t.pool.Pin(leftID)
	if err != nil {
		return false, fmt.Errorf("btree: pin left leaf %d: %w", leftID, err)
	}
	rFrame, err := t.pool.Pin(rightID)
	if err != nil {
		t.pool.Unpin(leftID)
		return false, fmt.Errorf("btree: pin right leaf %d: %w", rightID, err)
	}

	lUsed := leafUsedSpace(lFrame.Buf)
	rUsed := leafUsedSpace(rFrame.Buf)

	// Can we merge? Both fit in one page?
	if lUsed+rUsed <= maxCellArea {
		t.pool.Unpin(leftID)
		t.pool.Unpin(rightID)
		return t.mergeLeaves(parentID, leftID, rightID, sepIdx)
	}

	// Try redistribution.
	t.pool.Unpin(leftID)
	t.pool.Unpin(rightID)

	if rightIsTarget {
		return false, t.redistributeLeafRight(parentID, leftID, rightID, sepIdx)
	}
	return false, t.redistributeLeafLeft(parentID, leftID, rightID, sepIdx)
}

// mergeLeaves merges rightID into leftID and removes the separator from
// the parent. Returns true if the parent is now underflowing.
func (t *Tree) mergeLeaves(parentID, leftID, rightID uint64, sepIdx int) (bool, error) {
	// Collect all cells from right leaf.
	rFrame, err := t.pool.Pin(rightID)
	if err != nil {
		return false, fmt.Errorf("btree: pin right leaf %d: %w", rightID, err)
	}
	rCount := leafCount(rFrame.Buf)
	rightCells := make([]leafCell, rCount)
	for i := range rCount {
		k := leafCellKey(rFrame.Buf, i)
		v, ov, _ := leafCellValue(rFrame.Buf, i)
		rightCells[i] = leafCell{
			key:        append([]byte(nil), k...),
			val:        append([]byte(nil), v...),
			isOverflow: ov,
		}
	}
	rightNextLeaf := leafNextLeaf(rFrame.Buf)
	t.pool.Unpin(rightID)

	// Append right cells to left leaf.
	lFrame, err := t.pool.Pin(leftID)
	if err != nil {
		return false, fmt.Errorf("btree: pin left leaf %d: %w", leftID, err)
	}
	for _, c := range rightCells {
		idx := leafCount(lFrame.Buf)
		leafInsertCell(lFrame.Buf, idx, c.key, c.val, c.isOverflow)
	}
	// Update next-leaf pointer to skip over the removed right leaf.
	setLeafNextLeaf(lFrame.Buf, rightNextLeaf)
	t.pool.MarkDirty(leftID)
	t.pool.Unpin(leftID)

	// Free the right leaf page.
	if err := t.freePage(rightID); err != nil {
		return false, fmt.Errorf("btree: free merged leaf %d: %w", rightID, err)
	}

	// Remove the separator key from the parent.
	pFrame, err := t.pool.Pin(parentID)
	if err != nil {
		return false, fmt.Errorf("btree: pin parent %d: %w", parentID, err)
	}
	internalDeleteCell(pFrame.Buf, sepIdx)
	t.pool.MarkDirty(parentID)

	parentUnderflow := internalUnderflow(pFrame.Buf)
	t.pool.Unpin(parentID)

	return parentUnderflow, nil
}

// redistributeLeafRight moves one or more cells from the left leaf to the
// right leaf (which is underflowing) and updates the parent separator.
func (t *Tree) redistributeLeafRight(parentID, leftID, rightID uint64, sepIdx int) error {
	// Collect all cells from both leaves.
	lFrame, err := t.pool.Pin(leftID)
	if err != nil {
		return fmt.Errorf("btree: pin left leaf %d: %w", leftID, err)
	}
	lCount := leafCount(lFrame.Buf)
	var allCells []leafCell
	for i := range lCount {
		k := leafCellKey(lFrame.Buf, i)
		v, ov, _ := leafCellValue(lFrame.Buf, i)
		allCells = append(allCells, leafCell{
			key:        append([]byte(nil), k...),
			val:        append([]byte(nil), v...),
			isOverflow: ov,
		})
	}
	leftNextLeaf := leafNextLeaf(lFrame.Buf) // should be rightID
	t.pool.Unpin(leftID)

	rFrame, err := t.pool.Pin(rightID)
	if err != nil {
		return fmt.Errorf("btree: pin right leaf %d: %w", rightID, err)
	}
	rCount := leafCount(rFrame.Buf)
	for i := range rCount {
		k := leafCellKey(rFrame.Buf, i)
		v, ov, _ := leafCellValue(rFrame.Buf, i)
		allCells = append(allCells, leafCell{
			key:        append([]byte(nil), k...),
			val:        append([]byte(nil), v...),
			isOverflow: ov,
		})
	}
	rightNextLeaf := leafNextLeaf(rFrame.Buf)
	t.pool.Unpin(rightID)

	// Split 50/50.
	mid := len(allCells) / 2

	// Rewrite left leaf.
	lFrame, err = t.pool.Pin(leftID)
	if err != nil {
		return fmt.Errorf("btree: pin left leaf %d: %w", leftID, err)
	}
	initLeafNode(lFrame.Buf)
	setLeafNextLeaf(lFrame.Buf, leftNextLeaf)
	for _, c := range allCells[:mid] {
		idx := leafCount(lFrame.Buf)
		leafInsertCell(lFrame.Buf, idx, c.key, c.val, c.isOverflow)
	}
	t.pool.MarkDirty(leftID)
	t.pool.Unpin(leftID)

	// Rewrite right leaf.
	rFrame, err = t.pool.Pin(rightID)
	if err != nil {
		return fmt.Errorf("btree: pin right leaf %d: %w", rightID, err)
	}
	initLeafNode(rFrame.Buf)
	setLeafNextLeaf(rFrame.Buf, rightNextLeaf)
	for _, c := range allCells[mid:] {
		idx := leafCount(rFrame.Buf)
		leafInsertCell(rFrame.Buf, idx, c.key, c.val, c.isOverflow)
	}
	t.pool.MarkDirty(rightID)
	t.pool.Unpin(rightID)

	// Update parent separator to the first key of the right leaf.
	newSep := make([]byte, len(allCells[mid].key))
	copy(newSep, allCells[mid].key)
	return t.updateParentSeparator(parentID, sepIdx, newSep)
}

// redistributeLeafLeft moves cells from the right leaf to the left leaf
// (which is underflowing) and updates the parent separator.
func (t *Tree) redistributeLeafLeft(parentID, leftID, rightID uint64, sepIdx int) error {
	// Same logic as redistributeLeafRight — collect all, split 50/50.
	return t.redistributeLeafRight(parentID, leftID, rightID, sepIdx)
}

// updateParentSeparator replaces the separator key at sepIdx in the parent.
func (t *Tree) updateParentSeparator(parentID uint64, sepIdx int, newKey []byte) error {
	pFrame, err := t.pool.Pin(parentID)
	if err != nil {
		return fmt.Errorf("btree: pin parent %d: %w", parentID, err)
	}

	// Get the child pointer that was associated with the old separator.
	childID := internalCellChild(pFrame.Buf, sepIdx)

	// Delete old separator and insert new one.
	internalDeleteCell(pFrame.Buf, sepIdx)
	internalInsertCell(pFrame.Buf, sepIdx, newKey, childID)

	t.pool.MarkDirty(parentID)
	t.pool.Unpin(parentID)
	return nil
}

// rebalanceInternal attempts to fix an underflowing internal child node.
// parentID is the parent, childIdx is the slot index of the underflowing child.
// Returns true if the parent is now underflowing.
func (t *Tree) rebalanceInternal(parentID uint64, childIdx int) (bool, error) {
	pFrame, err := t.pool.Pin(parentID)
	if err != nil {
		return false, fmt.Errorf("btree: pin parent %d: %w", parentID, err)
	}

	pCount := internalCount(pFrame.Buf)
	childID := internalChildAt(pFrame.Buf, childIdx)

	// Try right sibling first, then left.
	if childIdx < pCount {
		rightID := internalChildAt(pFrame.Buf, childIdx+1)
		t.pool.Unpin(parentID)
		return t.tryInternalMergeOrRedist(parentID, childID, rightID, childIdx)
	}

	if childIdx > 0 {
		leftID := internalChildAt(pFrame.Buf, childIdx-1)
		t.pool.Unpin(parentID)
		return t.tryInternalMergeOrRedist(parentID, leftID, childID, childIdx-1)
	}

	t.pool.Unpin(parentID)
	return false, nil
}

// tryInternalMergeOrRedist tries to merge or redistribute two adjacent
// internal siblings.
func (t *Tree) tryInternalMergeOrRedist(parentID, leftID, rightID uint64, sepIdx int) (bool, error) {
	lFrame, err := t.pool.Pin(leftID)
	if err != nil {
		return false, fmt.Errorf("btree: pin left internal %d: %w", leftID, err)
	}
	rFrame, err := t.pool.Pin(rightID)
	if err != nil {
		t.pool.Unpin(leftID)
		return false, fmt.Errorf("btree: pin right internal %d: %w", rightID, err)
	}

	// Read the separator key from parent.
	pFrame, err := t.pool.Pin(parentID)
	if err != nil {
		t.pool.Unpin(leftID)
		t.pool.Unpin(rightID)
		return false, fmt.Errorf("btree: pin parent %d: %w", parentID, err)
	}
	sepKey := append([]byte(nil), internalCellKey(pFrame.Buf, sepIdx)...)
	t.pool.Unpin(parentID)

	lUsed := internalUsedSpace(lFrame.Buf)
	rUsed := internalUsedSpace(rFrame.Buf)
	sepSize := internalCellOverhead + len(sepKey)

	t.pool.Unpin(leftID)
	t.pool.Unpin(rightID)

	// Can we merge? Left cells + separator + right cells must fit.
	if lUsed+rUsed+sepSize <= maxCellArea {
		return t.mergeInternal(parentID, leftID, rightID, sepIdx, sepKey)
	}

	// Redistribution for internal nodes: collect all cells, split 50/50.
	return false, t.redistributeInternal(parentID, leftID, rightID, sepIdx, sepKey)
}

// mergeInternal merges rightID into leftID, pulling the separator key down
// from the parent. Returns true if the parent is now underflowing.
func (t *Tree) mergeInternal(parentID, leftID, rightID uint64, sepIdx int, sepKey []byte) (bool, error) {
	// Collect all cells from left.
	lFrame, err := t.pool.Pin(leftID)
	if err != nil {
		return false, fmt.Errorf("btree: pin left internal %d: %w", leftID, err)
	}
	lCount := internalCount(lFrame.Buf)
	leftFirst := internalFirstChild(lFrame.Buf)
	var allCells []internalCell
	for i := range lCount {
		k := append([]byte(nil), internalCellKey(lFrame.Buf, i)...)
		c := internalCellChild(lFrame.Buf, i)
		allCells = append(allCells, internalCell{key: k, childID: c})
	}
	t.pool.Unpin(leftID)

	// The separator key comes down with right's firstChild as its child.
	rFrame, err := t.pool.Pin(rightID)
	if err != nil {
		return false, fmt.Errorf("btree: pin right internal %d: %w", rightID, err)
	}
	rFirst := internalFirstChild(rFrame.Buf)
	rCount := internalCount(rFrame.Buf)
	allCells = append(allCells, internalCell{key: sepKey, childID: rFirst})
	for i := range rCount {
		k := append([]byte(nil), internalCellKey(rFrame.Buf, i)...)
		c := internalCellChild(rFrame.Buf, i)
		allCells = append(allCells, internalCell{key: k, childID: c})
	}
	t.pool.Unpin(rightID)

	// Rewrite left node with all cells.
	lFrame, err = t.pool.Pin(leftID)
	if err != nil {
		return false, fmt.Errorf("btree: pin left internal %d: %w", leftID, err)
	}
	initInternalNode(lFrame.Buf, leftFirst)
	for i, c := range allCells {
		internalInsertCell(lFrame.Buf, i, c.key, c.childID)
	}
	t.pool.MarkDirty(leftID)
	t.pool.Unpin(leftID)

	// Free right page.
	if err := t.freePage(rightID); err != nil {
		return false, fmt.Errorf("btree: free merged internal %d: %w", rightID, err)
	}

	// Remove separator from parent.
	pFrame, err := t.pool.Pin(parentID)
	if err != nil {
		return false, fmt.Errorf("btree: pin parent %d: %w", parentID, err)
	}
	internalDeleteCell(pFrame.Buf, sepIdx)
	t.pool.MarkDirty(parentID)
	parentUnderflow := internalUnderflow(pFrame.Buf)
	t.pool.Unpin(parentID)

	return parentUnderflow, nil
}

// redistributeInternal redistributes cells between two internal siblings.
func (t *Tree) redistributeInternal(parentID, leftID, rightID uint64, sepIdx int, sepKey []byte) error {
	// Collect all cells: left + separator + right.
	lFrame, err := t.pool.Pin(leftID)
	if err != nil {
		return fmt.Errorf("btree: pin left internal %d: %w", leftID, err)
	}
	leftFirst := internalFirstChild(lFrame.Buf)
	lCount := internalCount(lFrame.Buf)
	var allCells []internalCell
	for i := range lCount {
		k := append([]byte(nil), internalCellKey(lFrame.Buf, i)...)
		c := internalCellChild(lFrame.Buf, i)
		allCells = append(allCells, internalCell{key: k, childID: c})
	}
	t.pool.Unpin(leftID)

	rFrame, err := t.pool.Pin(rightID)
	if err != nil {
		return fmt.Errorf("btree: pin right internal %d: %w", rightID, err)
	}
	rFirst := internalFirstChild(rFrame.Buf)
	rCount := internalCount(rFrame.Buf)
	allCells = append(allCells, internalCell{key: sepKey, childID: rFirst})
	for i := range rCount {
		k := append([]byte(nil), internalCellKey(rFrame.Buf, i)...)
		c := internalCellChild(rFrame.Buf, i)
		allCells = append(allCells, internalCell{key: k, childID: c})
	}
	t.pool.Unpin(rightID)

	// Split: middle key is promoted to parent.
	mid := len(allCells) / 2
	newSepKey := append([]byte(nil), allCells[mid].key...)
	newRightFirst := allCells[mid].childID
	leftCells := allCells[:mid]
	rightCells := allCells[mid+1:]

	// Rewrite left.
	lFrame, err = t.pool.Pin(leftID)
	if err != nil {
		return fmt.Errorf("btree: pin left internal %d: %w", leftID, err)
	}
	initInternalNode(lFrame.Buf, leftFirst)
	for i, c := range leftCells {
		internalInsertCell(lFrame.Buf, i, c.key, c.childID)
	}
	t.pool.MarkDirty(leftID)
	t.pool.Unpin(leftID)

	// Rewrite right.
	rFrame, err = t.pool.Pin(rightID)
	if err != nil {
		return fmt.Errorf("btree: pin right internal %d: %w", rightID, err)
	}
	initInternalNode(rFrame.Buf, newRightFirst)
	for i, c := range rightCells {
		internalInsertCell(rFrame.Buf, i, c.key, c.childID)
	}
	t.pool.MarkDirty(rightID)
	t.pool.Unpin(rightID)

	// Update parent separator.
	return t.updateParentSeparator(parentID, sepIdx, newSepKey)
}

// collapseRoot shrinks the tree height by one if the root is an internal
// node with no separator keys (only a firstChild pointer).
func (t *Tree) collapseRoot() error {
	if t.root == 0 {
		return nil
	}

	frame, err := t.pool.Pin(t.root)
	if err != nil {
		return fmt.Errorf("btree: pin root %d: %w", t.root, err)
	}

	pt := page.GetType(frame.Buf)
	if pt != page.TypeInternal {
		t.pool.Unpin(t.root)
		return nil
	}

	if internalCount(frame.Buf) > 0 {
		t.pool.Unpin(t.root)
		return nil
	}

	// Root has 0 keys but a firstChild — collapse.
	newRoot := internalFirstChild(frame.Buf)
	t.pool.Unpin(t.root)

	oldRoot := t.root
	t.root = newRoot

	if err := t.freePage(oldRoot); err != nil {
		return fmt.Errorf("btree: free old root %d: %w", oldRoot, err)
	}

	return nil
}

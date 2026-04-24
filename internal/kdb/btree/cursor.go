package btree

import (
	"fmt"

	"github.com/jenaiz/pcke/internal/kdb/page"
)

// Cursor provides ordered iteration over the key-value pairs in a B+tree.
// It walks the leaf chain using the next-leaf pointers.
//
// A Cursor must be positioned before use via First() or Seek(). After
// positioning, call Key()/Value() to read and Next() to advance.
type Cursor struct {
	tree   *Tree
	leafID uint64 // current leaf page ID (0 = invalid)
	index  int    // cell index within current leaf
	valid  bool   // false after exhaustion or before positioning
}

// Cursor returns a new cursor for ordered iteration.
func (t *Tree) Cursor() *Cursor {
	return &Cursor{tree: t}
}

// First positions the cursor at the first (smallest) key. Returns false if
// the tree is empty.
func (c *Cursor) First() bool {
	if c.tree.root == 0 {
		c.valid = false
		return false
	}

	leafID, err := c.findLeftmost(c.tree.root)
	if err != nil {
		c.valid = false
		return false
	}

	c.leafID = leafID
	c.index = 0

	// Check if the leaf is empty.
	frame, err := c.tree.pool.Pin(c.leafID)
	if err != nil {
		c.valid = false
		return false
	}
	count := leafCount(frame.Buf)
	c.tree.pool.Unpin(c.leafID)

	c.valid = count > 0
	return c.valid
}

// Seek positions the cursor at the first key >= the given key. Returns false
// if no such key exists.
func (c *Cursor) Seek(key []byte) bool {
	if c.tree.root == 0 {
		c.valid = false
		return false
	}

	leafID, err := c.tree.findLeaf(key)
	if err != nil {
		c.valid = false
		return false
	}

	frame, err := c.tree.pool.Pin(leafID)
	if err != nil {
		c.valid = false
		return false
	}
	count := leafCount(frame.Buf)
	idx, _ := leafSearch(frame.Buf, key)
	c.tree.pool.Unpin(leafID)

	c.leafID = leafID
	c.index = idx

	// If idx is past the end of this leaf, advance to next leaf.
	if idx >= count {
		return c.advanceLeaf()
	}

	c.valid = true
	return true
}

// Next advances the cursor to the next key. Returns false if exhausted.
func (c *Cursor) Next() bool {
	if !c.valid {
		return false
	}

	c.index++

	// Check if we need to move to the next leaf.
	frame, err := c.tree.pool.Pin(c.leafID)
	if err != nil {
		c.valid = false
		return false
	}
	count := leafCount(frame.Buf)
	c.tree.pool.Unpin(c.leafID)

	if c.index < count {
		return true
	}

	return c.advanceLeaf()
}

// Key returns the key at the current cursor position. Returns nil if the
// cursor is invalid. The returned slice is a copy.
func (c *Cursor) Key() []byte {
	if !c.valid {
		return nil
	}

	frame, err := c.tree.pool.Pin(c.leafID)
	if err != nil {
		return nil
	}
	defer c.tree.pool.Unpin(c.leafID)

	k := leafCellKey(frame.Buf, c.index)
	result := make([]byte, len(k))
	copy(result, k)
	return result
}

// Value returns the value at the current cursor position. Returns nil if the
// cursor is invalid. For overflow values, the full chain is read. The returned
// slice is a copy.
func (c *Cursor) Value() []byte {
	if !c.valid {
		return nil
	}

	frame, err := c.tree.pool.Pin(c.leafID)
	if err != nil {
		return nil
	}

	val, isOverflow, overflowID := leafCellValue(frame.Buf, c.index)

	if !isOverflow {
		result := make([]byte, len(val))
		copy(result, val)
		c.tree.pool.Unpin(c.leafID)
		return result
	}

	inlineCopy := make([]byte, len(val))
	copy(inlineCopy, val)
	c.tree.pool.Unpin(c.leafID)

	result, err := c.tree.readOverflow(overflowID, inlineCopy)
	if err != nil {
		return nil
	}
	return result
}

// Valid reports whether the cursor is positioned at a valid entry.
func (c *Cursor) Valid() bool {
	return c.valid
}

// advanceLeaf moves to the next leaf via the next-leaf pointer.
func (c *Cursor) advanceLeaf() bool {
	frame, err := c.tree.pool.Pin(c.leafID)
	if err != nil {
		c.valid = false
		return false
	}
	nextID := leafNextLeaf(frame.Buf)
	c.tree.pool.Unpin(c.leafID)

	if nextID == 0 {
		c.valid = false
		return false
	}

	c.leafID = nextID
	c.index = 0

	// Verify the next leaf is non-empty.
	frame, err = c.tree.pool.Pin(c.leafID)
	if err != nil {
		c.valid = false
		return false
	}
	count := leafCount(frame.Buf)
	c.tree.pool.Unpin(c.leafID)

	c.valid = count > 0
	return c.valid
}

// findLeftmost traverses from pageID to the leftmost leaf.
func (c *Cursor) findLeftmost(pageID uint64) (uint64, error) {
	for {
		frame, err := c.tree.pool.Pin(pageID)
		if err != nil {
			return 0, fmt.Errorf("btree: cursor pin %d: %w", pageID, err)
		}

		pt := page.GetType(frame.Buf)
		if pt == page.TypeLeaf {
			c.tree.pool.Unpin(pageID)
			return pageID, nil
		}

		// Internal node: follow the leftmost child (firstChild).
		childID := internalFirstChild(frame.Buf)
		c.tree.pool.Unpin(pageID)
		pageID = childID
	}
}

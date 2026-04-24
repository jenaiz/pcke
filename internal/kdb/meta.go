// Package kdb — meta.go implements the double-meta page scheme.
//
// Layout in the .kdb file:
//
//	Page 0 (offset 0):     Meta-A
//	Page 1 (offset 4096):  Meta-B
//
// Each meta page uses the standard 24-byte page header (Type = TypeMeta)
// followed by a meta-specific payload:
//
//	Offset  Size  Field
//	------  ----  -----
//	24       4    Version        (format version, currently 1)
//	28       8    Generation     (monotonically increasing; higher = newer)
//	36       8    PageCount      (total pages in the data file)
//	44       8    FreelistRoot   (page ID of first freelist page, 0 = none)
//	52       1    FreelistFormat (0 = linked-list/T4, 1 = B+tree/T8)
//	53       3    Reserved       (must be 0)
//
// Atomic swap protocol:
//  1. Write the INACTIVE meta page (the one with the lower generation).
//  2. fsync.
//  3. On recovery, pick the meta with the highest valid generation (CRC ok).
//
// This guarantees that at least one meta page is always valid after a crash.
//
// Phase 0 — Task T5.
package kdb

import (
	"fmt"
	"os"

	"github.com/jenaiz/pcke/internal/kdb/encoding"
	"github.com/jenaiz/pcke/internal/kdb/page"
)

const (
	// MetaVersion is the current meta page format version.
	MetaVersion uint32 = 1

	// metaSlotA is the page index of meta slot A.
	metaSlotA = 0
	// metaSlotB is the page index of meta slot B.
	metaSlotB = 1

	// Meta payload offsets (relative to start of page, after the 24-byte header).
	metaOffVersion        = page.HeaderSize
	metaOffGeneration     = metaOffVersion + 4
	metaOffPageCount      = metaOffGeneration + 8
	metaOffFreelistRoot   = metaOffPageCount + 8
	metaOffFreelistFormat = metaOffFreelistRoot + 8
)

// FreelistFormat indicates the freelist storage strategy.
type FreelistFormat uint8

const (
	// FreelistLinkedList is the bootstrap linked-list format (T4).
	FreelistLinkedList FreelistFormat = 0
	// FreelistBTree is the B+tree format (T8).
	FreelistBTree FreelistFormat = 1
)

// Meta holds the decoded contents of a meta page.
type Meta struct {
	Version        uint32
	Generation     uint64
	PageCount      uint64
	FreelistRoot   uint64
	FreelistFormat FreelistFormat
}

// encodeMeta writes m into a 4096-byte page buffer as a TypeMeta page with
// LSN 0. The buffer must be exactly page.Size bytes.
func encodeMeta(buf []byte, m *Meta) {
	page.Init(buf, page.TypeMeta, 0)

	encoding.PutUint32(buf[metaOffVersion:], m.Version)
	encoding.PutUint64(buf[metaOffGeneration:], m.Generation)
	encoding.PutUint64(buf[metaOffPageCount:], m.PageCount)
	encoding.PutUint64(buf[metaOffFreelistRoot:], m.FreelistRoot)
	buf[metaOffFreelistFormat] = byte(m.FreelistFormat)

	// Recompute checksum after writing payload.
	page.SetChecksum(buf)
}

// decodeMeta reads a Meta from a 4096-byte page buffer.
// It verifies the page checksum and type before decoding.
func decodeMeta(buf []byte) (*Meta, error) {
	if err := page.Verify(buf); err != nil {
		return nil, fmt.Errorf("meta: %w", err)
	}

	if pt := page.GetType(buf); pt != page.TypeMeta {
		return nil, fmt.Errorf("meta: unexpected page type %s, want Meta", pt)
	}

	return &Meta{
		Version:        encoding.Uint32(buf[metaOffVersion:]),
		Generation:     encoding.Uint64(buf[metaOffGeneration:]),
		PageCount:      encoding.Uint64(buf[metaOffPageCount:]),
		FreelistRoot:   encoding.Uint64(buf[metaOffFreelistRoot:]),
		FreelistFormat: FreelistFormat(buf[metaOffFreelistFormat]),
	}, nil
}

// readMetaPage reads a single meta page from the file at the given slot (0 or 1).
func readMetaPage(f *os.File, slot int) ([]byte, error) {
	buf := make([]byte, page.Size)
	offset := int64(slot) * int64(page.Size)

	if _, err := f.ReadAt(buf, offset); err != nil {
		return nil, fmt.Errorf("meta: read slot %d: %w", slot, err)
	}

	return buf, nil
}

// writeMetaPage writes a meta page to the file at the given slot and fsyncs.
func writeMetaPage(f *os.File, slot int, buf []byte) error {
	offset := int64(slot) * int64(page.Size)

	if _, err := f.WriteAt(buf, offset); err != nil {
		return fmt.Errorf("meta: write slot %d: %w", slot, err)
	}

	if err := f.Sync(); err != nil {
		return fmt.Errorf("meta: fsync after write slot %d: %w", slot, err)
	}

	return nil
}

// loadMeta reads both meta pages and returns the one with the highest valid
// generation. If both are invalid, it returns an error. If exactly one is
// valid, that one is returned.
func loadMeta(f *os.File) (*Meta, error) {
	var metas [2]*Meta

	for i := range 2 {
		buf, err := readMetaPage(f, i)
		if err != nil {
			continue
		}
		m, err := decodeMeta(buf)
		if err != nil {
			continue
		}
		metas[i] = m
	}

	a, b := metas[metaSlotA], metas[metaSlotB]

	switch {
	case a == nil && b == nil:
		return nil, fmt.Errorf("meta: no valid meta page found: %w", page.ErrChecksumMismatch)
	case a == nil:
		return b, nil
	case b == nil:
		return a, nil
	default:
		if b.Generation > a.Generation {
			return b, nil
		}
		return a, nil
	}
}

// initMeta writes initial meta pages (generation 1) to both slots of a new
// database file. The pageCount should reflect the total pages after initial
// allocation.
func initMeta(f *os.File, pageCount uint64) error {
	m := &Meta{
		Version:        MetaVersion,
		Generation:     1,
		PageCount:      pageCount,
		FreelistRoot:   0,
		FreelistFormat: FreelistLinkedList,
	}

	buf := make([]byte, page.Size)

	// Write both slots with the same initial meta.
	for _, slot := range []int{metaSlotA, metaSlotB} {
		encodeMeta(buf, m)
		if err := writeMetaPage(f, slot, buf); err != nil {
			return fmt.Errorf("meta: init slot %d: %w", slot, err)
		}
	}

	return nil
}

// swapMeta performs an atomic meta page swap:
//  1. Reads both meta pages to determine the active (highest generation) one.
//  2. Writes the new meta to the INACTIVE slot (lower generation).
//  3. Fsyncs.
//
// After a successful swap, the newly written meta has the highest generation
// and will be selected on the next loadMeta call.
func swapMeta(f *os.File, m *Meta) error {
	// Determine which slot is inactive (lower generation).
	var gens [2]uint64
	var valid [2]bool

	for i := range 2 {
		buf, err := readMetaPage(f, i)
		if err != nil {
			continue
		}
		decoded, err := decodeMeta(buf)
		if err != nil {
			continue
		}
		gens[i] = decoded.Generation
		valid[i] = true
	}

	// Pick the inactive slot (lower generation). If one is invalid, pick that.
	var targetSlot int
	switch {
	case !valid[metaSlotA]:
		targetSlot = metaSlotA
	case !valid[metaSlotB]:
		targetSlot = metaSlotB
	case gens[metaSlotB] <= gens[metaSlotA]:
		targetSlot = metaSlotB
	default:
		targetSlot = metaSlotA
	}

	buf := make([]byte, page.Size)
	encodeMeta(buf, m)

	return writeMetaPage(f, targetSlot, buf)
}

// ReadMeta reads the active meta from the DB's data file. Returns ErrDBClosed
// if the database has been closed.
func (db *DB) ReadMeta() (*Meta, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return nil, ErrDBClosed
	}

	return loadMeta(db.file)
}

// WriteMeta atomically swaps a new meta page into the inactive slot.
// The generation of m must be higher than the current active generation.
// Returns ErrDBClosed if the database has been closed.
func (db *DB) WriteMeta(m *Meta) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return ErrDBClosed
	}

	return swapMeta(db.file, m)
}

// DataFile returns the underlying os.File for the data file.
// This is intended for use by internal sub-packages (e.g., meta tests)
// that need direct file access. The caller must not close the file.
func (db *DB) DataFile() *os.File {
	return db.file
}

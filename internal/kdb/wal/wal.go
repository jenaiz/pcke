// Package wal implements the write-ahead log for kdb.
//
// The WAL is a single append-only file stored in the .pcke/ directory alongside
// the data file. Each record has the format:
//
//	Field      Size   Description
//	---------  -----  -----------
//	LSN        8      Log sequence number (uint64 LE, monotonically increasing)
//	Length     4      Payload length in bytes (uint32 LE, excluding header/CRC)
//	Type       1      Record type (see RecordType)
//	Payload    N      Opaque payload (N = Length bytes)
//	CRC32C     4      CRC32C of [LSN + Length + Type + Payload]
//
// Total per-record overhead: 17 bytes (8+4+1+4).
//
// On Open, if the WAL is non-empty, Replay is called to recover committed
// operations. A corrupt tail (incomplete final record) is detected and
// truncated.
//
// Phase 0 — Task T9.
package wal

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/jenaiz/pcke/internal/kdb/encoding"
)

// Sentinel errors.
var (
	// ErrClosed indicates the WAL has been closed.
	ErrClosed = errors.New("wal: closed")

	// ErrCorruptRecord indicates a record failed CRC verification.
	ErrCorruptRecord = errors.New("wal: corrupt record")
)

// RecordType identifies the kind of WAL record.
type RecordType uint8

const (
	// TypeInsert records a key-value insertion.
	TypeInsert RecordType = iota + 1
	// TypeDelete records a key deletion.
	TypeDelete
	// TypeCommit marks the end of a transaction.
	TypeCommit
	// TypeCheckpoint marks a checkpoint boundary.
	TypeCheckpoint
)

// String returns a human-readable name for the record type.
func (rt RecordType) String() string {
	switch rt {
	case TypeInsert:
		return "Insert"
	case TypeDelete:
		return "Delete"
	case TypeCommit:
		return "Commit"
	case TypeCheckpoint:
		return "Checkpoint"
	default:
		return fmt.Sprintf("Unknown(%d)", rt)
	}
}

// Record is a single WAL entry.
type Record struct {
	LSN     uint64
	Type    RecordType
	Payload []byte
}

const (
	// headerSize is the fixed-size header: LSN(8) + Length(4) + Type(1).
	headerSize = 13

	// crcSize is the trailing CRC32C checksum.
	crcSize = 4

	// legacyWALFileName is the name of the old single-file WAL.
	legacyWALFileName = "wal.log"

	// filePerms is the permission mode for the WAL file.
	filePerms = 0o600
)

// WAL is a write-ahead log backed by numbered segment files.
//
// Each segment is named wal-XXXXXXXX.log. The WAL appends to the active
// (highest-numbered) segment. On [WAL.Rotate], a new segment is created
// and becomes the active one. [WAL.RemoveOlderSegments] deletes all
// segments except the active one — used after checkpoint.
//
// Concurrency: all methods are safe for concurrent use via an internal mutex.
type WAL struct {
	mu       sync.Mutex
	dir      string
	active   *os.File
	activeID uint64   // segment number of the active file
	segments []uint64 // all segment IDs, sorted ascending
	nextLSN  uint64
	closed   bool
}

// segmentName returns the filename for a given segment ID.
func segmentName(id uint64) string {
	return fmt.Sprintf("wal-%08d.log", id)
}

// Open opens (or creates) a WAL in the given directory. The directory
// must already exist. If a legacy single-file WAL (wal.log) exists, it
// is migrated to segment format (wal-00000001.log). If segments exist,
// the highest-numbered segment becomes the active one and is scanned
// to determine the next LSN.
func Open(dir string) (*WAL, error) {
	// Migrate legacy WAL if present.
	if err := migrateLegacy(dir); err != nil {
		return nil, err
	}

	// Discover existing segments.
	segIDs, err := discoverSegments(dir)
	if err != nil {
		return nil, err
	}

	w := &WAL{
		dir:     dir,
		nextLSN: 1,
	}

	if len(segIDs) == 0 {
		// No segments — create the first one.
		segIDs = []uint64{1}
		f, err := createSegment(dir, 1)
		if err != nil {
			return nil, err
		}
		w.active = f
		w.activeID = 1
		w.segments = segIDs
		return w, nil
	}

	w.segments = segIDs
	w.activeID = segIDs[len(segIDs)-1]

	// Scan all segments to find the highest LSN.
	for _, id := range segIDs {
		path := filepath.Join(dir, segmentName(id))
		f, err := os.OpenFile(path, os.O_RDWR, filePerms) //nolint:gosec // G304: path constructed from dir.
		if err != nil {
			return nil, fmt.Errorf("wal: open segment %d: %w", id, err)
		}
		if id == w.activeID {
			w.active = f
		} else {
			_ = f.Close()
		}
	}

	// Scan and repair the active segment (corrupt tail detection).
	if err := w.scanAllSegments(); err != nil {
		_ = w.active.Close()
		return nil, err
	}

	return w, nil
}

// Append writes a record to the active WAL segment and fsyncs. Returns the assigned LSN.
func (w *WAL) Append(rt RecordType, payload []byte) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, ErrClosed
	}

	lsn := w.nextLSN
	buf := encodeRecord(lsn, rt, payload)

	checkCrashHook("wal-pre-write")

	if _, err := w.active.Write(buf); err != nil {
		return 0, fmt.Errorf("wal: write: %w", err)
	}

	checkCrashHook("wal-post-write-pre-sync")

	if err := durableSync(w.active); err != nil {
		return 0, fmt.Errorf("wal: sync: %w", err)
	}

	checkCrashHook("wal-post-sync")

	w.nextLSN++
	return lsn, nil
}

// Replay reads all valid records from all WAL segments (oldest to newest)
// and calls fn for each one in order. Replay is deterministic: given the
// same WAL segments, it produces the same sequence of records.
func (w *WAL) Replay(fn func(Record) error) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrClosed
	}

	for _, id := range w.segments {
		if err := w.replaySegment(id, fn); err != nil {
			return err
		}
	}
	return nil
}

// NextLSN returns the next LSN that will be assigned.
func (w *WAL) NextLSN() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.nextLSN
}

// Close fsyncs and closes the active WAL segment.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true

	if err := durableSync(w.active); err != nil {
		_ = w.active.Close()
		return fmt.Errorf("wal: close sync: %w", err)
	}

	return w.active.Close()
}

// FileSize returns the total size of all WAL segment files in bytes.
func (w *WAL) FileSize() (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, ErrClosed
	}

	var total int64
	for _, id := range w.segments {
		path := filepath.Join(w.dir, segmentName(id))
		info, err := os.Stat(path)
		if err != nil {
			return 0, fmt.Errorf("wal: stat segment %d: %w", id, err)
		}
		total += info.Size()
	}
	return total, nil
}

// Truncate removes all WAL segments and creates a fresh empty active segment.
// This is called after WAL replay to prevent unbounded growth and avoid
// replaying already-recovered records on subsequent opens.
func (w *WAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrClosed
	}

	// Close the active file.
	if err := w.active.Close(); err != nil {
		return fmt.Errorf("wal: close active: %w", err)
	}

	// Remove all segment files.
	for _, id := range w.segments {
		path := filepath.Join(w.dir, segmentName(id))
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("wal: remove segment %d: %w", id, err)
		}
	}

	// Create a fresh segment.
	f, err := createSegment(w.dir, 1)
	if err != nil {
		return err
	}

	w.active = f
	w.activeID = 1
	w.segments = []uint64{1}
	w.nextLSN = 1

	return nil
}

// Rotate closes the current active segment and opens a new one.
//
// This is called at checkpoint boundaries so that the pre-checkpoint
// segments can be removed separately via [WAL.RemoveOlderSegments].
func (w *WAL) Rotate() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrClosed
	}

	// Sync and close the current active segment.
	if err := durableSync(w.active); err != nil {
		return fmt.Errorf("wal: rotate sync: %w", err)
	}
	if err := w.active.Close(); err != nil {
		return fmt.Errorf("wal: rotate close: %w", err)
	}

	// Open a new segment with the next ID.
	newID := w.activeID + 1
	f, err := createSegment(w.dir, newID)
	if err != nil {
		return err
	}

	w.active = f
	w.activeID = newID
	w.segments = append(w.segments, newID)

	return nil
}

// RemoveOlderSegments deletes all WAL segments except the active one.
//
// This is called after a checkpoint's meta swap to reclaim disk space
// from segments whose records are now durably on the data file.
func (w *WAL) RemoveOlderSegments() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrClosed
	}

	var kept []uint64
	for _, id := range w.segments {
		if id == w.activeID {
			kept = append(kept, id)
			continue
		}
		path := filepath.Join(w.dir, segmentName(id))
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("wal: remove segment %d: %w", id, err)
		}
	}
	w.segments = kept

	return nil
}

// SegmentCount returns the number of WAL segment files.
func (w *WAL) SegmentCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.segments)
}

// scanAllSegments reads all segments to find the highest LSN, and repairs
// the active segment's corrupt tail if needed. Called during Open.
func (w *WAL) scanAllSegments() error {
	// Scan older (non-active) segments for their highest LSN.
	for _, id := range w.segments {
		if id == w.activeID {
			continue
		}
		path := filepath.Join(w.dir, segmentName(id))
		f, err := os.Open(path) //nolint:gosec // G304: path from dir.
		if err != nil {
			return fmt.Errorf("wal: open segment %d for scan: %w", id, err)
		}
		highLSN := w.scanFileForLSN(f)
		_ = f.Close()
		if highLSN >= w.nextLSN {
			w.nextLSN = highLSN + 1
		}
	}

	// Scan and repair the active segment.
	return w.scanAndRepairActive()
}

// scanFileForLSN reads a file and returns the highest LSN found.
func (w *WAL) scanFileForLSN(f *os.File) uint64 {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0
	}
	var highest uint64
	for {
		rec, _, err := readOneRecord(f)
		if err != nil {
			break
		}
		if rec.LSN > highest {
			highest = rec.LSN
		}
	}
	return highest
}

// scanAndRepairActive reads the active segment to find the highest valid LSN
// and truncates any corrupt tail. Caller must hold w.mu.
func (w *WAL) scanAndRepairActive() error {
	info, err := w.active.Stat()
	if err != nil {
		return fmt.Errorf("wal: stat active: %w", err)
	}

	if info.Size() == 0 {
		return nil
	}

	if _, err := w.active.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("wal: seek active: %w", err)
	}

	var lastValidOffset int64
	offset := int64(0)

	for {
		rec, n, err := readOneRecord(w.active)
		if err != nil {
			break
		}
		if rec.LSN >= w.nextLSN {
			w.nextLSN = rec.LSN + 1
		}
		offset += int64(n)
		lastValidOffset = offset
	}

	// Truncate any corrupt tail in the active segment.
	if lastValidOffset < info.Size() {
		if err := w.active.Truncate(lastValidOffset); err != nil {
			return fmt.Errorf("wal: truncate corrupt tail: %w", err)
		}
		if err := durableSync(w.active); err != nil {
			return fmt.Errorf("wal: sync after truncate: %w", err)
		}
	}

	// Seek to end for future appends.
	if _, err := w.active.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("wal: seek to end: %w", err)
	}

	return nil
}

// replaySegment replays records from a single segment file.
func (w *WAL) replaySegment(id uint64, fn func(Record) error) error {
	if id == w.activeID {
		return replayFile(w.active, fn)
	}
	path := filepath.Join(w.dir, segmentName(id))
	f, err := os.Open(path) //nolint:gosec // G304: path from dir.
	if err != nil {
		return fmt.Errorf("wal: open segment %d for replay: %w", id, err)
	}
	defer func() { _ = f.Close() }()
	return replayFile(f, fn)
}

// migrateLegacy renames a legacy wal.log to wal-00000001.log if present.
func migrateLegacy(dir string) error {
	legacy := filepath.Join(dir, legacyWALFileName)
	if _, err := os.Stat(legacy); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	newPath := filepath.Join(dir, segmentName(1))
	return os.Rename(legacy, newPath)
}

// discoverSegments finds all wal-XXXXXXXX.log files in dir and returns
// their IDs sorted ascending.
func discoverSegments(dir string) ([]uint64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("wal: read dir: %w", err)
	}

	var ids []uint64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		var id uint64
		if _, err := fmt.Sscanf(e.Name(), "wal-%08d.log", &id); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}

	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// createSegment creates a new empty segment file and returns it open for R/W.
func createSegment(dir string, id uint64) (*os.File, error) {
	path := filepath.Join(dir, segmentName(id))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_EXCL, filePerms) //nolint:gosec // G304: path constructed from dir+segmentName.
	if err != nil {
		return nil, fmt.Errorf("wal: create segment %d: %w", id, err)
	}
	return f, nil
}

// ActiveSegmentPath returns the path to the active segment file.
// Exported for test access only.
func (w *WAL) ActiveSegmentPath() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return filepath.Join(w.dir, segmentName(w.activeID))
}

// replayFile reads all valid records from the file (from position 0) and
// calls fn for each one. Does not modify the file.
func replayFile(f *os.File, fn func(Record) error) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("wal: replay seek: %w", err)
	}

	for {
		rec, _, err := readOneRecord(f)
		if err != nil {
			// End of valid records.
			return nil
		}
		if err := fn(rec); err != nil {
			return fmt.Errorf("wal: replay callback: %w", err)
		}
	}
}

// readOneRecord reads a single record from the current file position.
// Returns the record, the number of bytes consumed, and any error.
// Returns a non-nil error on EOF, incomplete read, or CRC mismatch.
func readOneRecord(f *os.File) (Record, int, error) {
	// Read header.
	var hdr [headerSize]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return Record{}, 0, err
	}

	lsn := encoding.Uint64(hdr[0:])
	length := encoding.Uint32(hdr[8:])
	rtype := RecordType(hdr[12])

	// Sanity check: reject impossibly large payloads (> 64 MiB).
	if length > 64*1024*1024 {
		return Record{}, 0, ErrCorruptRecord
	}

	// Read payload.
	payload := make([]byte, length)
	if _, err := io.ReadFull(f, payload); err != nil {
		return Record{}, 0, err
	}

	// Read CRC.
	var crcBuf [crcSize]byte
	if _, err := io.ReadFull(f, crcBuf[:]); err != nil {
		return Record{}, 0, err
	}
	storedCRC := encoding.Uint32(crcBuf[:])

	// Verify CRC: computed over [header + payload].
	computedCRC := computeRecordCRC(hdr[:], payload)
	if storedCRC != computedCRC {
		return Record{}, 0, ErrCorruptRecord
	}

	rec := Record{
		LSN:     lsn,
		Type:    rtype,
		Payload: payload,
	}

	totalSize := headerSize + int(length) + crcSize
	return rec, totalSize, nil
}

// encodeRecord serialises a WAL record into a byte slice.
func encodeRecord(lsn uint64, rt RecordType, payload []byte) []byte {
	size := headerSize + len(payload) + crcSize
	buf := make([]byte, size)

	// Header.
	encoding.PutUint64(buf[0:], lsn)
	encoding.PutUint32(buf[8:], uint32(len(payload))) //nolint:gosec
	buf[12] = byte(rt)

	// Payload.
	copy(buf[headerSize:], payload)

	// CRC over [header + payload].
	crc := computeRecordCRC(buf[:headerSize], payload)
	encoding.PutUint32(buf[headerSize+len(payload):], crc)

	return buf
}

// computeRecordCRC computes CRC32C over the header and payload.
func computeRecordCRC(header, payload []byte) uint32 {
	crc := encoding.CRC32C(header)
	crc = encoding.UpdateCRC32C(crc, payload)
	return crc
}

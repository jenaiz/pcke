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

	// walFileName is the name of the WAL file in the .pcke directory.
	walFileName = "wal.log"

	// filePerms is the permission mode for the WAL file.
	filePerms = 0o600
)

// WAL is a write-ahead log backed by a single file.
type WAL struct {
	mu      sync.Mutex
	file    *os.File
	nextLSN uint64
	closed  bool
}

// Open opens (or creates) a WAL file in the given directory. The directory
// must already exist. If the WAL file contains data, the nextLSN is set to
// one past the highest LSN found by scanning.
func Open(dir string) (*WAL, error) {
	path := filepath.Join(dir, walFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, filePerms) //nolint:gosec // G304: path controlled by caller.
	if err != nil {
		return nil, fmt.Errorf("wal: open %s: %w", path, err)
	}

	w := &WAL{
		file:    f,
		nextLSN: 1,
	}

	// Scan existing records to find next LSN and detect corrupt tail.
	if err := w.scanAndRepair(); err != nil {
		_ = f.Close()
		return nil, err
	}

	return w, nil
}

// Append writes a record to the WAL and fsyncs. Returns the assigned LSN.
func (w *WAL) Append(rt RecordType, payload []byte) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, ErrClosed
	}

	lsn := w.nextLSN
	buf := encodeRecord(lsn, rt, payload)

	if _, err := w.file.Write(buf); err != nil {
		return 0, fmt.Errorf("wal: write: %w", err)
	}

	if err := durableSync(w.file); err != nil {
		return 0, fmt.Errorf("wal: sync: %w", err)
	}

	w.nextLSN++
	return lsn, nil
}

// Replay reads all valid records from the WAL and calls fn for each one in
// order. Replay is deterministic: given the same WAL file, it produces the
// same sequence of records.
func (w *WAL) Replay(fn func(Record) error) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrClosed
	}

	return replayFile(w.file, fn)
}

// NextLSN returns the next LSN that will be assigned.
func (w *WAL) NextLSN() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.nextLSN
}

// Close fsyncs and closes the WAL file.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true

	if err := durableSync(w.file); err != nil {
		_ = w.file.Close()
		return fmt.Errorf("wal: close sync: %w", err)
	}

	return w.file.Close()
}

// scanAndRepair reads the WAL to find the highest valid LSN and truncates
// any corrupt tail. Caller must hold w.mu (or be in Open before publishing).
func (w *WAL) scanAndRepair() error {
	info, err := w.file.Stat()
	if err != nil {
		return fmt.Errorf("wal: stat: %w", err)
	}

	if info.Size() == 0 {
		return nil // empty WAL
	}

	// Seek to beginning.
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("wal: seek: %w", err)
	}

	var lastValidOffset int64
	var highestLSN uint64

	offset := int64(0)
	for {
		rec, n, err := readOneRecord(w.file)
		if err != nil {
			// Corrupt tail or EOF — truncate here.
			break
		}
		if rec.LSN > highestLSN {
			highestLSN = rec.LSN
		}
		offset += int64(n)
		lastValidOffset = offset
	}

	if highestLSN > 0 {
		w.nextLSN = highestLSN + 1
	}

	// Truncate any corrupt tail.
	if lastValidOffset < info.Size() {
		if err := w.file.Truncate(lastValidOffset); err != nil {
			return fmt.Errorf("wal: truncate corrupt tail: %w", err)
		}
		if err := durableSync(w.file); err != nil {
			return fmt.Errorf("wal: sync after truncate: %w", err)
		}
	}

	// Seek to end for future appends.
	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("wal: seek to end: %w", err)
	}

	return nil
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

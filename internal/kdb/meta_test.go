package kdb_test

import (
	"testing"

	"github.com/jenaiz/pcke/internal/kdb"
	"github.com/jenaiz/pcke/internal/kdb/encoding"
	"github.com/jenaiz/pcke/internal/kdb/page"
)

func TestInitMetaWritesBothSlots(t *testing.T) {
	dir := t.TempDir()

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	m, err := db.ReadMeta()
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}

	if m.Version != kdb.MetaVersion {
		t.Errorf("Version = %d, want %d", m.Version, kdb.MetaVersion)
	}
	if m.Generation != 1 {
		t.Errorf("Generation = %d, want 1", m.Generation)
	}
	if m.PageCount != uint64(kdb.GrowthChunk) {
		t.Errorf("PageCount = %d, want %d", m.PageCount, kdb.GrowthChunk)
	}
	if m.FreelistRoot != 0 {
		t.Errorf("FreelistRoot = %d, want 0", m.FreelistRoot)
	}
}

func TestMetaRoundTrip(t *testing.T) {
	dir := t.TempDir()

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Write a new meta with bumped generation.
	newMeta := &kdb.Meta{
		Version:        kdb.MetaVersion,
		Generation:     2,
		PageCount:      32,
		FreelistRoot:   5,
		FreelistFormat: kdb.FreelistLinkedList,
	}
	if err := db.WriteMeta(newMeta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	got, err := db.ReadMeta()
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}

	if got.Generation != 2 {
		t.Errorf("Generation = %d, want 2", got.Generation)
	}
	if got.PageCount != 32 {
		t.Errorf("PageCount = %d, want 32", got.PageCount)
	}
	if got.FreelistRoot != 5 {
		t.Errorf("FreelistRoot = %d, want 5", got.FreelistRoot)
	}
}

func TestSwapMetaAlternatesSlots(t *testing.T) {
	dir := t.TempDir()

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Initial: both slots have generation 1.
	// Swap gen 2 → should go to inactive slot.
	m2 := &kdb.Meta{
		Version:    kdb.MetaVersion,
		Generation: 2,
		PageCount:  16,
	}
	if err := db.WriteMeta(m2); err != nil {
		t.Fatalf("WriteMeta(gen=2): %v", err)
	}

	// Swap gen 3 → should go to the other slot.
	m3 := &kdb.Meta{
		Version:    kdb.MetaVersion,
		Generation: 3,
		PageCount:  16,
	}
	if err := db.WriteMeta(m3); err != nil {
		t.Fatalf("WriteMeta(gen=3): %v", err)
	}

	got, err := db.ReadMeta()
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if got.Generation != 3 {
		t.Errorf("Generation = %d, want 3", got.Generation)
	}
}

func TestCrashDuringSwap_OneValidGeneration(t *testing.T) {
	dir := t.TempDir()

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Write gen 2 successfully.
	m2 := &kdb.Meta{
		Version:    kdb.MetaVersion,
		Generation: 2,
		PageCount:  16,
	}
	if err := db.WriteMeta(m2); err != nil {
		t.Fatalf("WriteMeta(gen=2): %v", err)
	}

	// Simulate a crash during swap of gen 3:
	// Corrupt the inactive slot (the one that would receive gen 3).
	// Read both slots, find the one with lower generation, corrupt it.
	f := db.DataFile()

	// Read slot A and slot B to find the inactive one.
	bufA := make([]byte, page.Size)
	if _, err := f.ReadAt(bufA, 0); err != nil {
		t.Fatalf("read slot A: %v", err)
	}
	bufB := make([]byte, page.Size)
	if _, err := f.ReadAt(bufB, page.Size); err != nil {
		t.Fatalf("read slot B: %v", err)
	}

	genA := encoding.Uint64(bufA[28:]) // metaOffGeneration = 28
	genB := encoding.Uint64(bufB[28:])

	// Corrupt the inactive slot (lower generation) to simulate partial write.
	var corruptOffset int64
	if genA <= genB {
		corruptOffset = 0
	} else {
		corruptOffset = page.Size
	}

	garbage := make([]byte, page.Size)
	for i := range garbage {
		garbage[i] = 0xFF
	}
	if _, err := f.WriteAt(garbage, corruptOffset); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync corrupt: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen — should recover the valid generation.
	db2, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = db2.Close() }()

	got, err := db2.ReadMeta()
	if err != nil {
		t.Fatalf("ReadMeta after crash: %v", err)
	}

	// The surviving slot should have generation 2 (the successful write).
	if got.Generation != 2 {
		t.Errorf("Generation after crash = %d, want 2", got.Generation)
	}
}

func TestBothMetaCorrupted(t *testing.T) {
	dir := t.TempDir()

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	f := db.DataFile()

	// Corrupt both meta pages.
	garbage := make([]byte, page.Size)
	for i := range garbage {
		garbage[i] = 0xDE
	}
	for _, offset := range []int64{0, page.Size} {
		if _, err := f.WriteAt(garbage, offset); err != nil {
			t.Fatalf("corrupt at %d: %v", offset, err)
		}
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen — should still succeed (file not empty, initIfEmpty skipped).
	// But ReadMeta should fail.
	db2, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = db2.Close() }()

	_, err = db2.ReadMeta()
	if err == nil {
		t.Fatal("expected error reading corrupted meta, got nil")
	}
}

func TestMetaOnClosedDB(t *testing.T) {
	dir := t.TempDir()

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := db.ReadMeta(); err != kdb.ErrDBClosed {
		t.Errorf("ReadMeta on closed: got %v, want ErrDBClosed", err)
	}
	if err := db.WriteMeta(&kdb.Meta{}); err != kdb.ErrDBClosed {
		t.Errorf("WriteMeta on closed: got %v, want ErrDBClosed", err)
	}
}

func TestMetaHighGenWins(t *testing.T) {
	dir := t.TempDir()

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Do 10 generation bumps.
	for gen := uint64(2); gen <= 11; gen++ {
		m := &kdb.Meta{
			Version:    kdb.MetaVersion,
			Generation: gen,
			PageCount:  uint64(kdb.GrowthChunk),
		}
		if err := db.WriteMeta(m); err != nil {
			t.Fatalf("WriteMeta(gen=%d): %v", gen, err)
		}
	}

	got, err := db.ReadMeta()
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if got.Generation != 11 {
		t.Errorf("Generation = %d, want 11", got.Generation)
	}
}

func TestMetaPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	m := &kdb.Meta{
		Version:        kdb.MetaVersion,
		Generation:     5,
		PageCount:      32,
		FreelistRoot:   10,
		FreelistFormat: kdb.FreelistBTree,
	}
	if err := db.WriteMeta(m); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen and verify.
	db2, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = db2.Close() }()

	got, err := db2.ReadMeta()
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}

	if got.Version != kdb.MetaVersion {
		t.Errorf("Version = %d, want %d", got.Version, kdb.MetaVersion)
	}
	if got.Generation != 5 {
		t.Errorf("Generation = %d, want 5", got.Generation)
	}
	if got.PageCount != 32 {
		t.Errorf("PageCount = %d, want 32", got.PageCount)
	}
	if got.FreelistRoot != 10 {
		t.Errorf("FreelistRoot = %d, want 10", got.FreelistRoot)
	}
	if got.FreelistFormat != kdb.FreelistBTree {
		t.Errorf("FreelistFormat = %d, want FreelistBTree", got.FreelistFormat)
	}
}

func TestInvalidGenerationDetection(t *testing.T) {
	dir := t.TempDir()

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Write gen 2 to one slot, corrupt the other to have bad CRC.
	m2 := &kdb.Meta{
		Version:    kdb.MetaVersion,
		Generation: 2,
		PageCount:  16,
	}
	if err := db.WriteMeta(m2); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	f := db.DataFile()

	// Read slot A, flip a byte in payload to make CRC invalid (but not totally garbage).
	bufA := make([]byte, page.Size)
	if _, err := f.ReadAt(bufA, 0); err != nil {
		t.Fatalf("read slot A: %v", err)
	}

	genA := encoding.Uint64(bufA[28:])

	// Determine which slot has the lower generation and corrupt it minimally.
	var corruptOffset int64
	if genA < 2 {
		corruptOffset = 0 // slot A has gen 1
	} else {
		corruptOffset = page.Size // slot B has gen 1
	}

	bufCorrupt := make([]byte, page.Size)
	if _, err := f.ReadAt(bufCorrupt, corruptOffset); err != nil {
		t.Fatalf("read for corrupt: %v", err)
	}
	// Flip one byte in the payload area to invalidate CRC.
	bufCorrupt[30] ^= 0xFF
	if _, err := f.WriteAt(bufCorrupt, corruptOffset); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// ReadMeta should still return the valid generation (gen 2).
	got, err := db.ReadMeta()
	if err != nil {
		t.Fatalf("ReadMeta with one invalid: %v", err)
	}
	if got.Generation != 2 {
		t.Errorf("Generation = %d, want 2", got.Generation)
	}

	_ = db.Close()
}

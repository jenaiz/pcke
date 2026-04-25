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
	// After T10 wiring, Open performs freelist migration which bumps generation.
	if m.Generation < 1 {
		t.Errorf("Generation = %d, want >= 1", m.Generation)
	}
	if m.PageCount != uint64(kdb.GrowthChunk) {
		t.Errorf("PageCount = %d, want %d", m.PageCount, kdb.GrowthChunk)
	}
	// FreelistFormat should be BTree after migration.
	if m.FreelistFormat != kdb.FreelistBTree {
		t.Errorf("FreelistFormat = %d, want %d (BTree)", m.FreelistFormat, kdb.FreelistBTree)
	}
}

func TestMetaRoundTrip(t *testing.T) {
	dir := t.TempDir()

	db, err := kdb.Open(dir, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Read current generation (may be > 1 after migration + post-replay commit).
	cur, err := db.ReadMeta()
	if err != nil {
		t.Fatalf("ReadMeta initial: %v", err)
	}

	// Write a new meta with bumped generation.
	wantGen := cur.Generation + 1
	newMeta := &kdb.Meta{
		Version:        kdb.MetaVersion,
		Generation:     wantGen,
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

	if got.Generation != wantGen {
		t.Errorf("Generation = %d, want %d", got.Generation, wantGen)
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

	// Read current generation and write one higher.
	cur, err := db.ReadMeta()
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	wantGen := cur.Generation + 1
	m2 := &kdb.Meta{
		Version:    kdb.MetaVersion,
		Generation: wantGen,
		PageCount:  16,
	}
	if err := db.WriteMeta(m2); err != nil {
		t.Fatalf("WriteMeta(gen=%d): %v", wantGen, err)
	}

	// Simulate a crash during swap of the next generation:
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

	// The surviving slot should have our written generation (the successful write)
	// or higher (from post-replay commit on reopen).
	if got.Generation < wantGen {
		t.Errorf("Generation after crash = %d, want >= %d", got.Generation, wantGen)
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

	// Reopen should fail because wireSubsystems cannot load meta.
	_, err = kdb.Open(dir, nil)
	if err == nil {
		t.Fatal("expected error opening DB with corrupted meta, got nil")
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

	cur, err := db.ReadMeta()
	if err != nil {
		t.Fatalf("ReadMeta initial: %v", err)
	}
	wantGen := cur.Generation + 1
	m := &kdb.Meta{
		Version:        kdb.MetaVersion,
		Generation:     wantGen,
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

	// Reopen and verify. Post-replay commit bumps generation further.
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
	if got.Generation < wantGen {
		t.Errorf("Generation = %d, want >= %d", got.Generation, wantGen)
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

	cur, err := db.ReadMeta()
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	wantGen := cur.Generation + 1

	// Write one generation higher, corrupt the other slot.
	m2 := &kdb.Meta{
		Version:    kdb.MetaVersion,
		Generation: wantGen,
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
	if genA < wantGen {
		corruptOffset = 0
	} else {
		corruptOffset = page.Size
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

	// ReadMeta should still return the valid generation.
	got, err := db.ReadMeta()
	if err != nil {
		t.Fatalf("ReadMeta with one invalid: %v", err)
	}
	if got.Generation != wantGen {
		t.Errorf("Generation = %d, want %d", got.Generation, wantGen)
	}

	_ = db.Close()
}

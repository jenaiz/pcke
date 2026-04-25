package diagnostics_test

import (
	"encoding/json"
	"testing"

	"github.com/jenaiz/pcke/internal/kdb/diagnostics"
)

// TestStatsJSON verifies that Stats marshals to valid JSON with the expected schema.
func TestStatsJSON(t *testing.T) {
	s := diagnostics.Stats{
		DataFileBytes:  65536,
		PageCount:      16,
		FreePageCount:  10,
		TreeDepth:      2,
		KeyCount:       100,
		WALTotalBytes:  4096,
		ActiveLSN:      42,
		BufferPoolSize: 256,
		DirtyPages:     3,
		PinnedPages:    1,
		BufferPoolHits: 900,
		BufferPoolMiss: 100,
		BufferHitRate:  0.9,
		Generation:     5,
		FreelistRoot:   7,
		FreelistFormat: 1,
	}

	data, err := s.JSON()
	if err != nil {
		t.Fatalf("JSON marshal: %v", err)
	}

	// Round-trip: unmarshal into a map to verify all fields present.
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}

	required := []string{
		"data_file_bytes", "page_count", "free_page_count",
		"tree_depth", "key_count",
		"wal_total_bytes", "active_lsn",
		"buffer_pool_size", "dirty_pages", "pinned_pages",
		"buffer_pool_hits", "buffer_pool_misses", "buffer_hit_rate",
		"generation", "freelist_root", "freelist_format",
	}
	for _, key := range required {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON field %q", key)
		}
	}

	// Verify round-trip produces the same values.
	var s2 diagnostics.Stats
	if err := json.Unmarshal(data, &s2); err != nil {
		t.Fatalf("JSON round-trip unmarshal: %v", err)
	}
	if s2 != s {
		t.Errorf("round-trip mismatch:\n  got  %+v\n  want %+v", s2, s)
	}
}

// TestStatsHuman verifies the human-readable output is non-empty.
func TestStatsHuman(t *testing.T) {
	s := diagnostics.Stats{
		DataFileBytes:  65536,
		PageCount:      16,
		Generation:     1,
		FreelistFormat: 1,
	}

	out := s.Human()
	if len(out) == 0 {
		t.Fatal("Human() returned empty string")
	}
	if out[:3] != "===" {
		t.Errorf("Human() does not start with header: %q", out[:20])
	}
}

// TestStatsJSONSchema verifies the JSON output is valid and non-null for
// a Stats with non-zero values (matching DoD: "All fields non-null").
func TestStatsJSONSchema(t *testing.T) {
	s := diagnostics.Stats{
		DataFileBytes:  1,
		PageCount:      1,
		FreePageCount:  1,
		TreeDepth:      1,
		KeyCount:       1,
		WALTotalBytes:  1,
		ActiveLSN:      1,
		BufferPoolSize: 1,
		DirtyPages:     1,
		PinnedPages:    1,
		BufferPoolHits: 1,
		BufferPoolMiss: 1,
		BufferHitRate:  0.5,
		Generation:     1,
		FreelistRoot:   1,
		FreelistFormat: 1,
	}

	data, err := s.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for k, v := range m {
		if v == nil {
			t.Errorf("field %q is null", k)
		}
	}
}

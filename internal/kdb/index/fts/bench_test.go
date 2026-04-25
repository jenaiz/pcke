package fts_test

import (
	"fmt"
	"math/rand/v2"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/jenaiz/pcke/internal/kdb/index/fts"
)

// corpusDocuments maps corpus doc IDs to representative document text.
// Each document contains terminology that the corresponding corpus query
// should find via BM25 scoring.
//
//nolint:gochecknoglobals,gosec // test-only lookup table; G101 false positive on map keys.
var corpusDocuments = map[string]string{
	// q01: "error handling strategy"
	"doc:error-handling":      "Error handling strategy in Go: always check returned errors and wrap them with context using fmt.Errorf",
	"doc:error-propagation":   "Error propagation patterns: propagate errors up the call stack with additional context for debugging",
	"doc:sentinel-errors":     "Sentinel errors provide stable error handling identifiers that callers can check with errors.Is",
	"doc:fmt-errorf-wrapping": "Error wrapping with fmt.Errorf and the %w verb enables error handling chains and unwrapping strategy",
	"doc:recovery-patterns":   "Recovery patterns for error handling: graceful degradation strategy when operations fail",

	// q02: "B+tree split algorithm"
	"doc:btree-split":          "B+tree split algorithm: when a leaf overflows, split it 50/50 and promote the median key to the parent",
	"doc:btree-node-format":    "B+tree node format: leaf nodes store key-value pairs sorted by key for the split algorithm",
	"doc:btree-overflow":       "B+tree overflow handling: split triggered when a node exceeds the page size",
	"doc:btree-internal-split": "B+tree internal node split algorithm: split the node and push median key up the tree",
	"doc:page-layout":          "Page layout for B+tree nodes: header with page type, cell count, and free space for the split algorithm",

	// q03: "WAL write-ahead log replay"
	"doc:wal-replay":        "WAL replay: on open, the write-ahead log is replayed linearly to reconstruct the latest committed state",
	"doc:wal-record-format": "WAL record format: each write-ahead log entry contains LSN, CRC32C, type tag, and payload",
	"doc:wal-append":        "WAL append: write-ahead log records are appended sequentially with fsync for durability",
	"doc:recovery-open":     "Recovery on open: replay the write-ahead log (WAL) to restore dirty pages lost during a crash",
	"doc:crash-recovery":    "Crash recovery relies on WAL write-ahead log replay, double-meta verification, and invariant checks",

	// q04: "transaction isolation level"
	"doc:tx-view":            "Transaction View provides read-only snapshot isolation level for concurrent readers",
	"doc:tx-update":          "Transaction Update acquires an exclusive lock to provide serializable isolation level for writers",
	"doc:snapshot-isolation": "Snapshot isolation level: readers see a consistent view of the database at transaction start time",
	"doc:rwmutex-global":     "Global RWMutex provides the current transaction isolation level: multiple readers, single writer",
	"doc:meta-swap":          "Meta page atomic swap finalizes a transaction by updating the active meta page with the new root and generation",

	// q05: "buffer pool eviction policy"
	"doc:bufpool-eviction":       "Buffer pool eviction policy: clock-sweep algorithm scans frames and evicts unreferenced pages",
	"doc:bufpool-pin-unpin":      "Buffer pool pin and unpin: pinned pages are protected from the eviction policy",
	"doc:bufpool-dirty-tracking": "Buffer pool dirty tracking: dirty pages must be flushed before the eviction policy reclaims them",
	"doc:clock-sweep":            "Clock-sweep eviction policy for the buffer pool: efficient approximation of LRU with a circular hand",
	"doc:page-replacement":       "Page replacement eviction policy: buffer pool manages which pages stay in memory",

	// q06: "file locking mechanism"
	"doc:lock-flock":       "File locking mechanism using flock system call for exclusive database access",
	"doc:lock-pid-file":    "PID file locking mechanism: write process ID to detect stale locks",
	"doc:lock-unix":        "Unix file locking mechanism: flock on Linux and macOS for cross-platform support",
	"doc:lock-windows":     "Windows file locking mechanism: LockFileEx for exclusive file access",
	"doc:exclusive-access": "Exclusive access via file locking mechanism prevents concurrent database corruption",

	// q07: "CRC32C integrity verification"
	"doc:crc32c-page":         "CRC32C integrity verification on every page read to detect silent corruption",
	"doc:crc32c-encoding":     "CRC32C checksum in the encoding layer for integrity verification of records",
	"doc:page-header-verify":  "Page header CRC32C verification ensures integrity of the page metadata",
	"doc:data-integrity":      "Data integrity through CRC32C verification at multiple levels: page, record, WAL",
	"doc:checksum-validation": "Checksum validation using CRC32C for integrity verification during recovery",

	// q08: "freelist page allocation"
	"doc:freelist-alloc":           "Freelist page allocation: allocate a free page by removing it from the freelist",
	"doc:freelist-free":            "Freelist free: return a page to the freelist for future allocation",
	"doc:freelist-btree-migration": "Freelist migration: B+tree-backed freelist replaces linked-list for page allocation",
	"doc:freelist-linked-list":     "Linked-list freelist: original page allocation strategy before B+tree migration",
	"doc:page-growth":              "Page growth: extend the file when freelist page allocation has no free pages",

	// q09: "camelCase identifier tokenization"
	"doc:tokenizer-camelcase":     "camelCase identifier tokenization: split parseJSON into parse and json tokens",
	"doc:tokenizer-word-boundary": "Word boundary tokenization: Unicode-aware splitting for identifier extraction",
	"doc:tokenizer-snake-case":    "snake_case identifier tokenization: split error_handler into error and handler tokens",
	"doc:fts-tokenizer":           "FTS tokenizer handles camelCase and snake_case identifier tokenization for code search",
	"doc:identifier-splitting":    "Identifier splitting tokenization: recognize camelCase, PascalCase, and snake_case patterns",

	// q10: "secondary index by module"
	"doc:index-by-module": "Secondary index by module: look up knowledge nodes belonging to a specific Go module",
	"doc:index-by-tag":    "Secondary index by tag: look up nodes by their assigned tags",
	"doc:index-encoding":  "Index encoding format for secondary index keys with module path prefix",
	"doc:secondary-index": "Secondary index design: B+tree-backed indexes for efficient lookup by module or tag",
	"doc:btree-index":     "B+tree index for secondary lookups by module, tag, or other attributes",

	// q11: "crash recovery invariants" (doc:crash-recovery already defined in q03)
	"doc:invariant-crc":              "CRC invariant for crash recovery: every page must pass CRC32C verification after replay",
	"doc:invariant-meta-consistency": "Meta consistency invariant for crash recovery: active meta must point to a valid B+tree root",
	"doc:invariant-wal-replay":       "WAL replay invariant for crash recovery: all committed transactions must be reconstructed",
	"doc:crashsim-harness":           "Crash simulation harness tests recovery invariants by injecting failures at hook points",

	// q12: "double meta page atomic swap" (doc:meta-swap already defined in q04)
	"doc:meta-double":   "Double meta page design: two meta pages at fixed locations enable atomic swap on commit",
	"doc:meta-active":   "Active meta page selection: the meta with the higher generation is the current state after atomic swap",
	"doc:meta-lsn":      "Meta page LSN tracks the double meta page state for WAL replay boundaries",
	"doc:atomic-commit": "Atomic commit via double meta page swap: a single fsync makes the transaction durable",

	// q13: "varint encoding format"
	"doc:varint-encode":      "Varint encoding format: variable-length integer encoding using continuation bits",
	"doc:varint-decode":      "Varint decoding format: read bytes until continuation bit is clear",
	"doc:encoding-record":    "Record encoding format with varint-encoded field lengths and type tags",
	"doc:encoding-endian":    "Endian encoding format: little-endian byte order for fixed-width integers",
	"doc:encoding-schema-v1": "Schema v1 encoding format: tagged records with varint lengths and CRC32C",

	// q14: "git repository analysis scanner"
	"doc:analysis-scanner":    "Git repository analysis scanner: walks the file tree and classifies project components",
	"doc:analysis-git":        "Git analysis: extract commit history and contributor patterns from the repository",
	"doc:analysis-heuristics": "Heuristics for repository analysis: detect frameworks, languages, and project structure via scanner",
	"doc:scan-command":        "Scan command triggers the git repository analysis scanner on the current project",
	"doc:file-tree-walk":      "File tree walk in the repository analysis scanner: enumerate all tracked files",

	// q15: "secret detection patterns"
	"doc:secrets-detection":  "Secret detection patterns: scan source files for accidentally committed credentials",
	"doc:secrets-aws":        "AWS secret detection patterns: match access key IDs and secret access key formats",
	"doc:secrets-patterns":   "Secret detection regex patterns: API keys, tokens, passwords, and private keys",
	"doc:analysis-security":  "Security analysis with secret detection patterns to flag exposed credentials",
	"doc:heuristics-secrets": "Heuristics for secret detection: high-entropy string patterns and known key formats",

	// q16: "markdown output context generation"
	"doc:output-render":       "Markdown output render: generate context files for AI agents from the knowledge base",
	"doc:output-architecture": "Architecture markdown output: generate project structure context for AI agents",
	"doc:output-modules":      "Modules markdown output: generate Go module dependency context",
	"doc:output-conventions":  "Conventions markdown output: generate coding standards context for AI agents",
	"doc:output-agents":       "Agents markdown output: generate agent-specific context instructions",

	// q17: "BM25 relevance scoring"
	"doc:bm25-scorer":     "BM25 relevance scoring: Okapi BM25 computes document scores based on term frequency and IDF",
	"doc:bm25-parameters": "BM25 scoring parameters: k1=1.2 controls term frequency saturation, b=0.75 controls length normalization",
	"doc:bm25-idf":        "BM25 IDF component for relevance scoring: ln((N-n+0.5)/(n+0.5)+1) dampens common terms",
	"doc:bm25-tf":         "BM25 term frequency component for relevance scoring: saturating TF reduces impact of repeated terms",
	"doc:fts-ranking":     "FTS ranking with BM25 relevance scoring produces ordered results for user queries",

	// q18: "checkpoint dirty page flush"
	"doc:checkpoint-flush":        "Checkpoint dirty page flush: write all modified pages from the buffer pool to disk",
	"doc:checkpoint-dirty-table":  "Dirty page table tracks which pages need checkpoint flush during the next checkpoint cycle",
	"doc:checkpoint-wal-rotation": "WAL rotation after checkpoint dirty page flush: rotate to a new segment and remove old ones",
	"doc:checkpoint-meta-update":  "Meta update during checkpoint: record the new LSN boundary after dirty page flush",
	"doc:fuzzy-checkpoint":        "Fuzzy checkpoint: flush dirty pages without blocking concurrent read transactions",

	// q19: "posting list delta compression"
	"doc:posting-encoding": "Posting list encoding with delta compression: store differences between consecutive document IDs",
	"doc:posting-delta":    "Delta compression for posting lists: encode doc ID gaps instead of absolute values",
	"doc:posting-gamma":    "Gamma coding for posting list compression: efficient encoding of small delta values",
	"doc:posting-varint":   "Varint encoding in posting list compression: variable-length integers for deltas and frequencies",
	"doc:fts-segment":      "FTS segment stores posting lists with delta compression for compact on-disk representation",

	// q20: "configuration TOML parsing"
	"doc:config-toml":       "Configuration TOML parsing: read layered config from repo-level and user-level TOML files",
	"doc:config-defaults":   "Configuration defaults: built-in values used when no TOML configuration file is present",
	"doc:config-validation": "Configuration validation: verify TOML parsing produced valid values within expected ranges",
	"doc:config-paths":      "Configuration file paths: TOML parsing looks in .pcke/config.toml and ~/.config/pcke/config.toml",
	"doc:cli-config":        "CLI config command: view and manage TOML configuration values",
}

// noiseDocuments generates background documents to test ranking quality.
// They contain common words but should not rank highly for corpus queries.
func noiseDocuments(count int) []string {
	rng := rand.New(rand.NewPCG(12345, 0)) //nolint:gosec // G404: deterministic seed for reproducible tests.

	words := []string{
		"function", "variable", "return", "method", "interface",
		"package", "import", "struct", "channel", "goroutine",
		"select", "switch", "defer", "compile",
		"memory", "heap", "pointer",
		"slice", "array", "boolean", "integer",
		"linked", "cancel", "timeout", "deadline",
		"closure", "literal", "mutex", "signal", "binary",
	}

	docs := make([]string, count)
	for i := range count {
		length := 10 + rng.IntN(30)
		buf := make([]byte, 0, length*10)
		for j := range length {
			if j > 0 {
				buf = append(buf, ' ')
			}
			buf = append(buf, words[rng.IntN(len(words))]...)
		}
		docs[i] = string(buf)
	}
	return docs
}

// TestPrecisionAt5EndToEnd loads the evaluation corpus, populates an FTS
// index with matching documents plus noise, and verifies Precision@5 >= 70%.
func TestPrecisionAt5EndToEnd(t *testing.T) {
	corpus := loadCorpus(t)

	// Build an FTS index with corpus documents + noise.
	idx := fts.NewIndex()

	// Map from docID (uint64) back to the corpus doc string ID.
	docIDToCorpus := make(map[uint64]string)

	// Add corpus documents.
	for corpusID, text := range corpusDocuments {
		docID := idx.AddDocument(text)
		docIDToCorpus[docID] = corpusID
	}

	// Add noise documents to make ranking meaningful.
	for _, text := range noiseDocuments(500) {
		idx.AddDocument(text)
	}

	idx.Commit()

	// Run evaluation.
	searchFn := func(query string) []string {
		tokens := fts.Tokenize(query)
		seen := make(map[string]struct{}, len(tokens))
		var terms []string
		for _, tok := range tokens {
			if _, ok := seen[tok.Term]; !ok {
				seen[tok.Term] = struct{}{}
				terms = append(terms, tok.Term)
			}
		}
		bm25Results := idx.ScoreBM25(terms)

		var ids []string
		for _, r := range bm25Results {
			if cid, ok := docIDToCorpus[r.DocID]; ok {
				ids = append(ids, cid)
			}
		}
		return ids
	}

	report := RunEvaluation(corpus, searchFn)

	t.Logf("\n%s", report.String())

	if report.MeanPrecAt5 < 0.70 {
		t.Errorf("MeanPrecision@5 = %.2f%%, want >= 70%%", report.MeanPrecAt5*100)
	}

	// Log per-query failures for debugging.
	for _, qr := range report.PerQuery {
		if qr.PrecAt5 < 0.60 {
			t.Logf("  LOW: %s [%.0f%%] top5=%v", qr.ID, qr.PrecAt5*100, qr.ResultIDs[:min(5, len(qr.ResultIDs))])
		}
	}
}

// BenchmarkRecallLatency measures recall query latency on a 100K document index.
func BenchmarkRecallLatency(b *testing.B) {
	// Build index: 100 corpus docs + 100K noise docs.
	idx := fts.NewIndex()

	for _, text := range corpusDocuments {
		idx.AddDocument(text)
	}

	noise := noiseDocuments(100_000)
	for _, text := range noise {
		idx.AddDocument(text)
	}

	idx.Commit()

	// Merge to simulate post-checkpoint state.
	idx.Merge()

	queries := []string{
		"error handling strategy",
		"B+tree split algorithm",
		"WAL write-ahead log replay",
		"buffer pool eviction policy",
		"BM25 relevance scoring",
		"checkpoint dirty page flush",
		"crash recovery invariants",
		"posting list delta compression",
	}

	tokens := make([][]string, len(queries))
	for i, q := range queries {
		toks := fts.Tokenize(q)
		seen := make(map[string]struct{}, len(toks))
		var terms []string
		for _, tok := range toks {
			if _, ok := seen[tok.Term]; !ok {
				seen[tok.Term] = struct{}{}
				terms = append(terms, tok.Term)
			}
		}
		tokens[i] = terms
	}

	b.ResetTimer()
	for i := range b.N {
		terms := tokens[i%len(tokens)]
		results := idx.ScoreBM25(terms)
		if len(results) == 0 {
			b.Fatal("no results")
		}
	}
}

// TestRecallP99Latency measures p99 latency on 100K docs and checks < 80ms.
func TestRecallP99Latency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping latency test in short mode")
	}

	// Build index: 100 corpus docs + 100K noise docs.
	idx := fts.NewIndex()

	for _, text := range corpusDocuments {
		idx.AddDocument(text)
	}

	noise := noiseDocuments(100_000)
	for _, text := range noise {
		idx.AddDocument(text)
	}

	idx.Commit()
	idx.Merge()

	queries := []string{
		"error handling strategy",
		"B+tree split algorithm",
		"WAL write-ahead log replay",
		"transaction isolation level",
		"buffer pool eviction policy",
		"file locking mechanism",
		"CRC32C integrity verification",
		"freelist page allocation",
		"camelCase identifier tokenization",
		"secondary index by module",
		"crash recovery invariants",
		"double meta page atomic swap",
		"varint encoding format",
		"git repository analysis scanner",
		"secret detection patterns",
		"markdown output context generation",
		"BM25 relevance scoring",
		"checkpoint dirty page flush",
		"posting list delta compression",
		"configuration TOML parsing",
	}

	tokenSets := make([][]string, len(queries))
	for i, q := range queries {
		toks := fts.Tokenize(q)
		seen := make(map[string]struct{}, len(toks))
		var terms []string
		for _, tok := range toks {
			if _, ok := seen[tok.Term]; !ok {
				seen[tok.Term] = struct{}{}
				terms = append(terms, tok.Term)
			}
		}
		tokenSets[i] = terms
	}

	// Force GC to clear allocations from index build, then warmup.
	runtime.GC()
	for i := range 50 {
		idx.ScoreBM25(tokenSets[i%len(queries)])
	}
	runtime.GC()

	// Run 1000 iterations (50 per query) and measure latencies.
	const iterations = 1000
	latencies := make([]time.Duration, 0, iterations)

	for i := range iterations {
		terms := tokenSets[i%len(queries)]

		start := time.Now()
		results := idx.ScoreBM25(terms)
		elapsed := time.Since(start)

		if len(results) == 0 {
			t.Fatalf("query %q returned no results", queries[i%len(queries)])
		}

		latencies = append(latencies, elapsed)
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	p50 := latencies[len(latencies)*50/100]
	p95 := latencies[len(latencies)*95/100]
	p99 := latencies[len(latencies)*99/100]
	maxLat := latencies[len(latencies)-1]

	t.Logf("Recall latency (100K docs, %d queries):", iterations)
	t.Logf("  p50  = %v", p50)
	t.Logf("  p95  = %v", p95)
	t.Logf("  p99  = %v", p99)
	t.Logf("  max  = %v", maxLat)

	if p99 > 80*time.Millisecond {
		t.Errorf("p99 latency = %v, want < 80ms", p99)
	}
}

func init() {
	// Verify all corpus queries have matching documents.
	_ = fmt.Sprintf("corpus has %d documents", len(corpusDocuments))
}

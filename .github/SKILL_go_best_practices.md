# Go Senior Engineer Best Practices (pcke)

> Workspace-scoped skill for pcke project. Ensures senior-level Go code quality across error handling, concurrency, testing, performance, and architecture.

## Purpose

This skill codifies Go best practices for **pcke** development, enforcing standards expected of senior engineers:
- Deterministic, testable, observable code
- Explicit error handling with semantic exit codes
- Thread-safe concurrent patterns and race detection
- High test coverage (88%+ target) with focused benchmarks
- Performance regression gates and profiling
- Clear module boundaries and package layering

**When to invoke:**
- During code implementation (catch issues early)
- Pre-commit validation (automated checks)
- PR review (peer validation using checklist)
- Performance regression detection (benchmark gates)

---

## 1. Error Handling & Return Codes

### Senior Principle: Errors are values, not strings. Be explicit.

#### Rules

1. **Define custom error types** for semantic information
   ```go
   // ✓ Good: packages define their own errors
   var (
       ErrDBLocked = errors.New("database locked")
       ErrNotFound = errors.New("entity not found")
   )
   
   type ValidationError struct {
       Field   string
       Message string
   }
   func (e *ValidationError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Message) }
   
   // ✗ Bad: stringly-typed errors
   if err != nil && strings.Contains(err.Error(), "not found") { ... }
   ```

2. **Map errors to exit codes** (see `cmd/pcke/exitcodes.go`):
   - `0`: success
   - `1`: user error (invalid input, bad flags)
   - `2`: config error (missing .toml, bad YAML)
   - `3`: lock conflict (ErrDBLocked → ExitLockConflict)
   - `4`: corruption (ErrChecksumMismatch, ErrInvalidConfig)
   - `5`: internal error (panic recovery, invariant violation)
   - `6`: schema mismatch (DB version incompatible)

3. **Unwrap sentinel errors, don't string-match**
   ```go
   // ✓ Good
   if errors.Is(err, io.EOF) { ... }
   if errors.As(err, &lockErr) { ... }
   
   // ✗ Bad
   if err.Error() == "EOF" { ... }
   ```

4. **Attach context with `fmt.Errorf`** (not string concatenation)
   ```go
   // ✓ Good: preserves error chain
   return fmt.Errorf("failed to open kdb at %s: %w", path, err)
   
   // ✗ Bad: loses original error
   return fmt.Errorf("failed to open kdb at %s: %v", path, err)
   ```

5. **Log AND return errors** — don't swallow
   ```go
   // ✓ Good
   if err != nil {
       log.Logger("kdb").Error("checksum mismatch on page", "page", p.ID, "err", err)
       return err
   }
   
   // ✗ Bad
   if err != nil {
       return nil  // Lost forever
   }
   ```

6. **Use structured attributes, never format secrets into messages**
   ```go
   // ✓ Good: slog attributes auto-redact matching (?i)(secret|token|key|password|credential)
   log.Logger("mcp").Error("auth failed", "token_len", len(token), "user", username)
   
   // ✗ Bad
   log.Logger("mcp").Error(fmt.Sprintf("auth failed: token=%s", token))
   ```

#### Checklist
- [ ] All public functions that can fail have error return
- [ ] Error types are defined at package level (not inline)
- [ ] Errors use `fmt.Errorf` with `%w` wrapper
- [ ] Exit codes mapped in `cmd/pcke/exitcodes.go`
- [ ] Errors logged with context attributes (not secrets)
- [ ] No string-matching on error messages

---

## 2. Concurrency & Locking

### Senior Principle: Threads are hard. Make the contract explicit and test for races.

#### Rules

1. **Document concurrency guarantees** in every exported type
   ```go
   // ✓ Good: explicit concurrency contract
   // DB is safe for concurrent reads and exclusive writes.
   // View/Update use RWMutex with snapshot isolation.
   type DB struct {
       mu sync.RWMutex
       pages map[int]*Page
   }
   
   // ✗ Bad: silence
   type DB struct {
       mu sync.RWMutex
       pages map[int]*Page
   }
   ```

2. **Use `sync.RWMutex` for reader-heavy workloads**
   ```go
   // ✓ Good: readers don't block each other
   func (db *DB) View(fn func() error) error {
       db.mu.RLock()
       defer db.mu.RUnlock()
       return fn()
   }
   
   // ✓ Good: exclusive writer
   func (db *DB) Update(fn func() error) error {
       db.mu.Lock()
       defer db.mu.Unlock()
       return fn()
   }
   ```

3. **Protect all shared state behind locks**
   ```go
   // ✓ Good: pages only accessible under lock
   func (db *DB) GetPage(id int) (*Page, error) {
       db.mu.RLock()
       defer db.mu.RUnlock()
       // Access db.pages only here
   }
   
   // ✗ Bad: returns mutable reference, caller could race
   func (db *DB) Pages() map[int]*Page {
       db.mu.RLock()
       defer db.mu.RUnlock()
       return db.pages  // Caller can mutate!
   }
   ```

4. **Test with `-race` detector enabled**
   ```bash
   make test-race          # All tests with -race
   go test -race ./...     # Ad-hoc
   ```
   - Fix **all** race detector warnings before merge
   - Never disable `-race` in prod code (`//nolint:all` is forbidden for races)

5. **Use channels for goroutine signaling, not locks**
   ```go
   // ✓ Good: clean shutdown
   ctx, cancel := context.WithCancel(context.Background())
   defer cancel()
   
   go func(ctx context.Context) {
       <-ctx.Done()
       // cleanup
   }(ctx)
   
   // ✗ Bad: ad-hoc stop flag + race
   var stopFlag bool  // Racy!
   go func() {
       for !stopFlag { ... }  // Data race
   }()
   stopFlag = true
   ```

6. **Use `context.Context` for cancellation and deadlines**
   ```go
   // ✓ Good: timeout + cancellation
   ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
   defer cancel()
   return db.Update(ctx, fn)
   
   // ✗ Bad: no timeout
   return db.Update(context.Background(), fn)
   ```

#### Checklist
- [ ] All exported types document concurrency safety
- [ ] Shared state protected by locks or atomics
- [ ] `sync.RWMutex` used for read-heavy patterns
- [ ] `-race` tests pass with no suppressions
- [ ] Shutdown/cancellation use context or channels, not flags
- [ ] No goroutine leaks (defer cleanup)

---

## 3. Interfaces & Composition

### Senior Principle: Small interfaces, fat implementations. Depend on interfaces, not concrete types.

#### Rules

1. **Keep interfaces small** (1–3 methods max)
   ```go
   // ✓ Good: focused, composable
   type Reader interface {
       Read([]byte) (int, error)
   }
   
   type Closer interface {
       Close() error
   }
   
   type ReadCloser interface {
       Reader
       Closer
   }
   
   // ✗ Bad: 15-method god interface
   type DatabaseInterface interface {
       Create(...) ...
       Read(...) ...
       Update(...) ...
       Delete(...) ...
       // ... 11 more
   }
   ```

2. **Accept interfaces, return concrete types** (from `io` stdlib pattern)
   ```go
   // ✓ Good
   func NewDB(r io.Reader) (*DB, error)  // Accept interface (flexible)
   func (db *DB) Export(w io.Writer) error  // Accept interface
   
   // ✗ Bad
   func NewDB(r *os.File) (*DB, error)  // Couple to concrete type
   ```

3. **Use embedding for composition, not inheritance**
   ```go
   // ✓ Good: composition over inheritance
   type ValidatingWriter struct {
       w io.Writer
       validator Validator
   }
   func (vw *ValidatingWriter) Write(b []byte) (int, error) {
       if err := vw.validator.Check(b); err != nil {
           return 0, err
       }
       return vw.w.Write(b)
   }
   
   // ✗ Bad: interface sprawl
   type Writer interface { ... }
   type Validator interface { ... }
   type ValidatingWriter interface { Writer; Validator }  // God interface
   ```

4. **Define interfaces where they're consumed, not where they're implemented**
   ```go
   // ✓ Good: interface in the package that needs it
   package analysis
   type SymbolResolver interface {
       Resolve(pkg, name string) (Symbol, error)
   }
   
   // ✗ Bad: interface in the provider package
   package ast
   type SymbolResolver interface { ... }  // Couples analysis to ast
   ```

5. **Test using interfaces, not reflection or mocks that match implementation**
   ```go
   // ✓ Good: mock via interface
   type mockStorage struct {
       getFunc func(key string) (string, error)
   }
   func (m *mockStorage) Get(key string) (string, error) {
       return m.getFunc(key)
   }
   
   // ✗ Bad: reflection-based mocking leaks impl details
   mock := MyStructMock{}  // Overly coupled
   ```

#### Checklist
- [ ] All interfaces have ≤ 3 methods (except stdlib stdlib.Writer, Reader which are canonical)
- [ ] Functions accept interfaces, return concrete types
- [ ] Interfaces defined in consuming package, not provider
- [ ] Composition used (embedding), not inheritance
- [ ] Tests use table-driven approach with interface mocks

---

## 4. Testing & Benchmarking

### Senior Principle: High coverage (88%+), focused benchmarks, fuzz for invariants.

#### Rules

1. **Colocate tests** as `foo_test.go` in same package
   ```
   ✓ kdb.go + kdb_test.go (same package)
   ✗ kdb.go + test/kdb_test.go (different package)
   ```

2. **Use table-driven tests**
   ```go
   // ✓ Good: parametric, easy to add cases
   tests := []struct {
       name    string
       input   interface{}
       want    interface{}
       wantErr string
   }{
       {"empty", []int{}, 0, ""},
       {"single", []int{5}, 5, ""},
       {"invalid", nil, 0, "nil slice"},
   }
   for _, tt := range tests {
       t.Run(tt.name, func(t *testing.T) {
           got, err := Sum(tt.input)
           if err != nil && tt.wantErr == "" {
               t.Fatalf("unexpected error: %v", err)
           }
           if got != tt.want {
               t.Errorf("got %v, want %v", got, tt.want)
           }
       })
   }
   ```

3. **Test interfaces, not implementation details**
   ```go
   // ✓ Good: test contract, not internals
   type StorageTest interface {
       TestGet(t *testing.T, s Storage)
       TestSet(t *testing.T, s Storage)
   }
   
   // ✗ Bad: test private fields
   func TestInternalLayout(t *testing.T) {
       db := &DB{}
       if len(db.pages) != 16 { ... }  // Implementation detail
   }
   ```

4. **Benchmark performance-critical paths**
   ```go
   // ✓ Good: focused benchmark
   func BenchmarkBTreeInsert(b *testing.B) {
       tree := NewBTree()
       b.ResetTimer()
       for i := 0; i < b.N; i++ {
           tree.Insert(i, i)
       }
   }
   
   // ✓ Run: make bench BENCH='BTreeInsert'
   ```

5. **Fuzz for invariants and edge cases**
   ```go
   // ✓ Good: property-based test
   func FuzzBTreeRoundtrip(f *testing.F) {
       f.Add([]byte("test"))
       f.Fuzz(func(t *testing.T, b []byte) {
           tree := NewBTree()
           tree.Insert(b, b)
           if got, _ := tree.Get(b); !bytes.Equal(got, b) {
               t.Errorf("roundtrip failed: %v != %v", got, b)
           }
       })
   }
   
   // ✓ Run: make fuzz FUZZTIME=30s
   ```

6. **Maintain >88% coverage in kdb/***
   - Run: `make test` → generates `cover.out`
   - Per-package: `go tool cover -html=cover.out`
   - Never drop >2pp vs. main without justification

7. **No sleep in tests** (except integration tests with explicit timeout)
   ```go
   // ✗ Bad
   go doAsync()
   time.Sleep(100*time.Millisecond)  // Flaky!
   
   // ✓ Good
   done := make(chan struct{})
   go func() { doAsync(); close(done) }()
   <-done  // Wait for signal
   ```

#### Checklist
- [ ] Tests colocated as `foo_test.go`
- [ ] Table-driven test structure used
- [ ] Coverage ≥ 88% in relevant packages
- [ ] Benchmarks for performance-critical paths
- [ ] Fuzz targets for invariants
- [ ] No sleeps (use channels/context)
- [ ] Tests use `-race` (no suppressions)

---

## 5. Code Style & Linting

### Senior Principle: Consistent, readable code. Linters catch the rest.

#### Rules

1. **Run linting before commit** (`make lint`)
   ```bash
   golangci-lint run --config=.golangci.yml
   ```

2. **No `nolint` without justification**
   ```go
   // ✓ Good: linter suppression with reason
   //nolint:gosec // G115: intentional uint64 → int cast for bit reinterpretation
   x := int(u64)
   
   // ✗ Bad: silent suppression
   //nolint:all
   x := int(u64)
   ```

3. **Keep cyclomatic complexity ≤ 15** (gocyclo rule)
   - If a function hits 16: extract helper functions or use early returns
   ```go
   // ✗ Bad: too complex (split it)
   if condition1 {
       if condition2 {
           if condition3 {
               // 15+ nesting levels
           }
       }
   }
   
   // ✓ Good: extract validators
   if !condition1 { return nil }
   if !condition2 { return nil }
   if !condition3 { return nil }
   // Simple logic
   ```

4. **Naming conventions**
   - `var`/`const`: `lowercase` (not `CamelCase` unless exported)
   - `func`: `CamelCase` if exported, `lowerCamelCase` if not
   - Acronyms: `HTTPServer` not `HttpServer`, `ID` not `Id`
   - Test names: `Test<Function><Scenario>` (e.g., `TestInsertDuplicateKey`)

5. **Comments: explain WHY, not WHAT** (code shows the what)
   ```go
   // ✓ Good: explains intent
   // Use a lock instead of atomic because we need snapshot isolation.
   mu sync.RWMutex
   
   // ✗ Bad: obvious from code
   // Declare a mutex.
   mu sync.RWMutex
   ```

6. **Package comments** (every public package has one)
   ```go
   // Package kdb provides an embedded key-value store with ACID guarantees.
   // It uses a B+tree with WAL-first writes and a buffer pool with clock-sweep eviction.
   package kdb
   ```

7. **Use `errcheck`, `gosec`, `staticcheck`** — fix all warnings
   - `errcheck`: catches unchecked errors
   - `gosec`: security issues (G115 for intentional int casts needs `//nolint:gosec`)
   - `staticcheck`: dead code, unreachable code

#### Checklist
- [ ] `make lint` passes (golangci-lint v2)
- [ ] No `//nolint` without comment justification
- [ ] Cyclomatic complexity ≤ 15
- [ ] Package-level comments for all public packages
- [ ] Function comments explain WHY, not WHAT
- [ ] Naming consistent (acronyms capitalized, exported PascalCase)
- [ ] No dead code or unreachable statements

---

## 6. Performance Gates & Benchmarking

### Senior Principle: Performance is observable. Regressions are caught and blamed.

#### Rules

1. **Benchmark critical paths** (rules in Makefile)
   ```bash
   make bench                    # All benchmarks
   make bench BENCH='Critical'   # Only BenchmarkCritical*
   ```

2. **BenchmarkCritical*** rules are enforced on every commit
   - >10% regression **blocks** the merge
   - Results are baseline'd in CI against main
   - Document performance characteristics in code:
   ```go
   // BenchmarkBTreeInsert measures single-node insertion.
   // Expect O(log N) operations per insert. Regression threshold: 10%.
   func BenchmarkCriticalBTreeInsert(b *testing.B) { ... }
   ```

3. **Profile performance regressions**
   ```bash
   # Dump CPU profile
   go test -cpuprofile=cpu.prof -bench=MyBench ./internal/kdb
   go tool pprof cpu.prof
   
   # Flame graph
   go-torch --url=http://localhost:6060 --time=30
   ```

4. **Use `testing.B.ReportAllocs()` for memory-sensitive code**
   ```go
   // ✓ Good: report allocations
   func BenchmarkCriticalPageAlloc(b *testing.B) {
       b.ReportAllocs()
       for i := 0; i < b.N; i++ {
           _ = NewPage()
       }
   }
   ```

5. **Never skip benchmarks in CI** — `skip` tests are allowed, `skip` benches are forbidden
   ```go
   // ✗ Bad in benchmarks
   func BenchmarkCriticalX(b *testing.B) {
       b.Skip("TODO: optimize")  // Hides regression!
   }
   ```

#### Checklist
- [ ] Critical paths have `BenchmarkCritical*` targets
- [ ] Benchmarks run cleanly (`make bench`)
- [ ] No 10%+ regressions vs. main
- [ ] Allocations reported for memory-critical code
- [ ] Performance comments document expectations
- [ ] Profiling tools available (pprof, flame graphs)

---

## 7. Module & Package Design

### Senior Principle: Clear boundaries, minimal internal mutations, explicit dependencies.

#### Rules

1. **Layer code strictly** (per `CLAUDE.md`)
   ```
   cmd/pcke/          ← CLI (Cobra), wires sub-commands
   internal/log/      ← Logging (slog factory)
   internal/config/   ← Config (TOML loader)
   internal/kdb/      ← Storage engine (core)
   internal/analysis/ ← Filesystem scan, AST, git, heuristics
   internal/output/   ← Markdown rendering
   internal/query/    ← DSL (lexer → parser → executor)
   internal/mcp/      ← MCP server
   ```

2. **Minimize internal mutations** — aim for immutability where practical
   ```go
   // ✓ Good: returns new value
   func (p *Page) WithHeader(h Header) *Page {
       np := *p
       np.header = h
       return &np
   }
   
   // ✗ Bad: mutates internal state
   func (p *Page) SetHeader(h Header) {
       p.header = h  // Ripple effects?
   }
   ```

3. **Use `internal/` packages** to hide implementation details
   ```
   internal/kdb/btree/     ← Hidden from external users
   internal/kdb/page/      ← Hidden
   
   // Only kdb/db.go and kdb/query.go are the public API
   ```

4. **No circular dependencies**
   ```go
   // ✗ Bad: analysis → kdb → analysis
   package analysis
   func Scan(db *kdb.DB) { ... }  // Creates cyclic dep
   
   // ✓ Good: pass interface
   package analysis
   type Store interface {
       Put(key, value []byte) error
   }
   func Scan(s Store) { ... }
   ```

5. **Explicit dependencies in constructors**
   ```go
   // ✓ Good: clear what's required
   type DB struct {
       logger    log.Logger
       config    *config.Config
       bufPool   *BufferPool
       pageCache *PageCache
   }
   
   func NewDB(logger log.Logger, cfg *config.Config) (*DB, error) {
       return &DB{
           logger:    logger,
           config:    cfg,
           bufPool:   NewBufferPool(cfg.BufferSize),
           pageCache: NewPageCache(cfg.CacheSize),
       }, nil
   }
   
   // ✗ Bad: global state
   var globalLogger = log.New(os.Stderr, "", 0)
   func NewDB(cfg *config.Config) (*DB, error) {
       db := &DB{logger: globalLogger}  // Hidden dependency
   }
   ```

6. **Document module boundaries**
   ```go
   // Package kdb exports: DB, View, Update, Schema, errors (ErrDBClosed, ErrInvalidConfig).
   // Subpackages (btree, page, wal, etc.) are internal — do not import from outside kdb/.
   // See docs/architecture.md for layering.
   package kdb
   ```

#### Checklist
- [ ] Code organized by layer (cmd → internal layers)
- [ ] No circular dependencies
- [ ] Implementation details in `internal/`
- [ ] Public API minimal and stable
- [ ] Dependencies explicit (passed, not global)
- [ ] Module boundaries documented

---

## 8. Logging & Observability

### Senior Principle: Structured logging with context. No printf-style logs.

#### Rules

1. **All logging must use `log.Logger(subsystem)`** (slog factory in `internal/log/`)
   ```go
   // ✓ Good: structured logging
   log.Logger("kdb.btree").Info("split node", "node_id", n.ID, "split_key", key)
   log.Logger("kdb").Error("checksum failed", "page", p.ID, "err", err)
   
   // ✗ Bad: fmt.Printf or raw log package
   fmt.Printf("Split node %d at key %v\n", n.ID, key)
   log.Printf("Failed to open: %v", err)
   ```

2. **Auto-redaction of sensitive attributes** (matching `(?i)(secret|token|key|password|credential)`)
   ```go
   // ✓ Good: attribute key signals redaction
   log.Logger("mcp").Info("auth check", "api_token_len", len(token), "secret_key_size", len(key))
   // Output redacts: secret_key_size (matches /secret|key/i), api_token_len (matches /token/i)
   
   // ✗ Bad: no structured attributes
   log.Logger("mcp").Info(fmt.Sprintf("token=%s", token))  // Exposed!
   ```

3. **Log at appropriate levels**
   - `Debug`: internal state, variable values (disabled in prod by default)
   - `Info`: lifecycle events, version, config applied
   - `Warn`: recoverable problems (retry, fallback)
   - `Error`: operation failed, may need manual intervention

   ```go
   // ✓ Good: graduated levels
   log.Logger("kdb").Debug("page evicted", "page_id", p.ID, "reason", "clock_sweep")
   log.Logger("kdb").Info("buffer pool resized", "old_size", old, "new_size", new)
   log.Logger("kdb").Warn("lock timeout, retrying", "attempt", n)
   log.Logger("kdb").Error("checksum mismatch", "page", p.ID, "expected", exp, "got", got)
   ```

4. **Use slog attributes (not `Msgf` / printf)**
   ```go
   // ✓ Good
   log.Logger("kdb").Info("transaction complete", "duration", duration, "writes", count)
   
   // ✗ Bad
   log.Logger("kdb").Infof("transaction complete in %v with %d writes", duration, count)
   ```

5. **Attach error context, don't lose the chain**
   ```go
   // ✓ Good: slog attributes capture the error
   if err != nil {
       log.Logger("kdb").Error("failed to sync WAL", "err", err)
       return fmt.Errorf("sync failed: %w", err)
   }
   
   // ✗ Bad: error info lost
   if err != nil {
       log.Logger("kdb").Error("failed")
       return err
   }
   ```

6. **Trace contextual flow with debug logging** in hot paths
   ```go
   // ✓ Good (with Debug level, disabled in prod)
   log.Logger("kdb.btree").Debug("search", "node", n.ID, "depth", depth, "key", searchKey)
   ```

#### Checklist
- [ ] All logging via `log.Logger("subsystem")`
- [ ] No `fmt.Printf` or raw `log` package in production code
- [ ] Attributes used (not formatted strings)
- [ ] No secrets in attributes (use restricted key names for sensitive data)
- [ ] Error logging includes `err` attribute
- [ ] Log levels graduated (debug → info → warn → error)
- [ ] Sensitive fields use restricted names matching redaction pattern

---

## Decision Flowchart

When starting a feature or refactoring, follow this order:

```
START
│
├─ Error handling required?
│  └─ YES → Define custom error type, map to exit code, use fmt.Errorf %w, test
│
├─ Goroutines or shared state?
│  └─ YES → Document concurrency contract, RWMutex, use -race, test
│
├─ New public API?
│  └─ YES → Design interfaces (≤3 methods), accept interfaces, return concrete
│
├─ Critical path?
│  └─ YES → Add BenchmarkCritical*, profile, maintain <10% regression threshold
│
├─ High complexity?
│  └─ YES → Table-driven tests, ≥88% coverage, fuzz invariants
│
└─ Ready to commit?
   └─ make verify (lint + test + build)
      └─ make install-hooks (pre-commit checks)
```

---

## Pre-Commit Checklist

Before pushing to a branch:

```bash
# 1. Format & lint
make format
make lint

# 2. Test everything
make test
make test-race
make bench BENCH='Critical'

# 3. Build & verify
make build
make verify  # Runs lint + test + build

# 4. Install git hooks (if not already done)
make install-hooks

# 5. Commit (hooks will block if lint/test fail)
git add .
git commit -m "feat: add X" -m "Details"
```

---

## PR Review Checklist (for reviewers)

- [ ] Error handling: custom types, %w, exit codes, no swallowing
- [ ] Concurrency: contracts documented, locks protect all shared state, -race passes
- [ ] Interfaces: ≤3 methods, accepted not returned, defined where consumed
- [ ] Testing: table-driven, ≥88% coverage, BenchmarkCritical* for hot paths, no sleeps
- [ ] Linting: `make lint` passes, no unjustified `//nolint`
- [ ] Performance: <10% regression on BenchmarkCritical*
- [ ] Logging: structured attributes, no secrets, levels graduated
- [ ] Module design: layers respected, internal/* hidden, no circular deps

---

## Invocation Scenarios

### Scenario 1: During Code Implementation
**When:** Writing a new function or package  
**Action:** Use this skill to guide design decisions *before* writing tests  
**Prompt:** "I'm implementing [feature]. Apply Go senior practices: error handling, interfaces, concurrency, logging."

### Scenario 2: Pre-Commit (Local)
**When:** Running `make install-hooks` → `.githooks/pre-commit`  
**Action:** Automated lint + test; this skill guides what to fix  
**Prompt:** "Help me fix these lint/test failures before commit"

### Scenario 3: Pre-Push Validation
**When:** Running `make verify` locally or in CI  
**Action:** Ensures all gates pass (lint, test, bench, race)  
**Prompt:** "Run the full verification suite and flag any regressions"

### Scenario 4: PR Review
**When:** Reviewing a colleague's PR  
**Action:** Apply the checklist above to give targeted feedback  
**Prompt:** "Review this PR against Go senior practices: error handling, testing, performance."

### Scenario 5: Performance Investigation
**When:** Benchmark regression detected  
**Action:** Profile and guide optimization  
**Prompt:** "BenchmarkCritical* regressed 12%. Help me profile and optimize."

---

## References

- **pcke Architecture:** [docs/architecture.md](../docs/architecture.md)
- **kdb Design:** [CLAUDE.md](../../CLAUDE.md)
- **Makefile:** Common commands: `make verify`, `make test-race`, `make bench`
- **Exit Codes:** [cmd/pcke/exitcodes.go](../../cmd/pcke/exitcodes.go)
- **Logging:** [internal/log/](../../internal/log/)
- **Config:** [internal/config/](../../internal/config/)
- **Go Guidelines:** https://golang.org/doc/effective_go, https://go.dev/blog/errors-are-values

---

## How to Use This Skill

1. **Invoke during development:** "I'm writing a parser. Apply the Go senior practices skill to guide my design."
2. **Before commit:** "Apply the pre-commit checklist to my changes."
3. **PR review:** "Use the Go best practices checklist to review this PR."
4. **Fix lint/test failures:** "Help me resolve lint and test issues using this skill."
5. **Performance tuning:** "Use the performance section to profile this regression."

This skill is **workspace-scoped** to pcke. It enforces standards expected of senior Go engineers and ensures code quality, safety, and performance across the codebase.

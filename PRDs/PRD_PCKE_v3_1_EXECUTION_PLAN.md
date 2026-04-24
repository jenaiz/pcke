# PCKE — Execution Plan (Anexo al PRD v3.1)

> **Version:** 1.0
> **Status:** Draft — pending review
> **Date:** April 2026
> **Parent:** [PRD_PCKE_v3_1.md](PRD_PCKE_v3_1.md)
> **Naturaleza:** Este documento es el **plan de ejecución** del PRD v3.1. El PRD describe "qué y por qué"; este anexo describe "cómo y cuándo". No redefine arquitectura ni decisiones de diseño; las descompone en tareas implementables con dependencias, criterios de aceptación y gates de calidad.

---

## 1. Preámbulo

### 1.1 Por qué existe este anexo

El PRD v3.1 es sólido en arquitectura, invariantes y decisiones, pero las Fases 0–4 están expresadas como **listas de entregables**, no como un **plan ejecutable**. Para arrancar a codear con confianza se necesita:

1. Una **fase de bootstrap** previa (tooling, CI, lint, release pipeline) que el PRD asume pero no describe.
2. **Fase 0 descompuesta** en tareas atómicas con orden de dependencias, incluyendo la resolución del ciclo oculto `freelist ↔ B+tree`.
3. **Definition of Done por fase** (demo, tests, métricas, cobertura).
4. **Diseño concreto** de testing, observability, configuration, error handling, cross-cutting concerns, security y distribution.
5. **Cierre de las preguntas abiertas** §13.2 que bloquean fases intermedias.

### 1.2 Cómo se consume

- Cada **tarea del DAG** (§4, §5) se convierte 1:1 en un issue de GitHub.
- Cada **fase** tiene un script `make verify-phase-N` que ejecuta su DoD (§6).
- Los **gates de CI** (§7) son no-negociables: una PR que no cumpla los umbrales no merge.
- El PRD v3.1 queda **congelado**; cambios de diseño se proponen como ADRs en `PRDs/ADRs/`.

### 1.3 Relación con el PRD v3.1

Este anexo **complementa**, no sustituye. Secciones numeradas como §4.X.Y en este documento se insertan conceptualmente después de la sección §X.Y del PRD. No hay reescrituras.

---

## 2. Decisiones de §13.2 resueltas

| # | Pregunta (PRD §13.2) | Resolución |
|---|---------------------|-----------|
| A | Tamaño máximo de repo soportado | **100K archivos / ~10M LoC** para v1.0. Benchmarks y sizing (B+tree depth, FTS segments) se calibran a este objetivo. |
| B | Compresión de valores (snappy/lz4) | **Diferida a post-v1.0.** No bloqueante; re-evaluar si el footprint disco supera 2× el tamaño del repo en benchmarks de Fase 4. |
| C | Corpus ground-truth Precision@5 | **Híbrido.** (1) Corpus sintético grande generado desde símbolos AST extraídos + doc esperado = símbolo fuente. (2) **20 queries manuales de alto nivel** curadas sobre el propio `pcke` y 1–2 repos Go de referencia, representando consultas "reales" (ej. "cómo se autentica un request", "dónde se valida el payload de webhook"). |
| D | Tokenización CJK v1 | **Segmentación con librería.** Preferencia por Go-pura (evaluar `github.com/ikawaha/kagome` para JP, `gojieba` para ZH — CGo aceptable si no hay alternativa madura). Decisión concreta de librería al abrir Fase 1. Fallback a per-codepoint si ninguna opción pasa el smoke test. |
| E | Modelo de ejecución de `pcke compact` | **Offline.** Exige DB cerrada (sin MCP corriendo, sin otro proceso con lock). Simplifica la implementación y es suficiente para v1. Online compaction es post-v1. |

---

## 3. Fase −1 — Bootstrap

**Goal:** Repo productivo con CI, lint, release pipeline y estructura vacía lista para arrancar Fase 0.

**Duración estimada:** trabajo de infraestructura, no de dominio. No se implementa lógica PCKE todavía.

### 3.1 Entregables

| # | Item | Detalle |
|---|------|---------|
| B0 | `go.mod` | Go **1.23+** fijado. Module path `github.com/jesusnavarrete/pcke`. |
| B1 | Estructura de directorios | Esqueleto completo según PRD §14, con `.gitkeep` en directorios vacíos. |
| B2 | `Makefile` | Targets: `build`, `test`, `test-race`, `fuzz`, `bench`, `lint`, `format`, `verify`, `release-dryrun`, `verify-phase-0..4`. |
| B3 | `golangci-lint` | Config con: `errcheck`, `gosec`, `govet`, `staticcheck`, `revive`, `unused`, `gofumpt`, `gocyclo` (umbral 15), `ineffassign`. |
| B4 | GitHub Actions | Workflows: `ci.yml`, `release.yml`, `nightly-fuzz.yml`. Matrix: `{darwin, linux} × {amd64, arm64}` para tests; Windows amd64 **solo build**. |
| B5 | CI jobs | `lint`, `test`, `test-race`, `fuzz-short` (5 min), `bench-gate` (benchstat, umbral 10% regresión en `BenchmarkCritical*`), `build`, `coverage` (publicar + umbral -2pp). |
| B6 | Logger base | `internal/log/logger.go` expone `Logger(subsystem string) *slog.Logger`. Nivel configurable por env `PCKE_LOG_LEVEL`. |
| B7 | Build tags | `kdbdebug` documentado y conectado a target `make test-debug`. |
| B8 | `goreleaser` | Config produciendo binarios para darwin/linux amd64+arm64. Inyección de versión vía `-ldflags "-X main.version=..."`. |
| B9 | Firmas | Binarios firmados con **sigstore/cosign** (keyless, OIDC GitHub). |
| B10 | Licencia y gobernanza | `LICENSE` (**Apache-2.0**), `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `CODEOWNERS`, templates issue/PR. |
| B11 | ADR base | Directorio `PRDs/ADRs/` + `PRDs/ADRs/0000-adr-template.md`. |
| B12 | README | Estado "pre-alpha", quickstart trivial (`make verify`), link al PRD y a este plan. |
| B13 | Pre-commit opcional | `.githooks/pre-commit` con `make lint` + `make test-short` (instalable vía `make install-hooks`). |

### 3.2 DoD Fase −1

- `make verify` termina en verde en un clon limpio.
- CI verde en PR de prueba con todos los jobs.
- `goreleaser release --snapshot --clean` produce binarios en `dist/`.
- Tag `v0.0.0-phase-minus-1` publicado con binarios dummy firmados (smoke de pipeline).

---

## 4. Fase 0 — Rediseñada como DAG

**Goal:** Storage engine crash-safe + CLI con scan/sync básico + rules/notes durables desde el primer commit (invariante del PRD §7).

### 4.1 Resolución del ciclo freelist ↔ B+tree

**Problema:** el PRD pone "freelist (B+tree-based)" y B+tree en la misma fase, pero el B+tree necesita alocar/liberar páginas vía freelist.

**Resolución en este plan:**

1. **T4 Freelist bootstrap**: lista enlazada simple en páginas reservadas (`Freelist` page type), suficiente para servir a T7.
2. **T7 B+tree**: consume el freelist bootstrap.
3. **T8 Freelist B+tree**: migración al diseño final del PRD §4.11, ejecutada una vez que B+tree es estable. La migración es un proceso one-time al `Open` si el meta page indica `freelist_format=0` (bootstrap); deja `freelist_format=1` (B+tree) al terminar.

Este puente explícito no está en el PRD y es la razón más concreta por la que Fase 0 no es arrancable tal cual.

### 4.2 DAG de tareas

Dependencias explícitas (predecesores → tarea → sucesores). Una tarea sólo puede empezar cuando **todos** sus predecesores están mergeados.

```
T0  Encoding base (varint, LE, CRC32C, record encoder/decoder schema v1)
    preds: —                                  succs: T6, T7, T9

T1  flock + LOCK/PID (abstracción unix/windows)
    preds: —                                  succs: T3

T2  Page format + header (24B) + CRC32C verify + Page type enum
    preds: —                                  succs: T3, T6

T3  File layout + growth en chunks de 16 páginas + reopen idempotente
    preds: T1, T2                             succs: T4, T5, T6, T9

T4  Freelist bootstrap (lista enlazada en Freelist pages)
    preds: T3                                 succs: T7

T5  Double-meta page + atomic swap (offset 0, 4096, active-gen a 8192)
    preds: T3                                 succs: T10

T6  Buffer pool mínimo (pin/unpin, dirty tracking, sin clock-sweep)
    preds: T0, T2, T3                         succs: T7, T9
    [clock-sweep diferido a Fase 1]

T7  B+tree (Get/Put/Delete, splits 50/50, overflow pages, cursor básico)
    preds: T0, T4, T6                         succs: T8, T10
    [merges/redistribución diferidos a Fase 1]

T8  Freelist B+tree (migración desde T4; meta flag freelist_format)
    preds: T7                                 succs: T10

T9  WAL mínimo (append + F_FULLFSYNC/fsync, replay lineal en Open)
    preds: T0, T3, T6                         succs: T10
    [sin checkpoint; WAL crece hasta cierre limpio]

T10 Transaction API (View/Update) + integración WAL+bufpool+meta swap
    preds: T5, T7, T8, T9                     succs: T11, T13, T14, T16

T11 Recovery + crash harness (subproceso + SIGKILL en hooks inyectados)
    preds: T10                                succs: DoD

T12 CLI skeleton (Cobra) + config loader (toml) + slog wiring
    preds: —  (independiente del storage)     succs: T13

T13 Analysis Fase 0 (file tree, go-git, secretos, heurísticas path)
    preds: T10, T12                           succs: T15

T14 Secondary indexes mínimos (by_module, by_tag)
    preds: T10                                succs: T15

T15 Output Markdown (.context/ + .github/copilot-instructions.md + .claude/CLAUDE.md)
    preds: T13, T14                           succs: DoD

T16 Diagnostics básico (Stats struct parcial: pages, WAL, trees)
    preds: T10                                succs: DoD
```

### 4.3 Olas (orden de ejecución)

Cada ola agrupa tareas cuyos predecesores están completos. Tareas dentro de una ola son paralelizables.

- **Ola 1 (sin deps):** T0, T1, T2, T12
- **Ola 2 (requiere T1, T2):** T3
- **Ola 3 (requiere T3):** T4, T5, T6
- **Ola 4 (requiere T4+T6 para T7; requiere T3+T6 para T9):** T7, T9
- **Ola 5 (requiere T7):** T8
- **Ola 6 (requiere T5, T7, T8, T9):** T10
- **Ola 7 (requiere T10):** T11, T13, T14, T16
- **Ola 8 (requiere T13, T14):** T15

**Ruta crítica** (la que define el tiempo mínimo de Fase 0): T2 → T3 → T6 → T7 → T8 → T10 → T13 → T15.

### 4.4 Criterios de aceptación por tarea

Cada tarea se convierte en un issue que lleva su DoD individual. Criterios obligatorios listados; los issues pueden añadir criterios específicos pero no quitar.

| Tarea | Criterio principal |
|-------|--------------------|
| T0 | Round-trip encode/decode de todos los tipos de PRD §4.14; fuzz 5 min sin panic ni diferencias de round-trip. |
| T1 | Dos procesos compitiendo por `Open` → uno obtiene lock, otro `ErrDBLocked` sin side effects en archivos. |
| T2 | CRC detecta corrupción 1-bit en todas las posiciones (property test: flip de cada byte del payload). |
| T3 | `Open`/`Close` 1000× sobre la misma ruta es idempotente; `growth chunk=16` verificado con `stat`. |
| T4 | 100K alloc/free aleatorios; freelist íntegra tras crash en cualquier punto (crash harness reutilizable). |
| T5 | Crash inyectado durante swap → exactamente una generación válida readable; detección de generación inválida cubierta. |
| T6 | Pin/unpin bajo race detector sin data races; dirty tracking correcto en 10K mutaciones. |
| T7 | Property test (`rapid`): 10K ops aleatorias mantienen invariantes B+tree (sorted, balanced, no orphan). Overflow chains roundtrip. |
| T8 | Migración freelist idempotente; ejecutar dos veces es no-op; `freelist_format` avanza 0→1 exactamente una vez. |
| T9 | Replay es función pura de (WAL + meta); determinístico ante WAL corrupt-tail; 0 panics en fuzz de 5 min. |
| T10 | `Update` retornando error deja DB indistinguible de estado pre-tx (property test de equivalencia). |
| T11 | Crash inyectado en cada uno de ≥20 hooks WAL: 0 violaciones de invariantes PRD §4.2 (#1–4, #7). |
| T12 | `pcke --help`, `pcke --version`, config loader resuelve precedencia flag > env > repo > user > default en tests tabla. |
| T13 | Secretos: 100 fixtures con AWS keys, `.env`, PEM → 0 llegan al índice. `.gitignore` respetado. |
| T14 | Consistencia índice↔primary: 10K mutaciones aleatorias, inverted scan de índice = scan primary filtrado. |
| T15 | Golden tests del output Markdown sobre repo fixture estable; diff = 0. |
| T16 | `pcke diagnostics --format=json` produce JSON válido con schema fijo; fields no-null para DB no vacía. |

### 4.5 API congelada al cierre de Fase 0

Para que Fase 1 arranque sin refactor forzado, al cierre de Fase 0 se **congela** la siguiente superficie pública de `internal/kdb`. Cambios posteriores requieren ADR.

- `kdb.Open(path string, opts Options) (*DB, error)` + `(*DB).Close()`.
- `(*DB).View(ctx, func(*ReadTx) error) error`
- `(*DB).Update(ctx, func(*WriteTx) error) error`
- Trees accesibles vía `tx.Tree(name string) *Tree` con métodos `Get`, `Put`, `Delete`, `Range`, `Scan`, `Cursor`.
- `(*DB).Stats() Stats` (estructura puede **crecer** entre fases; campos existentes no cambian semántica).
- Errores sentinela listados en §10.
- Formato on-disk de: page header, record encoding v1, double-meta layout, WAL record format.

Lo que **no** se congela en Fase 0 (puede cambiar en Fase 1): políticas internas del buffer pool, layout interno de WAL segments, estructura del freelist una vez migrado a B+tree, formato de secondary indexes.

### 4.6 Alcance de concurrencia en Fase 0

El PRD §4.6 promete snapshot isolation completa (readers concurrentes con un writer vía CoW de meta). Esto **no** se implementa en Fase 0; la implementación real de CoW vive en `F2.T1`.

**Contrato de Fase 0:**
- Un único `Update` activo a la vez (writer mutex).
- Readers (`View`) concurrentes entre sí **sí soportados**, pero **serialize contra writers** mediante un RWMutex global a nivel de DB.
- La API `View(ctx, fn)` no cambia entre Fase 0 y Fase 2 → el upgrade a CoW es transparente para consumidores.

Esta limitación queda documentada en godoc de `View` y en `docs/architecture.md` con nota *"Phase 0 provides reader–writer exclusion; true snapshot isolation ships in Phase 2."*

---

## 5. Fases 1–4 refinadas

Cada fase declara **prerrequisitos de entrada** (lo que tiene que estar mergeado y taggeado en la fase previa), un **DAG interno** (tareas con deps), y la **API que congela al cerrarse**. Esto permite empezar cualquier fase independientemente en cuanto sus prereqs estén firmados.

### 5.1 Fase 1 — Search & Checkpointing

**Prerrequisitos de entrada (hard gate):**
- Tag `v0.0.0` (Fase 0 cerrada) publicado.
- `make verify-phase-0` verde en `main`.
- API §4.5 congelada.
- Decisión P-1 (librería CJK) tomada y registrada en ADR.
- Corpus Precision@5 manual (20 queries) existe en `testdata/fts/queries.json`.

**DAG interno:**

```
F1.T1  Clock-sweep buffer pool + métrica hit rate
       preds: —                              succs: DoD

F1.T11 Merges B+tree + redistribución
       preds: —                              succs: DoD

F1.T2  Checkpoint fuzzy (dirty page table, flush ordenado, meta update)
       preds: —                              succs: F1.T3, F1.T8

F1.T3  WAL segment rotation + truncation post-checkpoint
       preds: F1.T2                          succs: DoD

F1.T4  FTS tokenizer (word boundary + camelCase/snake + CJK segmenter)
       preds: —                              succs: F1.T5, F1.T9

F1.T5  FTS in-memory segment + flush on commit
       preds: F1.T4                          succs: F1.T6, F1.T7, F1.T8

F1.T6  FTS posting encoding (VarInt + delta + gamma)
       preds: F1.T5                          succs: F1.T8, F1.T9

F1.T7  FTS tombstones + delete path
       preds: F1.T5                          succs: F1.T8

F1.T8  FTS tiered merge at checkpoint
       preds: F1.T2, F1.T5, F1.T6, F1.T7     succs: F1.T9

F1.T9  BM25 scorer + paridad con impl. referencia
       preds: F1.T4, F1.T6                   succs: F1.T10

F1.T12 Corpus Precision@5 harness de evaluación
       preds: —                              succs: F1.T10

F1.T10 `pcke recall` CLI + planner trivial (single-field FTS)
       preds: F1.T9, F1.T12                  succs: DoD
```

**Ruta crítica:** F1.T4 → F1.T5 → F1.T6 → F1.T9 → F1.T10.

**API que congela al cierre:** formato de segmento FTS on-disk, contrato BM25 (parámetros), tokenizer API, interfaz de checkpoint (`DB.Checkpoint(ctx)`).

### 5.2 Fase 2 — Deep Analysis & MCP

**Prerrequisitos de entrada:**
- Tag `v0.1.0` (Fase 1 cerrada) + `make verify-phase-1` verde.
- Decisión P-2 (3 repos de referencia concretos) tomada y submodules shallow añadidos.
- CGo toolchain disponible en CI para darwin+linux (prebuilts verificados).

**DAG interno:**

```
F2.T1  CoW de meta para snapshot isolation real (pages versioning)
       preds: —                              succs: DoD

F2.T2  Secondary indexes completos (by_file, by_type) + consistencia
       preds: —                              succs: DoD

F2.T3  tree-sitter integration (CGo) + binding + prebuilts
       preds: —                              succs: F2.T4

F2.T4  `pcke scan --deep`: entity extraction + pattern recognition
       preds: F2.T3                          succs: F2.T5

F2.T5  `relations` populator desde import graph
       preds: F2.T4                          succs: DoD

F2.T10 Rename detection vía go-git + evolution log `renamed`
       preds: —                              succs: DoD

F2.T9  Branch mismatch detection en invocación CLI
       preds: —                              succs: DoD

F2.T8  `pcke compact` offline
       preds: F2.T2                          succs: DoD

F2.T6  MCP server stdio: tools recall/get_module_context/get_constraints/get_history
       preds: F2.T5, F2.T2                   succs: F2.T7

F2.T7  MCP resources pcke://architecture | constraints | decisions
       preds: F2.T6                          succs: DoD
```

**Ruta crítica:** F2.T3 → F2.T4 → F2.T5 → F2.T6 → F2.T7.

**API que congela al cierre:** contrato MCP (tools + resources), formato de CoW meta, API de `relations`.

### 5.3 Fase 3 — Query Language & Polish

**Prerrequisitos de entrada:**
- Tag `v0.2.0` (Fase 2 cerrada) + `make verify-phase-2` verde.
- Bench baselines publicadas (reutilizables como golden).

**DAG interno:**

```
F3.T1  Lexer + parser recursivo descendente (gramática PRD §4.16)
       preds: —                              succs: F3.T2

F3.T2  AST + type check (field existence, tipo vs operador)
       preds: F3.T1                          succs: F3.T3

F3.T3  Planner: index selection, AND-intersect, OR-union
       preds: F3.T2                          succs: F3.T4, F3.T5

F3.T4  Executor + materializer de order by sin índice
       preds: F3.T3                          succs: F3.T6

F3.T5  `pcke explain` → plan human-readable
       preds: F3.T3                          succs: DoD

F3.T6  `pcke export` (json, yaml)
       preds: F3.T4                          succs: DoD

F3.T7  In-code annotations parser (@pcke-rule, @pcke-lesson)
       preds: —                              succs: DoD

F3.T8  Bench suite 1K/10K/100K archivos + CI gate activo
       preds: F3.T4                          succs: DoD
```

**Ruta crítica:** F3.T1 → F3.T2 → F3.T3 → F3.T4 → F3.T6.

**Nota de paralelización:** F3.T7 (annotations) y F3.T1–T6 son técnicamente independientes; un segundo stream puede empezar F3.T7 desde el inicio de la fase.

**API que congela al cierre:** gramática del query DSL, formato de export, sintaxis de annotations in-code.

### 5.4 Fase 4 — v1.0

**Prerrequisitos de entrada:**
- Tag `v0.3.0` (Fase 3 cerrada) + `make verify-phase-3` verde.
- Decisión P-3 (sitio de docs) tomada.
- Decisión §13.2-B (compresión) preparada para evaluación con benchmarks reales.

**Tareas (paralelas salvo donde se indica):**

- `F4.T1` Buffer pool tuning + sizing dinámico. preds: —
- `F4.T2` Group commit optimization (intra-tx). preds: —
- `F4.T3` `pcke migrate` chunked + idempotente + verificación schema_version. preds: —
- `F4.T4` Multi-repo shared DB (**stretch goal**, sólo si el resto está verde). preds: F4.T3
- `F4.T5` Docs site + release notes consolidadas. preds: —
- `F4.T6` Evaluar §13.2-B (compresión) con benchmarks; decidir go/no-go (ADR). preds: F4.T1

**API que congela al cierre:** toda. v1.0 es el commit de compatibilidad.

---

## 6. Definition of Done por fase

| Fase | Entrada (prereqs) | Demo ejecutable | Test gates | Métricas mínimas | Cobertura `kdb` | Tag al cierre |
|------|-------------------|-----------------|------------|------------------|-----------------|---------------|
| −1 | Repo vacío | `make verify` verde en clon limpio; release dryrun produce binarios firmados | Lint, build, smoke | — | — | `v0.0.0-phase-minus-1` |
| 0 | Tag Fase −1 + CI verde | `pcke init && pcke scan && pcke sync` sobre el propio repo `pcke`; harness inyecta 100 crashes durante scan → rules/notes previas intactas, 0 CRC errors | Property tests B+tree, crash harness (≥20 hooks × 10 seeds), encoder fuzz 5 min, invariantes PRD §4.2 #1–4, #7 verificadas | `scan --full` 10K archivos < **15 s** en cold start | **≥ 70 %** | `v0.0.0` |
| 1 | Tag `v0.0.0` + ADR P-1 (CJK) + corpus manual 20 queries | `pcke recall` sobre DB con 100K nodos responde corpus híbrido; soak 1 h (scan+crash loop) sin drift | Precision@5 ≥ 70 %, BM25 parity fixtures, FTS consistency (10K mutaciones aleatorias) | Recall p99 < **80 ms** en 100K nodos; hit rate bufpool ≥ 85 % | **≥ 80 %** | `v0.1.0` |
| 2 | Tag `v0.1.0` + ADR P-2 (repos referencia) + CGo toolchain verificado | Claude Code conectado vía MCP responde a prompt real usando contexto del repo; `pcke compact` reduce tamaño > 10 % tras churn sintético | Snapshot isolation tests (N readers vs writer bajo race detector), AST accuracy ≥ 90 % sobre 3 repos referencia, MCP contract tests | `scan --deep` 10K archivos < **30 s** | **≥ 85 %** | `v0.2.0` |
| 3 | Tag `v0.2.0` + bench baselines publicadas | `pcke query` + `pcke explain` resuelven 10 queries demo del PRD §4.16 | Parser AST snapshot tests, planner elige índice correcto en 100 % casos sintéticos, bench CI gate activo | Query p99 < **50 ms** en 100K nodos | **≥ 85 %** | `v0.3.0` |
| 4 | Tag `v0.3.0` + ADR P-3 (docs site) + decisión §13.2-B preparada | v1.0 release candidate: todas las métricas PRD §9 cumplidas | Suite completa + regression gate nightly | Todas las métricas §9 del PRD | **≥ 90 %** | `v1.0.0` |

Cada fase tiene un script `make verify-phase-N` que ejecuta su DoD. CI falla si la métrica mínima regresa. **Una fase sólo se puede abrir cuando su columna "Entrada" está firmada.**

---

## 7. Testing Infrastructure

### 7.1 Librerías y herramientas

| Área | Elección | Rationale |
|------|----------|-----------|
| Property-based testing | **`pgregory.net/rapid`** | Mejor shrinking, API más moderna que gopter, mantenida. |
| Fuzzing | **`go test -fuzz`** (nativo) | Integrado; corpora en `testdata/fuzz/`. |
| Bench diff | **`benchstat`** + CI gate | Umbral 10 % en `BenchmarkCritical*`. |
| Race detection | **`go test -race`** | Job dedicado en CI; obligatorio. |
| Coverage | **`go test -cover -covermode=atomic`** | Publica a artifact; gate -2 pp vs baseline branch principal. |
| Mocks | Evitar; usar fakes en `internal/*/testutil/` | Menos fragilidad. |

### 7.2 Crash harness (diseño)

Paquete `internal/kdb/testutil/crashsim`:

1. El código productivo expone puntos de inyección con build tag `kdbdebug`:
   ```go
   //go:build kdbdebug
   func checkCrashHook(name string) { if os.Getenv("PCKE_CRASH_AT") == name { os.Exit(137) } }
   ```
2. Hooks ubicados en: pre-WAL-write, pre-fsync-WAL, post-fsync-WAL-pre-meta, pre-meta-swap, post-meta-swap-pre-truncate, pre-bufpool-flush, post-bufpool-flush-pre-fsync-data. Lista fija enumerada en `hooks.go`.
3. Test orquesta subproceso vía `os/exec` con `PCKE_CRASH_AT` setado; tras exit, reabre DB, valida invariantes.
4. Seeds variados para explorar estados (timing de commits previos).

### 7.3 Corpora

| Corpus | Ubicación | Uso |
|--------|-----------|-----|
| FTS queries híbrido | `testdata/fts/queries.json` | Precision@5 (Fase 1) |
| Repos de referencia | Shallow submodules en `testdata/repos/` | AST accuracy (Fase 2), Precision@5 manual |
| Fuzz records | `internal/kdb/encoding/testdata/fuzz/` | Encoder robustness (Fase 0) |
| Fuzz WAL records | `internal/kdb/wal/testdata/fuzz/` | Recovery robustness (Fase 0) |
| Fuzz B+tree keys | `internal/kdb/btree/testdata/fuzz/` | Tree robustness (Fase 0) |
| Fuzz queries | `internal/kdb/query/testdata/fuzz/` | Parser robustness (Fase 3) |
| Golden Markdown | `internal/output/testdata/golden/` | Output stability (Fase 0+) |
| Secretos positivos/negativos | `internal/analysis/testdata/secrets/` | Redacción (Fase 0) |

Repos de referencia candidatos (acotar por tamaño): prometheus/prometheus (~400k LoC Go), grafana/grafana (~4M LoC mixto), cockroachdb/cockroach (grande, representa el límite). Selección final al abrir Fase 2.

### 7.4 CI gates (resumen)

| Gate | Umbral | Fase activa |
|------|--------|-------------|
| Lint | 0 warnings | desde −1 |
| Test + race | 100 % passing | desde −1 |
| Coverage regression | ≤ 2 pp drop | desde 0 |
| Fuzz short (5 min PR) | 0 crashes | desde 0 |
| Fuzz long (nightly) | 0 crashes | desde 0 |
| Bench regression | ≤ 10 % en `BenchmarkCritical*` | desde 1 |
| Invariant debug tests | 0 violaciones | desde 0 |
| MCP contract tests | 100 % passing | desde 2 |

---

## 8. Observability & Logging

### 8.1 Logger

- **Raíz:** `slog` (stdlib Go 1.21+).
- **Factory:** `internal/log/logger.go → Logger(subsystem string) *slog.Logger`.
- **Subsistemas:** `kdb.page`, `kdb.bufpool`, `kdb.wal`, `kdb.btree`, `kdb.tx`, `kdb.fts`, `kdb.query`, `kdb.meta`, `pcke.scan`, `pcke.mcp`, `pcke.cli`, `pcke.output`.
- **Campos estándar por evento:** `tx_id`, `lsn`, `page_id`, `tree`, `subsystem`, `op`. Adjuntados vía `slog.With`.
- **Nivel por env:** `PCKE_LOG_LEVEL=debug|info|warn|error` (default `info`).
- **Nivel por subsistema:** `PCKE_LOG_LEVEL_KDB_WAL=debug` (override granular).

### 8.2 Debug build (`kdbdebug`)

- Activa `assert()` de invariantes §4.2 tras cada mutación en buffer pool, B+tree, WAL.
- Activa crash hooks (§7.2).
- Nunca se compila en releases (`go build -tags=kdbdebug` solo en tests).
- `make test-debug` ejecuta toda la suite con el tag activo.

### 8.3 Redacción en logs

Campos con nombre matching `(?i)(secret|token|key|password|credential)` se redactan a `[REDACTED]` en el handler custom de slog. Aplica también a `diagnostics --pages`.

---

## 9. Configuration System

### 9.1 Archivos y precedencia

```
flag CLI  >  env PCKE_*  >  .pcke/config.toml (repo)  >  ~/.config/pcke/config.toml (user)  >  defaults
```

- `.pcke/config.toml` es generado por `pcke init` con defaults comentados.
- El user-level es opcional; si no existe, se ignora silenciosamente.
- Validación completa al `Open` de la DB; errores → `ErrInvalidConfig` con mensaje actionable.

### 9.2 Esquema (v1)

```toml
[scan]
redact_secrets    = true                # bool
include_ignored   = false               # bool
exclude_globs     = []                  # []string
max_file_bytes    = 2_097_152           # int, default 2 MiB

[kdb]
buffer_pool_mb          = 0             # 0 = auto (min(256, 25% RAM))
wal_segment_mb          = 16
checkpoint_wal_mb       = 32
checkpoint_interval_sec = 60
graceful_shutdown_sec   = 10

[fts]
tokenizer_cjk_mode      = "segmenter"   # "per_codepoint" | "segmenter"
merge_tier_threshold    = 10

[mcp]
read_timeout_sec        = 30

[commit]
commit_db               = false         # si true, .pcke/ no se añade a .gitignore
```

### 9.3 Comandos

- `pcke config get <key>` — imprime valor efectivo + origen (`flag|env|repo|user|default`).
- `pcke config set <key> <value> [--user]` — escribe al archivo correspondiente.
- `pcke config list [--effective]` — lista completa con orígenes.

---

## 10. Error Taxonomy

### 10.1 Sentinelas

Declarados en `internal/kdb/errors.go` y `cmd/pcke/errors.go`:

| Código | Cuándo |
|--------|--------|
| `ErrDBLocked` | Otro proceso tiene el flock. |
| `ErrValueTooLarge` | Valor > 16 MB. |
| `ErrKeyTooLarge` | Key > 1/4 usable space y overflow fallido. |
| `ErrKeyNotFound` | Punto `Get` sin resultado. |
| `ErrTxAborted` | Transacción abortada explícita o por panic. |
| `ErrTxReadOnly` | Mutación en `View`. |
| `ErrTxClosed` | Uso tras `Commit`/`Rollback`. |
| `ErrCorrupted` | Inconsistencia estructural detectada. |
| `ErrChecksumMismatch` | CRC32C fallido en página. |
| `ErrWALTornTail` | Último registro WAL truncado (recuperable, se descarta). |
| `ErrWALRecoveryFailed` | Recovery no pudo completar (no recuperable). |
| `ErrSchemaVersionMismatch` | Decoder no soporta versión del registro. |
| `ErrFormatVersionTooNew` | DB escrita por kdb más nuevo. |
| `ErrNFSDetected` | Warning, no abort por defecto. |
| `ErrInvalidConfig` | Config inválida al Open. |
| `ErrUnknownCollection` | Query a colección inexistente. |
| `ErrQuerySyntax` | Parser query DSL falló. |
| `ErrBranchMismatch` | Warning, no abort. |
| `ErrDBClosed` | Operación post-Close. |

### 10.2 Wrapping

- **Siempre** `fmt.Errorf("subsystem: ctx: %w", err)`.
- **Nunca** `errors.New` con el mismo mensaje dos veces en el mismo paquete.
- Top-level CLI convierte a exit code:

| Exit code | Clase |
|-----------|-------|
| 0 | Success |
| 1 | User error (flag, input inválido) |
| 2 | Config error |
| 3 | Lock conflict (`ErrDBLocked`) |
| 4 | Corruption (`ErrCorrupted`, `ErrChecksumMismatch`, `ErrWALRecoveryFailed`) |
| 5 | Internal / panic |
| 6 | Schema / format version (`ErrSchemaVersionMismatch`, `ErrFormatVersionTooNew`) |

---

## 11. Cross-Cutting Concerns

### 11.1 `context.Context`

Operaciones que aceptan `ctx`:

- `DB.View(ctx, fn)`, `DB.Update(ctx, fn)`
- `DB.Checkpoint(ctx)`, `DB.Compact(ctx)`
- `pcke scan`, `pcke recall`, `pcke query`, `pcke sync`, `pcke export`
- MCP server: ctx propagado del request.

**Punto de seguridad de cancelación:** entre tx, entre páginas leídas en scan, entre documentos tokenizados. No interrumpe un `fsync` en curso ni un recovery.

### 11.2 Panic recovery

`DB.Update` implementa:

```go
defer func() {
    if r := recover(); r != nil {
        tx.abort()
        // log con stack
        panic(r) // re-panic tras limpiar
    }
}()
```

Garantiza invariantes tras panic del closure del usuario. Logs incluyen `tx_id` y stack trace.

### 11.3 Signal handling

- CLI y MCP server registran handler para `SIGINT`/`SIGTERM`.
- Primer signal: `ctx.Cancel()` + espera hasta `kdb.graceful_shutdown_sec` (default 10 s).
- Segundo signal: `os.Exit(130)` inmediato (aceptando que WAL pendiente puede perderse, pero ya commited transactions están fsync'd).

### 11.4 Backpressure

- **Scan:** si `dirty_pages / buffer_pool_size > 0.8`, pausa tokenization hasta bajar a 0.6 (high/low watermark).
- **FTS in-memory segment:** flush forzado si supera 64 MB (además del flush normal en commit).
- **Overflow page chains:** limitadas a 4096 páginas por valor (≈ 16 MB match con `ErrValueTooLarge`).

---

## 12. Security Model

### 12.1 Threat model

**Dentro de scope:**
- Atacante local con acceso lectura al filesystem no debe leer secretos indexados → filtrado en `scan` §5.5.5.
- Código malicioso en el repo escaneado no debe ejecutar código arbitrario en `pcke` → el analyzer no evalúa, solo parsea AST.
- Prompt injection en contenido indexado no debe escapar vía MCP respuestas → redacción + marcado de origen.

**Fuera de scope:**
- Atacante con acceso escritura al `.pcke/` (asume trust boundary de usuario local).
- Side channels (timing, cache).
- Supply chain (mitigado por sigstore en distribución).

### 12.2 Permisos filesystem

- `.pcke/` creado con **0700**.
- Archivos `.kdb`, `.wal` creados con **0600**.
- Si al `Open` los permisos son más laxos: log warning + (opcional) `PCKE_STRICT_PERMS=1` aborta.

### 12.3 Validación de inputs CLI

- `--file <path>`: rechaza rutas absolutas fuera del repo (`filepath.Rel` debe no empezar con `..`).
- `--scope`: regex estricta `^(global|module:[\w.-]+|file:[^\0]+)$`.
- `--tag`: cada tag debe matchear `^[\w-]{1,64}$`.
- Query DSL: parser con grammar cerrada; ningún modo "eval" ni interpolación.

### 12.4 Redacción defensiva

- Aplica en: output de `recall`, responses MCP, `diagnostics --pages`, logs estructurados.
- Doble filtrado: aunque el contenido indexado ya está limpio, se re-aplica el patrón de redacción en la ruta de lectura (defense in depth).

---

## 13. Distribution & Release

### 13.1 Matriz de soporte

| Plataforma | Test | Build | Binario release |
|------------|------|-------|-----------------|
| darwin amd64 | sí | sí | sí |
| darwin arm64 | sí | sí | sí |
| linux amd64 | sí | sí | sí |
| linux arm64 | sí | sí | sí |
| windows amd64 | no (Fase ≤3) / sí (Fase 4 best-effort) | sí | sí (Fase 2+) |

### 13.2 CGo (tree-sitter, Fase 2)

- Linux: build nativo trivial.
- Darwin: cross-compile vía zig o clang en CI.
- Windows: diferido a Fase 4; se evalúa WASM wrapper si CGo cross-compile resulta frágil.
- Prebuilts publicados en releases para evitar CGo en tiempo de `go install` cuando sea posible.

### 13.3 Versionado

- **Pre-1.0:** `0.X.Y` donde `X = fase completada` (0.0 post-Fase 0, 0.1 post-Fase 1, …), `Y` = patches.
- Breaking changes permitidos entre `X` menores.
- Schema/format changes requieren `pcke migrate` (Fase 4+).
- **v1.0:** primer release con estabilidad API + format frozen.

### 13.4 Distribución

- `go install github.com/jesusnavarrete/pcke@latest` desde Fase 0.
- **Homebrew tap** (`jesusnavarrete/pcke`) desde Fase 1.
- **GitHub releases** con binarios firmados vía sigstore desde Fase 0.
- **Checksums** SHA-256 publicados junto con binarios.

---

## 14. Docs por fase

| Fase | Deliverable |
|------|-------------|
| −1 | `README.md` pre-alpha, `CONTRIBUTING.md`, template ADR |
| 0 | `docs/architecture.md` (expande PRD §3), `docs/kdb-invariants.md` (tests-as-doc de §4.2), godoc completo de API pública de `internal/kdb` |
| 1 | `docs/fts.md` (modelo BM25, segment layout, tokenization, CJK decisión) |
| 2 | `docs/mcp.md` (setup Claude Code + Copilot paso a paso, ejemplos tools/resources, troubleshooting) |
| 3 | `docs/query-language.md` (gramática + ejemplos + planner traces) |
| 4 | Sitio completo (mdbook propuesto), guía de migración, release notes consolidadas, tutorial end-to-end |

---

## 15. Preguntas abiertas restantes

Las no resueltas en §2 se listan aquí con ventana de decisión:

| # | Pregunta | Deadline |
|---|----------|----------|
| 13.2-B | Compresión de valores (snappy/lz4) | Antes de cerrar Fase 4 (con benchmarks reales) |
| P-1 | Librería CJK específica | Al abrir Fase 1 (tras spike corto) |
| P-2 | Repos de referencia concretos para AST accuracy | Al abrir Fase 2 |
| P-3 | Sitio de docs: mdbook vs docusaurus vs otro | Al abrir Fase 4 |
| P-4 | Política de firmas: sigstore keyless vs GPG | Antes del primer release de Fase 0 |

---

## 16. Handoff a implementación

Orden sugerido tras aprobación de este plan:

1. **PR-1:** Fase −1 completa (bootstrap, CI, lint, release dryrun). Tag `v0.0.0-phase-minus-1`.
2. **PR-2..N:** Una PR por tarea Tx de Fase 0, en orden del DAG (§4.2). Cada PR incluye tests y actualiza godoc.
3. **PR-cierre-fase-0:** Ejecuta `make verify-phase-0` + publica artefacto de dogfooding (pcke indexándose a sí mismo). Tag `v0.0.0`.
4. Repetir estructura para Fases 1–4.

Cada PR debe:
- Referenciar la tarea del DAG (ej. "Closes #T7 — B+tree Put/Delete/splits").
- Pasar todos los CI gates.
- Actualizar cobertura sin regresar > 2 pp.
- Si introduce decisión de diseño no cubierta por PRD/anexo: añadir ADR.

---

### End of Execution Plan

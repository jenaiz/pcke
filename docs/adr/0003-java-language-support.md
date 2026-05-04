# ADR-0003: Java Language Support for AST Entity Extraction

> **Status:** Accepted
> **Date:** 2026-04-25
> **Authors:** Jesus Navarrete
> **Supersedes:** —

## Context

The AST analysis module (`internal/analysis/ast/`) currently supports four
languages: Go, Python, JavaScript, and TypeScript. Java is one of the most
widely used languages in enterprise codebases and a frequent target for the
kind of codebase knowledge extraction that `pcke scan --deep` provides.

The `go-tree-sitter` library already ships a Java grammar binding
(`github.com/smacker/go-tree-sitter/java`) at the same version pinned in
`go.mod` (`v0.0.0-20240827094217-dd81d9e9be82`), so no new dependency is
required — only an additional import.

Java introduces two first-class entity types that have no direct mapping in
the current `EntityKind` enum:

- **Enums** (`enum`) — a core language construct; mapping them to `class`
  loses semantic precision.
- **Annotation types** (`@interface`) — ubiquitous in frameworks (Spring,
  Jakarta EE, Lombok); mapping them to `interface` is misleading.

## Decision

### 1. New `EntityKind` values

Two new kinds are added to the `EntityKind` enum in
`internal/analysis/ast/ast.go`:

| Constant         | Value          | Description                        |
|------------------|----------------|------------------------------------|
| `KindEnum`       | `"enum"`       | Enum declarations                  |
| `KindAnnotation` | `"annotation"` | Annotation type (`@interface`) declarations |

These kinds are language-agnostic and may be reused by future languages
(e.g., TypeScript `enum`, Python `enum.Enum` subclasses).

Existing kinds reused for Java:

| Java construct          | EntityKind     |
|-------------------------|----------------|
| `class`                 | `KindClass`    |
| `interface`             | `KindInterface`|
| `enum`                  | `KindEnum`     |
| `@interface`            | `KindAnnotation`|
| method                  | `KindMethod`   |
| `static final` constant| `KindConstant` |

### 2. Language integration in `ast.go`

- Add `LangJava` to the `Lang` enum.
- Map `.java` in `langFromExt()`.
- Return `"Java"` from `langName()`.
- Return `java.GetLanguage()` from `tsLang()`.
- Add `case LangJava` in the `ParseBytes()` switch, dispatching to
  `extractJavaEntities()` and `extractJavaImports()`.

### 3. Java extractor

A new file `internal/analysis/ast/extract_java.go` will implement:

- `extractJavaEntities(root, src)` — walks the tree-sitter Java AST to
  extract classes, interfaces, enums, annotation types, methods, and
  `static final` constants.
- `extractJavaImports(root, src)` — extracts `import` and `import static`
  statements.

The extractor follows the same pattern as `extract_go.go`,
`extract_python.go`, and `extract_javascript.go`.

### 4. Reference repository for accuracy validation

Extending the ADR-0002 accuracy harness, the following Java repository is
added to the corpus:

| Language | Repository      | Approx LOC | Why                                                         |
|----------|-----------------|------------|-------------------------------------------------------------|
| Java     | `square/okhttp` | ~10 K      | Classes, interfaces, enums, annotations, builders, interceptor pattern. Exercises all Java entity types. |

The repo will be pinned to a release tag in `testdata/ast/repos.json`.
A hand-curated golden file (`testdata/ast/golden/okhttp.json`) will list
expected entities with name, kind, and file path.

The same F1 ≥ 90% threshold from ADR-0002 applies.

## Consequences

### Positive

- Five languages covered — adds the most-requested enterprise language.
- `KindEnum` and `KindAnnotation` are reusable across future languages,
  improving semantic precision for all extractors.
- No new dependency: the Java grammar is already bundled in the existing
  `go-tree-sitter` module.

### Negative

- Hand-curating the `okhttp` golden file requires ~3 h of manual effort
  (larger repo than chi, express, or flask).
- The accuracy test budget increases: ~10 K LOC adds ~5–8 s to the harness
  run, though still within the 30 s total budget.

### Risks

- **Inner classes and generics** — Java's nested class declarations and
  generic type parameters may complicate entity extraction. Mitigation:
  start with top-level and first-level inner classes; skip anonymous classes.
- **Annotation processors** — generated code (`*_Generated.java`) may
  pollute entity counts. Mitigation: exclude files matching common generated
  patterns (`**/generated/**`, `*_Generated.java`).
- **okhttp tag deletion** — same mitigation as ADR-0002: pin to release
  tags and cache clones in CI.

## Alternatives Considered

1. **`google/gson`** (~5 K LOC) — rejected: smaller but exercises fewer
   entity types (no annotations, minimal enum usage).
2. **`square/retrofit`** (~4 K LOC) — rejected: too annotation-heavy,
   unbalanced representation of Java constructs.
3. **Map `enum` → `KindClass` and `@interface` → `KindInterface`** —
   rejected: loses semantic precision and makes downstream consumers
   (e.g., architecture diagrams, search filters) less useful.

## References

- ADR-0002: `PRDs/ADRs/0002-ast-reference-repos.md`
- PRD v3.1: `PRDs/PRD_PCKE_v3_1.md`
- Execution Plan: `PRDs/PRD_PCKE_v3_1_EXECUTION_PLAN.md`
- tree-sitter Java binding: <https://pkg.go.dev/github.com/smacker/go-tree-sitter/java>

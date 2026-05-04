# ADR-0001: CJK Tokenization Strategy for Phase 1

> **Status:** Accepted
> **Date:** 2026-04-25
> **Authors:** jenaiz
> **Supersedes:** —

## Context

Phase 1 introduces full-text search (FTS) with a code-aware tokenizer
(F1.T4). The Execution Plan §2-D resolves the open question from PRD §13.2
by upgrading CJK support from per-codepoint tokens to library-based
segmentation, with a fallback to per-codepoint if no library passes a smoke
test.

We evaluated the following options for CJK tokenization:

| Library | Language coverage | Pure Go? | Maturity | Dict size |
|---------|------------------|----------|----------|-----------|
| `ikawaha/kagome` | Japanese only | Yes | Mature (v2) | ~50 MB |
| `yanyiwu/gojieba` | Chinese only | No (CGo) | Stable | ~30 MB |
| Character bigram (stdlib) | CJK universal | Yes | N/A | 0 |

The primary use case is indexing **code comments and documentation** in
multilingual repositories. Queries are typically short technical phrases,
not natural-language prose.

## Decision

Use **character bigram segmentation** for all CJK text in Phase 1.

The tokenizer detects CJK codepoints via `unicode.Is(unicode.Han, r)`,
`unicode.Is(unicode.Katakana, r)`, `unicode.Is(unicode.Hiragana, r)`, and
`unicode.Is(unicode.Hangul, r)`. Consecutive CJK codepoints are emitted as
overlapping bigrams (e.g., "東京都" → ["東京", "京都"]). A single trailing
codepoint is emitted as a unigram.

This approach:

1. Covers Chinese, Japanese, and Korean uniformly — no per-language
   dictionary or model.
2. Keeps the build pure Go with zero external dependencies for the
   tokenizer.
3. Avoids embedding large dictionary files (50+ MB) in the binary.
4. Is the same strategy used by Elasticsearch's `cjk_analyzer` and
   Lucene's `CJKBigramFilter`, proven effective for search workloads.

## Consequences

### Positive

- Zero dependency cost: no CGo, no dictionary files, no version pinning.
- Universal CJK coverage from a single code path.
- Predictable token output: every CJK character pair is a token, making
  debugging and testing straightforward.
- Aligns with Elasticsearch/Lucene prior art.

### Negative

- Bigrams produce more tokens than morphological analysis (~2× for
  typical text), increasing index size.
- No word-boundary awareness: "東京都" is ["東京", "京都"] not ["東京",
  "都"]. This may reduce precision for some queries.
- Mixed CJK-Latin text at word boundaries requires careful handling in
  the tokenizer.

### Risks

- **Precision@5 regression on CJK queries**: Mitigated by the fact that
  the Phase 1 corpus is code-oriented (mostly Latin with occasional CJK
  comments). If CJK precision is insufficient, morphological analysis can
  be layered on top without changing the index format.
- **Index bloat**: Monitored via diagnostics. If bigram overhead exceeds
  30% of total index size, revisit in Phase 2.

## Alternatives Considered

1. **`ikawaha/kagome`** — Japanese-only. Does not help with Chinese or
   Korean. Adding a separate library per language creates maintenance
   burden without clear benefit for a code knowledge base.

2. **`yanyiwu/gojieba`** — Requires CGo. The execution plan accepts CGo
   "if no mature pure-Go alternative exists," but bigram segmentation is
   a mature pure-Go alternative that covers all CJK scripts.

3. **Per-codepoint tokens (PRD v1 default)** — Each CJK character becomes
   a separate token. This is the fallback from the execution plan. Bigrams
   are strictly better: they capture two-character compounds (the most
   common word length in Chinese and Japanese) while still matching
   single characters via overlap.

## References

- PRD v3.1: `PRDs/PRD_PCKE_v3_1.md` §4.15 (Tokenizer), §13.2 (Open questions)
- Execution Plan: `PRDs/PRD_PCKE_v3_1_EXECUTION_PLAN.md` §2-D, §5.1
- Elasticsearch CJK Analyzer: bigram-based, same approach
- Lucene CJKBigramFilter: reference implementation of this strategy

# Architecture Decision Records

Each ADR captures a single decision: the context, the chosen approach, the
consequences, and the alternatives considered. ADRs are append-only — to
revise a decision, write a new ADR that either **supersedes** (wholesale
replacement) or **amends** (targeted corrections) the original. See
[`0000-adr-template.md`](0000-adr-template.md) for the template and the
amend-vs-supersede convention.

## Index

| ADR | Title | Status |
|---|---|---|
| [0001](0001-cjk-tokenization-strategy.md) | CJK Tokenization Strategy | Accepted |
| [0002](0002-ast-reference-repos.md) | AST Reference Repositories | Accepted |
| [0003](0003-java-language-support.md) | Java Language Support | Accepted |
| [0004](0004-docs-site-strategy.md) | Documentation Site Strategy | Accepted |
| [0005](0005-compression-go-no-go.md) | Compression Go/No-Go | Accepted |
| [0006](0006-mcp-streaming-design.md) | MCP Streaming Design | Accepted |
| [0007](0007-prompt-templates-schema.md) | Prompt Templates Schema | Accepted |
| [0008](0008-context-graph-pivot.md) | Context Graph Pivot | Accepted (amended by 0009) |
| [0009](0009-durable-memory-corrections.md) | Durable Memory Corrections | Accepted |
| [0010](0010-retrieval-package-path.md) | Retrieval Package Path (`internal/retrieval`) | Accepted |

## Note on PRD references

ADRs may reference internal PRDs (Product Requirements Documents) by version
number — e.g. "PRD v3.1", "PRD v5.2". PRDs are maintained as internal
documents and are not published in this repository. The ADR itself is the
authoritative public record of the decision; the PRD context is summarized
inline when relevant.

# PRD: Project Context & Knowledge Engine (PCKE) v1.0
## 1. Product Vision
To transform the "ephemeral" nature of AI chat sessions into a Long-Term Engineering Memory. PCKE extracts architectural intent, business logic, and historical lessons from codebases and Git history, enabling AI agents (GitHub Copilot, Claude Code) to perform with the insight of a Senior Engineer who has years of project context.

## 2. Core Pillars of Intelligence
1. Code as Truth: The current state of the repository is the primary reality.

2. History as Narrative: Git history is mined to understand the evolution of the code and the intent behind changes.

3. Temporal Awareness: The system tracks how logic has changed over time, maintaining "Historical Lessons" to prevent recurring mistakes.

## 3. Functional Requirements

### 3.1. Analysis Engine (The Ingestor)

The engine supports two modes of operation:

- **Standard Scan:** Quick analysis of the current file tree and recent commits to update the active context.
- **Intensive Analysis (`--intensive`):** A deep-dive crawl of the entire Git tree.
  - **Logic Extraction:** Maps AST (Abstract Syntax Tree) components to natural language business rules.
  - **Intent Synthesis:** Compares Diffs to extract *why* a refactor happened.
  - **Stability Scoring:** Identifies "Core" vs. "Volatile" modules.

### 3.2. Hybrid Storage & Schema

A custom-built, versioned Vector Database (PostgreSQL + `pgvector`) stores knowledge as a **Temporal Knowledge Graph**.

- **Knowledge Nodes:** Stores the "What" (Code snippets, summaries, embeddings).
- **Evolution Logs:** Stores the "Why" (Changelogs, reasons for logic pivots).
- **Constraints:** Stores the "Must/Must-Nots" (Architecture guardrails).
- **Versioning:** When code changes, the old Node is marked as `Legacy` and linked to the new `Active` Node, preserving the "Trauma/Lesson."    

### 3.3. Interoperability & Portability

To ensure compatibility with GitHub Copilot and Claude:

- **Markdown Sync:** Bi-directional sync between the DB and a local `.context/` directory.
- **Auto-Injection:** Automatically updates `.github/copilot-instructions.md` and `.claudecode.md` with relevant context snippets based on the active branch.

## 4. The PCKE CLI Specification

| **Command**           | **Description**                                              |
| --------------------- | ------------------------------------------------------------ |
| `pcke init`           | Initializes the PCKE environment and database connection.    |
| `pcke scan [-i]`      | Performs standard or intensive (--intensive) project analysis. |
| `pcke sync`           | Updates local Markdown instruction files from the Vector DB. |
| `pcke recall <query>` | Manually queries the project brain (e.g., "Why do we use Redis here?"). |
| `pcke rule add`       | Manually injects a strict engineering constraint into the AI's memory. |
| `pcke status`         | Shows knowledge health and any branch-merge conflicts.       |



## 5. Conflict & Branch Strategy

- **Namespace Isolation:** Knowledge is tracked per branch.
- **Knowledge Reconciliation:** Upon merging a branch into `Main`, the system triggers an LLM-assisted merge to synthesize "Lessons Learned" from both branches into a single timeline.
- **Conflict CLI:** Provides a manual interface for developers to resolve contradictory "Experience" entries.

## 6. Validation & Quality Assurance (KPIs)

To ensure the system actually makes the AI "smarter," we measure:

- **Contextual Citation Rate:** The frequency with which Copilot/Claude references a `Historical Lesson` in its suggestions ($Goal: >30\%$).
- **The Zero-Context Test:** Comparing AI performance on a fresh repo vs. a PCKE-synced repo.
- **Constraint Adherence:** Tracking how often the AI respects "Rules" stored in the `Constraints_Registry`.
- **Hallucination Check:** Human-in-the-loop verification via `pcke verify` and `pcke debunk`.

------

## 7. Future Roadmap

- **V1.1:** Implementation of an **MCP (Model Context Protocol)** server for native Claude Code support.
- **V1.2:** "Onboarding Mode"—Auto-generating project walkthroughs for new human developers based on the AI's accumulated "Experience."
- **V2.0:** Multi-repo cross-pollination (Learning patterns from one project and applying them as suggestions to another).

------

### End of PRD

> **Status:** Finalized for Development
> **Prepared by:** Gemini & User
> **Date:** April 2026
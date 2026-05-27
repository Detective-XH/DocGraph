<div align="center">

# DocGraph

### Documentation knowledge graph MCP server for LLM agents

**Governance metadata · Research provenance · Drift audit · Cross-reference tracking · Topic similarity · Multi-format**

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go 1.25+](https://img.shields.io/badge/Go-1.25%2B-00ADD8.svg)](https://go.dev)
[![Single binary](https://img.shields.io/badge/binary-%7E16_MB_%C2%B7_zero_runtime_deps-brightgreen.svg)](#install)
[![Formats](https://img.shields.io/badge/formats-.md_%C2%B7_.docx_%C2%B7_.html_%C2%B7_.pdf-orange.svg)](#what-gets-indexed)

[![macOS](https://img.shields.io/badge/macOS-supported-blue.svg)](#install)
[![Linux](https://img.shields.io/badge/Linux-supported-blue.svg)](#install)
[![Windows](https://img.shields.io/badge/Windows-supported-blue.svg)](#install)

[![Claude Code](https://img.shields.io/badge/Claude_Code-supported-blueviolet.svg)](#claude-code)
[![Codex](https://img.shields.io/badge/Codex-supported-blueviolet.svg)](#codex-openai)
[![Hermes Agent](https://img.shields.io/badge/Hermes_Agent-supported-blueviolet.svg)](#hermes-agent)
[![OpenCode](https://img.shields.io/badge/OpenCode-supported-blueviolet.svg)](#opencode)

</div>

Documentation knowledge graph MCP server. Indexes `.md`, `.docx`, `.html`,
`.pdf`, and optional code-documentation surfaces into SQLite, extracts
cross-references and topic similarity, and exposes the graph through 12 MCP tools over stdio.

Three domain packs are enabled by default — **governance** (`status`, `owner`,
`review_due`, `sensitivity`…), **research_provenance** (`claim_id`, `evidence`,
`confidence`…), and **entity** (`entity_type`, `canonical_name`…) — plus three
opt-in packs for policy/SOP drift, assessment contradiction audit, and code
documentation surfaces. No code knowledge required for the governance and
research packs: they work on any document collection (.md, .docx, .html, .pdf).

DocGraph's value scales with **how connected your documents are**:

- **High value**: docs that cross-reference each other (`[links](other.md)`, `[[wikilinks]]`, frontmatter `related_to`), or carry governance/research frontmatter (`status`, `owner`, `claim_id`, `confidence`). Examples: ADR networks, Obsidian vaults, policy/SOP libraries, research assessment corpora.
- **Medium value**: docs with shared tags/frontmatter but few explicit links. Similarity engine still finds topic clusters.
- **Low value**: flat, isolated documents with no links or metadata. `grep` is simpler and faster.

The LLM-facing installation and tool-selection guide lives in [`AGENTS.md`](AGENTS.md).

Single binary. Zero runtime dependencies. Indexes hundreds of docs in seconds.

## At a Glance

| Metric | Value |
|--------|-------|
| Language | Go 1.25+ |
| Binary size | ~16 MB |
| Codebase | ~52,210 lines of Go (+ ~45,980 lines of tests) |
| Index speed | 70–700 files per project in 2–6s (full rebuild; `--force`) |
| Typical graph | ~950 nodes and ~670 edges per 100 indexed files |

## Install

```bash
go install github.com/Detective-XH/docgraph@latest
```

Or build from source with version embedded:

```bash
git clone https://github.com/Detective-XH/DocGraph.git
cd DocGraph
go build -ldflags "-X main.version=$(git describe --tags --always)" -o docgraph .
```

Requires Go 1.25 or later.

> `go install` does not support `-ldflags` injection, so `docgraph version` will output `dev` for binaries installed that way. Use the source build above to get a versioned binary.

## CLI

```
docgraph init [--dry-run] [--interactive] [--install-clients auto|all|LIST] [--workspace] [--scope user] [--with-skills] [--update-skills] [path] # Create local config; optionally install MCP clients and bundled skills
docgraph install [--dry-run] [--interactive] [--clients auto|all|LIST] [--workspace] [--scope user] [--update-skills] [path]      # Configure MCP clients without re-initializing
docgraph pack list [--workspace] <path>                         # List domain packs and enabled state
docgraph pack enable [--workspace] [--no-sync] <pack-id> <path>  # Enable a domain pack; code_doc syncs by default
docgraph pack disable [--workspace] <pack-id> <path>             # Disable a domain pack; code_doc rows are removed
docgraph index [--force] [--threshold N] [--no-gitignore] <path>  # Index a project
docgraph sync [--threshold N] [--no-gitignore] <path>             # Incremental hash-based update
docgraph status <path>                       # Print index stats
docgraph serve [--threshold N] [--no-gitignore] --path <path>     # MCP stdio server (single project)
docgraph serve [--threshold N] [--no-gitignore] --workspace <dir> # MCP stdio server (auto-discover all child dirs)
docgraph version                             # Print build version
```

`LIST` is a comma-separated client list: `claude,codex,hermes,opencode`.
`auto` always writes project-local Claude Code config and also writes Codex,
Hermes, and OpenCode config when their config directories already exist.
`all` creates config files for every supported client.
Use `--dry-run` to print create/update/unchanged actions without writing files.
Use `--interactive` to print the same review and confirm before writes.

## Bundled Skills

When installing for Claude Code, DocGraph automatically installs companion skills
into `.claude/skills/` alongside the MCP config — no extra flag needed:

```bash
docgraph init --install-clients claude /path/to/project  # MCP config + skill
docgraph install --clients claude /path/to/project       # MCP config + skill
```

To install skills on a project that was already initialized without `--install-clients`:

```bash
docgraph init --with-skills /path/to/project
```

Skills are installed with skip-if-exists policy — safe to re-run. To update an
existing skill to the latest bundled version:

```bash
docgraph init --update-skills /path/to/project
docgraph install --clients claude --update-skills /path/to/project
```

The `docgraph-drift-audit` skill audits all indexed `.md` files for DocGraph
compatibility: missing frontmatter, isolated docs (no outgoing links), broken
wikilinks, headings, and similarity islands. Reports PASS/FAIL per category and
offers auto-fix via `docgraph_files` and `docgraph_similar`.

Available skills bundled in the binary:

| Skill | Purpose |
|-------|---------|
| `docgraph-drift-audit` | Audit `.md` files for DocGraph compatibility |
| `policy-drift-audit` | Display and triage policy/process drift findings from `docgraph_context format=drift_audit` |
| `assessment-drift-audit` | Display and triage research assessment drift findings from `docgraph_context format=drift_audit` |
| `code-doc-drift-audit` | Display and triage docs-code drift findings (`code.*`) when the `code_doc` pack is enabled |

## MCP Tools

`docgraph_graph` supports `operation=incoming|outgoing|impact|trace`. Use
`document` for incoming, outgoing, and impact; use `from` and `to` for trace.
`--tool-profile full` and `--tool-profile dual` are deprecated and ignored.

### Tools

| # | Tool | Description |
|---|------|-------------|
| 1 | `docgraph_search` | FTS5 full-text search (CJK + Latin) with section-level results, field-weighted ranking, graph-aware reranking, and governance/research/entity filters |
| 2 | `docgraph_context` | **Primary entry point** -- task context with related docs, structure, cross-refs, and bounded source content. Use `format=context_pack` for reviewable evidence packs; `format=drift_audit` for policy/process, research, and (when `code_doc` is enabled) docs-code drift audit reports |
| 3 | `docgraph_graph` | Graph traversal facade. `operation=incoming` (who references this doc), `operation=outgoing` (what this doc links to), `operation=impact` (blast radius, configurable depth), `operation=trace` (shortest path between two docs). Use `document=` for incoming/outgoing/impact; `from=` and `to=` for trace |
| 4 | `docgraph_node` | Single document details with metadata, structure, and edges |
| 5 | `docgraph_explore` | Survey multiple related documents in one call |
| 6 | `docgraph_files` | Indexed file tree |
| 7 | `docgraph_similar` | Find topically similar documents (TF-IDF + shared refs + tags) |
| 8 | `docgraph_status` | Index health, per-project stats, schema version, domain packs, pending reindex/migration state, and compact drift audit summary when policy/research findings exist |
| 9 | `docgraph_tags` | List all tags with doc counts, or filter documents by tag |
| 10 | `docgraph_history` | Git commit history for a document: amendment count, authors, dates |
| 11 | `docgraph_enrichment` | Pull or store inferred summaries and metadata for documents without frontmatter |
| 12 | `docgraph_embeddings` | Neural embedding workflow facade. `action=pending` lists docs needing embeddings; `action=store` saves a vector and recomputes neural similarity; `action=clear` deletes all embeddings for a model |

Start with `docgraph_context` for any research question. It composes search,
structure, and cross-references into a single result. Use the other tools
to drill into specifics.

For agent-facing fit checks and tool-selection rules, see [`AGENTS.md`](AGENTS.md).

## Agent Metadata Enrichment

DocGraph can enrich `.docx`, `.pdf`, `.html`, and other documents that do not
have frontmatter. The workflow is agent-driven: DocGraph returns candidate
content, and the caller decides which model or provider to use.

1. `docgraph_enrichment(operation=pending, limit, content_mode)` returns
   frontmatter-less documents without a current inferred summary, including
   `doc_id`, `content_hash`, and bounded content.
2. The agent infers a concise summary and optional metadata JSON object.
3. `docgraph_enrichment(operation=store, doc_id, content_hash, summary,
   metadata, confidence, model_id, provider, agent_id)` stores the result.
   `model_id` is required, and `content_hash` must match the pending response.

Inferred metadata never overrides authored frontmatter or extracted document
metadata. Stored summaries appear in `docgraph_node`, `docgraph_context`, and
context packs. `docgraph_status` reports enrichment coverage and stale results.
Normal retrieval uses one current enrichment per document, while DocGraph keeps
an internal run ledger with model, provider, agent, and content-hash provenance.
Agent-inferred summaries and metadata are advisory context, not source of truth.

**Privacy**: `docgraph_enrichment operation=pending` returns document content that your
agent may send to an external provider. Get user consent before proceeding.

## Semantic Similarity

DocGraph computes topic similarity between documents using three signals:

| Signal | Method | Weight |
|--------|--------|--------|
| Text overlap | TF-IDF cosine similarity | 50% |
| Shared references | Jaccard similarity of outgoing link targets | 30% |
| Tag overlap | Jaccard similarity of frontmatter tags | 20% |

Documents scoring above the threshold (default 0.25) are connected with
`similar_to` edges. This finds conceptually related documents even when
they don't explicitly link to each other — the key advantage over
grep-based search.

Similarity is computed automatically during indexing. Query with
`docgraph_similar`. Tune sensitivity with `--threshold N` on `index`, `sync`,
or `serve`; lower values create more `similar_to` edges.

### Neural Embeddings (agent-driven)

DocGraph never calls an LLM itself. Instead, your agent computes embeddings
with any provider and pushes the vectors back — a pull-then-push agentic
workflow that enables semantic search far beyond TF-IDF vocabulary matching.
1. `docgraph_embeddings(action=pending, model_id, limit, content_mode)` — returns docs without up-to-date embeddings, including content and `content_hash`. `content_mode=full` (default) reads the full section from disk; `content_mode=excerpt` uses the stored body excerpt. Different `model_id` values are partitioned separately and never compared with each other.
2. Your agent computes vectors with its own provider (OpenAI, Ollama, Nomic, etc.)
3. `docgraph_embeddings(action=store, doc_id, model_id, vector, content_hash)` per doc — stores the vector and recomputes neural `similar_to` edges. Pass `content_hash` exactly as returned by step 1.
4. `docgraph_similar` deduplicates TF-IDF and neural results for the same pair, preferring neural when both exist.

In workspace mode, both embedding workflows automatically locate the correct per-project store by `doc_id`.

**Privacy**: pending embedding actions return document content that your agent will send to an external provider. Get user consent before proceeding.

Use `docgraph_embeddings_clear(model_id)` or `docgraph_embeddings(action=clear, model_id)` to delete all vectors for a model and reclaim space. `docgraph_status` shows a Neural Embeddings table listing stored models, total vectors, and stale count.

## Node and Edge Kinds

**Nodes:** `document`, `heading`, `definition`, `tag`; optional `code_file`
nodes when the `code_doc` domain pack is enabled.

**Edges:**

| Kind | Meaning |
|------|---------|
| `contains` | Document contains heading/definition |
| `references` | `[text](path.md)` Markdown link |
| `wikilinks_to` | `[[target]]` wikilink |
| `related_to` | Frontmatter wikilink (e.g., `related_to: "[[target]]"`) |
| `similar_to` | Topic similarity (TF-IDF + shared refs + tags; or neural if embeddings stored) |
| `tagged` | Frontmatter tag association |
| `embeds` | `![[embed]]` transclusion |
| `links_external` | URL to external resource |

## What Gets Indexed

**Markdown (`.md`)** — up to 1 MB per file:
- YAML frontmatter parsed into metadata; headings and `**Term:** definition` lines produce structural nodes
- `[[wikilinks]]`, `[links](path.md)`, `![[embeds]]`, external URLs, and frontmatter tags produce typed edges

**Word documents (`.docx`)** — up to 10 MB per file:
- Heading paragraphs (Heading 1–6 styles) become `heading` nodes with containment edges
- Hyperlinks extracted as `docx_hyperlink` edges; Dublin Core metadata (`core.xml`) stored as key/value tuples
- Zip-slip protection, per-entry size limits, 50 MB total uncompressed budget

**HTML (`.html`, `.htm`)** — up to 5 MB per file:
- `<h1>`–`<h6>` tags (including `id` attributes) become `heading` nodes
- `<meta name=…>` and `<meta property=…>` stored as metadata tuples; `<a href=…>` become typed link edges
- `<script>` and `<style>` content excluded from body text and section chunks

**PDF (`.pdf`)** — up to 50 MB / 500 pages per file:
- Each page becomes a `heading` node and a section chunk
- Info-dict fields (Title, Author, Subject, Keywords, CreationDate) indexed as metadata tuples
- Image-only PDFs detected via average chars/page and flagged with `warning: image-only-pdf`

**Code documentation surfaces (opt-in)** — up to 1 MB per file:
- Enable the `code_doc` domain pack to index file headers, exported doc comments, test names, and example names:
  - Single project: `docgraph pack enable code_doc /path/to/project`
  - Workspace: `docgraph pack enable --workspace code_doc /path/to/workspace`
  - Inspect state: `docgraph pack list /path/to/project`
- Supported languages include Go, Python, Ruby, JavaScript, TypeScript, Svelte, Vue, Rust, C, C++, Java, Swift, C#, PHP, Kotlin, Dart, Lua, Luau, Pascal, SQL, and Liquid
- Adds one `code_file` node per source file; incremental `pack enable` sync completes in 1–4s for 40–80 code files, up to ~12s for 300+ code files
- After enabling, `kind=code_file` is immediately available in `docgraph_search` and `format=drift_audit` surfaces `code.*` findings
- `--force` re-index resets domain pack state — re-run `docgraph pack enable code_doc <path>` after a force rebuild
- This is shallow documentation indexing only; CodeGraph remains the intended tool for call graphs, type resolution, routes, and code impact

**Common rules:**
- Respects `.gitignore` and `.docgraphignore`
- Skipped directories: `node_modules`, `.git`, `target`, `dist`, `build`, `vendor`, and similar

## Domain Packs

Domain packs extend the metadata schema for specific use cases. Three packs are
enabled by default; three are opt-in.

| Pack | Default | Domain | Purpose |
|------|---------|--------|---------|
| `governance` | On | governance | Lifecycle status, ownership, sensitivity, review scheduling, audience access controls, and document supersession |
| `research_provenance` | On | research | Claims, evidence, source type, confidence, analyst workflow, event/assessment dates, and temporal validity |
| `entity` | On | entity | Entity classification, canonical naming, and alias declaration; activates the entity source graph |
| `code_doc` | **Off** | code | File headers, doc comments, test names, and example names from Go, Python, JS/TS, Rust, and 20+ more languages |
| `policy_process` | **Off** | policy_process | Policy/SOP drift detection — conflicting, stale, duplicated, superseded, and non-canonical documents |
| `assessment_drift` | **Off** | research | Assessment drift detection — stale assessments, unverified evidence, and competing research interpretations |

### Frontmatter Fields by Pack

Each pack reads specific keys from your Markdown frontmatter.

**governance** — lifecycle and access control:
```yaml
status: active            # Governance lifecycle status
owner: alice              # Accountable person or role
sensitivity: internal     # Sets retrieval boundaries
allowed_audience: [engineering, legal]
review_due: 2026-12-31    # Triggers policy.stale_review when overdue
effective_date: 2026-01-01
canonical_source: true    # Marks as the authoritative copy among duplicates
approver: bob
department: Engineering
supersedes: old-policy.md
superseded_by: new-policy.md
```

**research_provenance** — evidence and provenance tracking:
```yaml
claim_id: CLM-001
source_type: primary      # primary | secondary | internal
confidence: high
analyst_status: verified
assessment_date: 2026-05-01
event_date: 2026-04-15
last_verified: 2026-05-20
valid_until: 2026-11-01   # Triggers research.stale_assessment when expired
evidence: [doc/evidence-a.md, doc/evidence-b.md]
client: ACME
deliverable_id: RPT-42
```

**entity** — canonical entity declaration:
```yaml
entity_type: organization   # person | organization | location | …
canonical_name: Acme Corp
aliases: [ACME, Acme Corporation]
```

**policy_process** (opt-in) — SOP and policy enrichment:
```yaml
sop_category: onboarding
policy_domain: HR           # HR | Security | Finance | …
process_owner: People Ops
version: "2.1"
conflict_resolution: supersedes-v2.0
```

**assessment_drift** (opt-in) — competing research tracking:
```yaml
contradicts: assessment-2026-03.md
supersedes_claim: CLM-099
```

### Drift Audit Findings

`docgraph_context format=drift_audit` surfaces advisory findings from enabled packs.
No code knowledge is needed — governance and research packs work on any document collection (.md, .docx, .html, .pdf).

| Finding | Pack(s) required | What it detects |
|---------|-----------------|-----------------|
| `policy.stale_review` | governance | `review_due` has passed |
| `policy.superseded_referenced` | governance | Superseded doc is still cited by others |
| `policy.duplicate` | governance | Near-duplicate content detected via similarity |
| `policy.non_canonical` | governance | No `canonical_source` marker among near-duplicates |
| `policy.conflicting` | governance | Similar docs with conflicting status or effective dates |
| `research.stale_assessment` | research_provenance | `valid_until` has expired |
| `research.unverified_evidence` | research_provenance | Evidence reference cannot be resolved |
| `research.competing_interpretations` | research_provenance + assessment_drift | Conflicting claims on the same topic |
| `research.superseded_claim` | research_provenance + assessment_drift | Outdated claim still cited |
| `research.impacted_deliverable` | research_provenance | Deliverable depends on a stale claim |
| `code.missing_symbol` | code_doc | Doc references a code symbol that no longer exists |
| `code.undocumented_export` | code_doc | Exported symbol has no doc comment |
| `code.unanchored_feature` | code_doc + governance | Feature mentioned in docs has no matching code |

### Managing Packs

```bash
docgraph pack list /path/to/project                                # Show all packs and enabled state
docgraph pack enable policy_process /path/to/project               # Enable an opt-in pack
docgraph pack enable assessment_drift /path/to/project
docgraph pack enable code_doc /path/to/project                     # Also triggers incremental sync
docgraph pack disable code_doc /path/to/project                    # Removes code_file rows
docgraph pack enable --workspace policy_process /path/to/workspace # Apply to all child projects
```

`--force` re-index resets all pack state — re-run `docgraph pack enable <pack-id> <path>` after a force rebuild.

## Workspace Mode

Point DocGraph at a parent directory and it auto-discovers all immediate
child directories as separate projects:

```bash
docgraph serve --workspace /path/to/workspace
```

- Each project gets its own `.docgraph/docgraph.db` (add `.docgraph/` to `.gitignore`)
- Cross-project search fans out to all databases
- File watcher (fsnotify, 2s debounce) monitors served projects for live re-indexing
- No configuration file needed

## File Exclusion

DocGraph respects `.gitignore` by default. For additional control, create a
`.docgraphignore` file (same syntax as `.gitignore`):

```
# Project-level .docgraphignore — exclude files within a project
drafts/
archive/
*.draft.md
!archive/INDEX.md    # re-include a specific file
```

Workspace-level `.docgraphignore` (at the workspace root) excludes entire
projects by directory name:

```
# Workspace-level .docgraphignore — exclude projects
OSINT-Platform-backup-20260518
csint-private
```

### Indexing all files

To index files that are gitignored (e.g., `.claude/skills/`, `memory/`
directories), use the `--no-gitignore` flag:

```bash
docgraph index --no-gitignore <path>
docgraph sync --no-gitignore <path>
docgraph serve --no-gitignore --workspace <dir>
```

This ignores `.gitignore` rules but still respects `.docgraphignore`.

## MCP Client Integration

DocGraph works with any MCP-compatible client via stdio transport.

For automatic setup:

```bash
docgraph init --install-clients auto /path/to/project
docgraph install --clients all --workspace /path/to/workspace
```

The installer writes:

| Client | Config target |
|--------|---------------|
| Claude Code | `/path/to/project/.mcp.json` |
| Codex | `$CODEX_HOME/config.toml` or `~/.codex/config.toml` |
| Hermes Agent | `~/.hermes/config.yaml` |
| OpenCode | project `opencode.json` / `.opencode.json`, otherwise `$XDG_CONFIG_HOME/opencode/opencode.json` |

### Claude Code

**Project-level** (this project only) — add to `.mcp.json` in your project root, or run:

```bash
docgraph init --install-clients claude /path/to/project
```

Manual `.mcp.json`:

```json
{
  "mcpServers": {
    "docgraph": {
      "command": "docgraph",
      "args": ["serve", "--path", "."]
    }
  }
}
```

**User-level (global)** — available across all projects. Writes to `~/.claude.json` via the `claude` CLI:

```bash
docgraph install --clients claude --scope user --workspace /path/to/workspace
```

Or manually with the `claude` CLI:

```bash
claude mcp add --scope user docgraph -- docgraph serve --workspace /path/to/workspace
```

Verify the connection:

```bash
claude mcp list
```

> **Important:** Claude Code stores user-scope MCP config in `~/.claude.json`, **not** `~/.claude/mcp.json`. Manually editing `~/.claude/mcp.json` has no effect — use `claude mcp add --scope user` or the project-level `.mcp.json` approach instead.

> **PATH note:** `docgraph` must be on your PATH. For `go install` builds, ensure `$GOPATH/bin` is in your PATH (run `go env GOPATH` to find the location). If not, use the absolute path to the binary.

### Codex (OpenAI)

Add to your MCP configuration:

```toml
[mcp_servers.docgraph]
command = "docgraph"
args = ["serve", "--workspace", "/path/to/workspace"]
```

### Hermes Agent

Add to `~/.hermes/config.yaml`:

```yaml
mcp_servers:
  docgraph:
    command: docgraph
    args:
      - serve
      - --workspace
      - /path/to/workspace
```

### OpenCode

Add to your opencode MCP configuration:

```json
{
  "mcpServers": {
    "docgraph": {
      "command": "docgraph",
      "args": ["serve", "--workspace", "/path/to/workspace"]
    }
  }
}
```

### Any MCP client

DocGraph uses stdio transport. Launch with:

```bash
docgraph serve --workspace /path/to/workspace
# or single project:
docgraph serve --path /path/to/project
```

The server reads JSON-RPC from stdin and writes to stdout.

## Architecture

```
scan .md / .docx / .html / .pdf  (docformat registry: extensions + per-format size limits)
  -> dispatch:
       .md          → goldmark + inlined YAML frontmatter parser
       .docx        → stdlib archive/zip + encoding/xml
       .html / .htm → golang.org/x/net HTML tokenizer
       .pdf         → ledongthuc/pdf (text layer; writes to temp file)
       code docs    → optional code_doc pack for comments/tests/examples
  -> extract nodes, edges, links, metadata tuples, and section chunks
  -> store in SQLite (modernc.org/sqlite, pure Go)
  -> resolve cross-document references
  -> compute topic similarity (TF-IDF + graph Jaccard)
  -> serve over MCP stdio (mark3labs/mcp-go)
```

FTS5 uses the trigram tokenizer for mixed CJK and Latin full-text search.

## Dependencies

| Dependency | Role |
|------------|------|
| [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) | Pure-Go SQLite driver (no CGo) with FTS5 |
| [goldmark](https://github.com/yuin/goldmark) | Markdown parser |
| [yaml.v3](https://github.com/go-yaml/yaml) | YAML frontmatter parsing |
| [mcp-go](https://github.com/mark3labs/mcp-go) | MCP protocol (stdio transport) |
| [fsnotify](https://github.com/fsnotify/fsnotify) | Cross-platform file watcher |
| [golang.org/x/net](https://pkg.go.dev/golang.org/x/net/html) | HTML tokenizer for `.html`/`.htm` extraction |
| [ledongthuc/pdf](https://github.com/ledongthuc/pdf) | PDF text-layer extraction |
| stdlib | `.gitignore` + `.docgraphignore` matching, `archive/zip` + `encoding/xml` for `.docx` |

## Supply Chain

CI verifies module checksums with `go mod verify`, runs `govulncheck`, and
generates a CycloneDX JSON SBOM artifact named `docgraph-sbom` with
`cyclonedx-gomod`. The SBOM is generated from `go.mod` during GitHub Actions
runs; generated SBOM files are not checked into the repository.

## CodeGraph Interoperability

DocGraph and CodeGraph are complementary. DocGraph owns documentation context,
governance/research metadata, citation paths, document references, context
packs, drift audits, and shallow code-documentation surfaces. CodeGraph owns
source-code intelligence such as symbols, callers/callees, call traces, route
handlers, and code impact.

CodeGraph interoperability currently ships as an advisory handoff layer in the
MCP server instructions. DocGraph does not call CodeGraph, read `.codegraph/`,
or import CodeGraph symbol anchors. The reserved `codegraph_anchor` metadata
field stays empty until CodeGraph exposes a stable export/API contract.

For docs-code work, enable DocGraph's `code_doc` pack — it is the interface
layer between DocGraph and CodeGraph. DocGraph indexes documentation surfaces
(file headers, exported doc comments, test names, example names); CodeGraph
indexes code structure (symbols, callers/callees, call graphs, type resolution).
Together they give a complete picture: `format=drift_audit` with `code_doc`
enabled can surface `code.missing_symbol`, `code.undocumented_export`, and
`code.unanchored_feature` findings, then hand symbol-level questions to
`codegraph_*` tools when the agent environment exposes them.

## Inspired By

DocGraph is inspired by [CodeGraph](https://github.com/colbymchenry/codegraph),
which builds a knowledge graph from source code symbols using tree-sitter
and SQLite. DocGraph adopts the same core design:

- **Schema**: `nodes` + `edges` + `files` + `unresolved_refs` + FTS5 + `section_chunks` + `section_chunks_fts` + `document_metadata` + `governance_metadata` + `research_metadata` + `domain_packs` + `domain_pack_fields` + `entities` + `entity_mentions` — the graph model extended with section snapshots, section-level search, normalized governance metadata, research provenance, domain schema pack registration, and entity/source graph primitives. Forward-only versioned migrations replace `CREATE TABLE IF NOT EXISTS`.
- **Pipeline**: scan → parse → store → resolve — the same four-phase indexing
  pipeline, with goldmark replacing tree-sitter for AST extraction.
- **Two-phase resolution**: raw links are extracted during parsing, then
  resolved in a separate pass after all files are indexed — identical to
  CodeGraph's `UnresolvedReference` → `ReferenceResolver` pattern.
- **MCP tool surface**: 12 tools with CodeGraph-compatible naming for context, search, node, explore, similar, files, status, tags, and history. Graph traversal (`docgraph_graph`) and neural embeddings (`docgraph_embeddings`) are facade tools that group fine-grained operations behind a single dispatch parameter, keeping agent-facing instructions compact.

Where they diverge: DocGraph is written in Go (single binary, no Node.js
runtime), uses the trigram tokenizer for CJK support, and adds workspace
mode for multi-project fan-out queries — features that reflect documentation
use cases rather than code navigation. DocGraph also adds hybrid topic
similarity (TF-IDF + graph Jaccard + tags) to discover conceptual
relationships that neither explicit links nor code structure can capture.

## Release v0.2.1 — SHA-256 Checksums

Signed by `EDB0808F3F248B66F53837B4888293C4BA30EEF6` (Xavier).

**docgraph-darwin-arm64.tar.gz**
```
f919b519bb06b69af60c7d0950b523e9ae071798e519bf467f6867c859d47872
```

**docgraph-darwin-amd64.tar.gz**
```
e90e69210954062b4a8a75c5adeed146d05247797d2b987c5d07b44c4e0b6cc1
```

**docgraph-linux-amd64.tar.gz**
```
1e6af1f0c0b52091559fcd63bb1cc1952f45ecad7575d19820002a0131d22000
```

**docgraph-linux-arm64.tar.gz**
```
2b59657c4f5c12a34d3ba8a55d3116e67e3fd3b0c1c64c90e827cd20a4f0da20
```

**docgraph-windows-amd64.zip**
```
647ae998751e55e28081f321f0a1f63cd6e1b868e86aa45d1d510d280ee3dda5
```

Verify:
```bash
gpg --verify SHA256SUMS.asc SHA256SUMS
shasum -a 256 -c SHA256SUMS
```

## License

MIT

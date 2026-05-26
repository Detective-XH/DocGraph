<div align="center">

# DocGraph

### Documentation knowledge graph MCP server for LLM agents

**Cross-reference tracking · Impact analysis · Topic similarity · Multi-format**

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
cross-references and topic similarity, and exposes the graph through 16 MCP
tools over stdio.

DocGraph's value scales with **how connected your documents are**:

- **High value**: docs that cross-reference each other (`[links](other.md)`, `[[wikilinks]]`, frontmatter `related_to`). Examples: ADR networks, Obsidian vaults, governance docs, [LLM wiki](https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f) collections with interlinks.
- **Medium value**: docs with shared tags/frontmatter but few explicit links. Similarity engine still finds topic clusters.
- **Low value**: flat, isolated documents with no links or metadata. `grep` is simpler and faster.

The LLM-facing installation and tool-selection guide lives in [`AGENTS.md`](AGENTS.md).

Single binary. Zero runtime dependencies. Indexes hundreds of docs in seconds.

## At a Glance

| Metric | Value |
|--------|-------|
| Language | Go 1.25+ |
| Binary size | ~16 MB |
| Codebase | ~51,140 lines of Go (+ ~45,090 lines of tests) |
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

| # | Tool | Description |
|---|------|-------------|
| 1 | `docgraph_search` | FTS5 full-text search (CJK + Latin) |
| 2 | `docgraph_context` | **Primary entry point** -- task context with related docs, structure, cross-refs, and bounded source content. Use `format=context_pack` for reviewable evidence packs; `format=drift_audit` for policy/process, research, and (when `code_doc` is enabled) docs-code drift audit reports |
| 3 | `docgraph_references` | Incoming links (who references this doc) |
| 4 | `docgraph_links` | Outgoing links (what this doc links to) |
| 5 | `docgraph_impact` | Blast radius analysis (BFS over incoming refs, configurable depth) |
| 6 | `docgraph_node` | Single document details with metadata, structure, and edges |
| 7 | `docgraph_explore` | Survey multiple related documents in one call |
| 8 | `docgraph_trace` | Shortest reference path between two docs (BFS, max 10 hops) |
| 9 | `docgraph_files` | Indexed file tree |
| 10 | `docgraph_similar` | Find topically similar documents (TF-IDF + shared refs + tags) |
| 11 | `docgraph_status` | Index health, per-project stats, schema version, domain packs, pending reindex/migration state, and compact drift audit summary when policy/research findings exist |
| 12 | `docgraph_tags` | List all tags with doc counts, or filter documents by tag |
| 13 | `docgraph_history` | Git commit history for a document: amendment count, authors, dates |
| 14 | `docgraph_embeddings_pending` | List documents that need neural embeddings (no embedding yet, or content changed since last embed) |
| 15 | `docgraph_embeddings_store` | Store a neural embedding vector for a document and recompute neural similarity edges |
| 16 | `docgraph_embeddings_clear` | Delete all stored embeddings for a model and their associated neural similarity edges |

Start with `docgraph_context` for any research question. It composes search,
structure, and cross-references into a single result. Use the other tools
to drill into specifics.

For agent-facing fit checks and tool-selection rules, see [`AGENTS.md`](AGENTS.md).

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
with any provider and pushes the vectors back:

1. `docgraph_embeddings_pending(model_id="text-embedding-3-small")` — returns docs without up-to-date embeddings
2. Your agent computes vectors with its own provider
3. `docgraph_embeddings_store(doc_id, model_id, vector, content_hash)` per doc — stores the vector and recomputes neural `similar_to` edges
4. `docgraph_similar` automatically surfaces neural results alongside TF-IDF results

**Privacy**: `docgraph_embeddings_pending` returns document content that your agent will send to an external provider. User consent is required before proceeding.

Use `docgraph_embeddings_clear(model_id)` to delete all vectors for a model and reclaim space. `docgraph_status` shows a Neural Embeddings table listing stored models, total vectors, and stale count.

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

For docs-code work, enable DocGraph's `code_doc` pack for comments, tests,
examples, and file headers, then hand symbol existence, callers/callees, call
traces, routes, and code impact questions to `codegraph_*` tools when the agent
environment exposes them.

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
- **MCP tool surface**: the 16 tools keep CodeGraph-compatible naming for
  context, search, references/links, impact, trace, node, explore, similar,
  files, status, tags, and history, with a distinct opt-in neural embeddings
  protocol. Agent-facing instructions group these tools behind a compact
  decision tree so future workflows do not require a growing top-level catalog.

Where they diverge: DocGraph is written in Go (single binary, no Node.js
runtime), uses the trigram tokenizer for CJK support, and adds workspace
mode for multi-project fan-out queries — features that reflect documentation
use cases rather than code navigation. DocGraph also adds hybrid topic
similarity (TF-IDF + graph Jaccard + tags) to discover conceptual
relationships that neither explicit links nor code structure can capture.

## Release v0.1.9 — SHA-256 Checksums

Signed by `EDB0808F3F248B66F53837B4888293C4BA30EEF6` (Xavier).

**docgraph-darwin-arm64.tar.gz**
```
c3ae298b3320f93d533f3e9a7617e1ab2fc2370ab5141b5492c3f7099976dd90
```

**docgraph-darwin-amd64.tar.gz**
```
154b24324b3b784df918a0b88d6af985ce8f50f64312df389ca80eb9c50fdeca
```

**docgraph-linux-amd64.tar.gz**
```
afe9adf657b6538cf9e77ef198f6da01139112fc472fbf968986fd98b7e3e425
```

**docgraph-linux-arm64.tar.gz**
```
ee1bc9c63ef1444520c60956a653f73cdd77014bdf41f44fb9020a8c953a5321
```

**docgraph-windows-amd64.zip**
```
605ce33dbb9401986c58a279a0990d110da93d01565c3c99d6d64ce60f0e56e4
```

Verify:
```bash
gpg --verify SHA256SUMS.asc SHA256SUMS
shasum -a 256 -c SHA256SUMS
```

## License

MIT

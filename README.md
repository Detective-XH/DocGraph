# DocGraph

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Documentation knowledge graph MCP server for LLM agents. Indexes Markdown
files into SQLite, extracts cross-references and topic similarity, and
exposes the graph through 11 MCP tools over stdio.

DocGraph's value scales with **how connected your documents are**:

- **High value**: docs that cross-reference each other (`[links](other.md)`, `[[wikilinks]]`, frontmatter `related_to`). Examples: ADR networks, Obsidian vaults, governance docs, [LLM wiki](https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f) collections with interlinks.
- **Medium value**: docs with shared tags/frontmatter but few explicit links. Similarity engine still finds topic clusters.
- **Low value**: flat, isolated documents with no links or metadata. `grep` is simpler and faster.

**LLM agents**: before installing, read [`AGENTS.md`](AGENTS.md) to diagnose whether DocGraph fits your project. It includes a 6-question scoring guide and a tool decision tree.

Single binary. Zero runtime dependencies. Indexes hundreds of docs in seconds.

## At a Glance

| Metric | Value |
|--------|-------|
| Language | Go 1.25+ |
| Binary size | ~13.5 MB |
| Codebase | ~3,900 lines of Go (+ ~3,100 lines of tests) |
| Index speed | ~880 .md files across 19 projects in seconds |
| Typical graph | ~12,800 nodes, ~13,500 edges |

## Install

```bash
go install github.com/Detective-XH/docgraph@latest
```

Or build from source:

```bash
git clone https://github.com/Detective-XH/docgraph.git
cd docgraph
go build -o docgraph .
```

Requires Go 1.25 or later.

## CLI

```
docgraph index [--force] [--no-gitignore] <path>     # Index a project
docgraph sync [--no-gitignore] <path>                # Incremental hash-based update
docgraph status <path>                       # Print index stats
docgraph serve [--no-gitignore] --path <path>        # MCP stdio server (single project)
docgraph serve [--no-gitignore] --workspace <dir>    # MCP stdio server (auto-discover all child dirs)
```

## MCP Tools

| # | Tool | Description |
|---|------|-------------|
| 1 | `docgraph_search` | FTS5 full-text search (CJK + Latin) |
| 2 | `docgraph_context` | **Primary entry point** -- task context with related docs, structure, and cross-refs |
| 3 | `docgraph_references` | Incoming links (who references this doc) |
| 4 | `docgraph_links` | Outgoing links (what this doc links to) |
| 5 | `docgraph_impact` | Blast radius analysis (BFS over incoming refs, configurable depth) |
| 6 | `docgraph_node` | Single document details with metadata, structure, and edges |
| 7 | `docgraph_explore` | Survey multiple related documents in one call |
| 8 | `docgraph_trace` | Shortest reference path between two docs (BFS, max 10 hops) |
| 9 | `docgraph_files` | Indexed file tree |
| 10 | `docgraph_similar` | Find topically similar documents (TF-IDF + shared refs + tags) |
| 11 | `docgraph_status` | Index health and per-project stats |

Start with `docgraph_context` for any research question. It composes search,
structure, and cross-references into a single result. Use the other tools
to drill into specifics.

**LLM agents**: if you have access to file reading tools, read [`AGENTS.md`](AGENTS.md) first — it contains a tool decision tree and usage tips that will save you trial-and-error calls.

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
`docgraph_similar`.

## Node and Edge Kinds

**Nodes:** `document`, `heading`, `definition`, `tag`

**Edges:**

| Kind | Meaning |
|------|---------|
| `contains` | Document contains heading/definition |
| `references` | `[text](path.md)` Markdown link |
| `wikilinks_to` | `[[target]]` wikilink |
| `related_to` | Frontmatter wikilink (e.g., `related_to: "[[target]]"`) |
| `similar_to` | Topic similarity (TF-IDF + shared refs + tags) |
| `tagged` | Frontmatter tag association |
| `embeds` | `![[embed]]` transclusion |
| `links_external` | URL to external resource |

## What Gets Indexed

- Markdown files (`.md`) only
- Respects `.gitignore` rules
- YAML frontmatter parsed into metadata JSON
- Headings extracted as structural hierarchy
- `[[wikilinks]]`, `[links](path.md)`, `![[embeds]]`, external URLs, and frontmatter tags all produce typed edges
- Max file size: 1 MB
- Skipped directories: `node_modules`, `.git`, `target`, `dist`, `build`, `vendor`, and similar

## Workspace Mode

Point DocGraph at a parent directory and it auto-discovers all immediate
child directories as separate projects:

```bash
docgraph serve --workspace /path/to/workspace
```

- Each project gets its own `.docgraph/docgraph.db` (add `.docgraph/` to `.gitignore`)
- Cross-project search fans out to all databases
- File watcher (fsnotify, 2s debounce) monitors all projects for live re-indexing
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

### Claude Code

Add to `.mcp.json` in your project root:

```json
{
  "docgraph": {
    "command": "docgraph",
    "args": ["serve", "--workspace", "/path/to/workspace"]
  }
}
```

### Codex (OpenAI)

Add to your MCP configuration:

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
scan .md files
  -> parse with goldmark (+ inlined YAML frontmatter parser)
  -> extract nodes, edges, and links
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
| stdlib | `.gitignore` + `.docgraphignore` rule matching (no external dep) |

## Inspired By

DocGraph is inspired by [CodeGraph](https://github.com/colbymchenry/codegraph),
which builds a knowledge graph from source code symbols using tree-sitter
and SQLite. DocGraph adopts the same core design:

- **Schema**: `nodes` + `edges` + `files` + `unresolved_refs` + FTS5 — the
  same four-table graph model, adapted from code symbols to document structure.
- **Pipeline**: scan → parse → store → resolve — the same four-phase indexing
  pipeline, with goldmark replacing tree-sitter for AST extraction.
- **Two-phase resolution**: raw links are extracted during parsing, then
  resolved in a separate pass after all files are indexed — identical to
  CodeGraph's `UnresolvedReference` → `ReferenceResolver` pattern.
- **MCP tool surface**: the 11 tools (`_context`, `_search`, `_callers`/
  `_references`, `_callees`/`_links`, `_impact`, `_trace`, `_node`,
  `_explore`, `_similar`, `_files`, `_status`) mirror CodeGraph's names
  and semantics so that agents already familiar with one can use the other
  without learning a new interface.

Where they diverge: DocGraph is written in Go (single binary, no Node.js
runtime), uses the trigram tokenizer for CJK support, and adds workspace
mode for multi-project fan-out queries — features that reflect documentation
use cases rather than code navigation. DocGraph also adds hybrid topic
similarity (TF-IDF + graph Jaccard + tags) to discover conceptual
relationships that neither explicit links nor code structure can capture.

## Release v0.1.0 — SHA-256 Checksums

Signed by `EDB0808F3F248B66F53837B4888293C4BA30EEF6` (Xavier).

**docgraph-darwin-arm64.tar.gz**
```
cd1d66d82d4a79a972a1b0be13fc46d12cfa9d3c2b5840cf403ae8b7f42c5650
```

**docgraph-darwin-amd64.tar.gz**
```
1620121a8e59604c7315eb7ee35e9b30678172338514deabf0d0f3aed082fb37
```

**docgraph-linux-amd64.tar.gz**
```
a158dd71dda4f86fa14960f492ca618c05c8f8943d24eb05c13dd3ecaa51968b
```

**docgraph-linux-arm64.tar.gz**
```
c5c19b7b6aab143dd9fc799ea0c2827a036492ed405305f8660cc6749fb689aa
```

**docgraph-windows-amd64.zip**
```
974d7bab7de3006585f2c63baa3b754c1d2f1d988ceaf965560d3bf6d470163c
```

Verify:
```bash
gpg --verify SHA256SUMS.asc SHA256SUMS
shasum -a 256 -c SHA256SUMS
```

## License

MIT

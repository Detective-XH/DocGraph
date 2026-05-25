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
| Codebase | ~4,740 lines of Go (+ ~3,860 lines of tests) |
| Index speed | ~880 .md files across 19 projects in seconds |
| Typical graph | ~12,800 nodes, ~13,500 edges |

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
docgraph init [--install-clients auto|all|LIST] [--workspace] [--scope user] [--with-skills] [path] # Create local config; optionally install MCP clients and bundled skills
docgraph install [--clients auto|all|LIST] [--workspace] [--scope user] [path]      # Configure MCP clients without re-initializing
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

## Bundled Skills

`docgraph init --with-skills <path>` installs the `docgraph-drift-audit` skill
into `.claude/skills/docgraph-drift-audit/SKILL.md` in your project:

```bash
docgraph init --with-skills /path/to/project
```

The skill audits all indexed `.md` files for DocGraph compatibility: missing
frontmatter, isolated docs (no outgoing links), broken wikilinks, headings, and
similarity islands. It runs against the live index via `docgraph_status` and
direct SQLite queries, then reports PASS/FAIL per category and offers auto-fix.

**Install behaviour:** skips any skill directory that already exists — safe to
re-run after a fresh `docgraph init`. To update an existing skill to the latest
version, delete the skill directory first.

Available skills bundled in the binary:

| Skill | Purpose |
|-------|---------|
| `docgraph-drift-audit` | Audit `.md` files for DocGraph compatibility |

## MCP Tools

| # | Tool | Description |
|---|------|-------------|
| 1 | `docgraph_search` | FTS5 full-text search (CJK + Latin) |
| 2 | `docgraph_context` | **Primary entry point** -- task context with related docs, structure, cross-refs, and bounded source content |
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
`docgraph_similar`. Tune sensitivity with `--threshold N` on `index`, `sync`,
or `serve`; lower values create more `similar_to` edges.

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
- Definition lines such as `**Term:** definition` produce `definition` nodes
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

## Supply Chain

CI verifies module checksums with `go mod verify`, runs `govulncheck`, and
generates a CycloneDX JSON SBOM artifact named `docgraph-sbom` with
`cyclonedx-gomod`. The SBOM is generated from `go.mod` during GitHub Actions
runs; generated SBOM files are not checked into the repository.

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

## Release v0.1.7 — SHA-256 Checksums

Signed by `EDB0808F3F248B66F53837B4888293C4BA30EEF6` (Xavier).

**docgraph-darwin-arm64.tar.gz**
```
02b046560a62aae4bd507abb5f187859bb048c13ca027536ff5a14988fcf0f19
```

**docgraph-darwin-amd64.tar.gz**
```
b0544d499bf4a8d8c8ea128bc84eaf78f44ca3964c65641c790acdede7425fdf
```

**docgraph-linux-amd64.tar.gz**
```
5c9669733af2cde05d7d6f1e98d210f0da55f80fc786ace9dd16435a34a42d22
```

**docgraph-linux-arm64.tar.gz**
```
d7988d31015c294844fe97009b681f72fa88cdfe7e427309927678f060bee236
```

**docgraph-windows-amd64.zip**
```
b57a4e8b6f202a3052fe164e9ebd7562bb9f9b2b512bdb7775a71eaa01352e39
```

Verify:
```bash
gpg --verify SHA256SUMS.asc SHA256SUMS
shasum -a 256 -c SHA256SUMS
```

## License

MIT

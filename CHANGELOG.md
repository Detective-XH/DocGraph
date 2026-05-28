# Changelog

## v0.2.2 — 2026-05-28

LLM callout opt-in, scope confirmation, and cost transparency.

### Added

- `--enable-embeddings` and `--enable-enrichment` CLI flags; both tools are off by default and must be explicitly opted in via `mcp.json` args.
- `docgraph_enrichment action=pending` returns a pre-written ASCII scope tree with per-folder file counts, token estimates, per-model cost table, and sensitive path flags before any writes occur.
- `docgraph_embeddings action=pending` now generates a one-time confirmation token; `action=store` requires the token before processing.
- `engine=` parameter for `docgraph_similar`: `auto` (default), `tfidf`, or `neural`. Neural mode returns a clear error when embeddings are not enabled.
- `docgraph_status` now includes a **LLM Callout Tools** section showing enabled/disabled state and pending counts for both tools.

### Security

- Vector payload cap (2 MB) enforced before deserialisation in `docgraph_embeddings action=store`.
- Enrichment metadata payload cap (1 MB) enforced before deserialisation in `docgraph_enrichment action=process`.
- Confirmation tokens use `crypto/rand` (16 bytes, 32-char hex); embeddings and enrichment tokens are kept in separate stores to prevent cross-tool reuse; 30-minute TTL with lazy sweep.
- When 100% of pending documents are in sensitive-flagged paths, no token is generated and `action=store`/`action=process` are rejected.

### Breaking Changes

- `docgraph_embeddings` and `docgraph_enrichment` are no longer registered by default. Add `--enable-embeddings` / `--enable-enrichment` to `mcp.json` args to restore access.
- `docgraph_enrichment` API changed: `operation=pending/store` → `action=pending/process`; `action=process` requires a `confirmation_token` from the preceding `action=pending` call.

---

## v0.2.1 — 2026-05-27

Agent metadata enrichment and dependency maintenance release.

- Added an agent-driven workflow for enriching frontmatter-less documents with inferred summaries and metadata
- Enrichment writes now record model provenance while keeping only the current inferred summary active for retrieval
- Inferred summaries now appear in document, context, and context-pack outputs
- `docgraph_status` now reports metadata enrichment coverage and stale enrichment state
- Added the `docgraph_embeddings` facade for compact-profile neural embedding workflows with `action=pending|store|clear`
- Compact MCP profile now exposes 12 tools by replacing the three fine-grained embedding tools with the embedding facade
- Full MCP profile keeps `docgraph_embeddings_pending`, `docgraph_embeddings_store`, and `docgraph_embeddings_clear` for compatibility; dual profile exposes both surfaces
- SQLite upgraded to 3.53.1 via `modernc.org/sqlite` bump
- `mcp-go` updated to support MCP spec 2025-11-25
- CI actions (`checkout`, `upload-artifact`) bumped to current major versions

---

## v0.2.0 — Governance knowledge graph

First stable release with structured metadata, multi-format indexing, and drift auditing.

### Multi-format indexing

DocGraph now indexes `.md`, `.docx`, `.html`, and `.pdf` files. Scanned PDFs are flagged. Large archives and bombs are rejected at extraction time.

### Domain packs

Six built-in domain packs define the metadata schemas DocGraph understands:

| Pack | On by default | Purpose |
|------|---------------|---------|
| `governance` | Yes | Lifecycle status, ownership, review schedules, sensitivity, audience |
| `research_provenance` | Yes | Claims, evidence, confidence, analyst workflow, temporal validity |
| `entity` | Yes | Entity type, canonical name, aliases |
| `code_doc` | No | Shallow code documentation surface (comments, exports, test names) |
| `policy_process` | No | SOP category, policy domain, conflict resolution |
| `assessment_drift` | No | Cross-document contradiction and supersession tracking |

Enable opt-in packs with `docgraph pack enable <pack> <path>`.

### Drift audit

`docgraph_context(format=drift_audit)` surfaces advisory findings across enabled packs:

- **Policy findings** — stale reviews, superseded references, duplicates, conflicting documents
- **Research findings** — expired validity, unverified evidence, competing interpretations, impacted deliverables
- **Code findings** — missing symbols, undocumented exports, unanchored features (requires `code_doc` pack)

Governance and research drift audits work on any document collection — no code required.

### Governance-aware search

Search results respect governance metadata: `status`, `sensitivity`, `canonical_source`, `allowed_audience`, `as_of_date`, `confidence`, and `analyst_status` can all be used as filters. Approved and canonical documents rank ahead of drafts, superseded, or restricted ones.

### Context packs

`docgraph_context(format=context_pack)` produces a reviewable Markdown evidence package with source hashes, section content, citation paths, and metadata — useful for auditing or feeding a longer LLM reasoning chain.

### Entity graph

Documents with `entity_type`, `canonical_name`, and `aliases` frontmatter keys activate the entity graph. Entity mentions from wikilinks are tracked separately. Filter searches by `entity_type` or `entity_id`.

### Section-level search

FTS now covers individual section chunks alongside full documents, improving precision for long documents with multiple topics.

### Interactive installer

`docgraph init` and `docgraph install` support `--dry-run` and `--interactive` flags for reviewing configuration before applying it.

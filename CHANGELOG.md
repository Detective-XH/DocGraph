# Changelog

## v0.2.1 — 2026-05-27

Dependency maintenance release. No behavior changes.

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

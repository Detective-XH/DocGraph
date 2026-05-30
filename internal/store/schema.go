package store

import "database/sql"

// SchemaSQL is the complete current schema. Every statement uses
// CREATE ... IF NOT EXISTS so bootstrapSchema is idempotent — safe to call
// on every Open() whether the DB is brand new or already initialised.
//
// Node kinds: document, heading, definition, tag (plus code_file when the
// code_doc pack is enabled).
// Edge kinds: contains, references, wikilinks_to, related_to, similar_to,
// tagged, embeds, links_external. related_to is a recognized reference edge —
// graph traversal, search ranking, and the drift audits all query it alongside
// references/wikilinks_to/embeds — but the current parser/resolver never EMITS
// one: a frontmatter related_to: field resolves into wikilinks_to. So normal
// indexing produces zero related_to edges; they exist only if inserted directly.
const SchemaSQL = `
CREATE TABLE IF NOT EXISTS nodes (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    qualified_name TEXT NOT NULL,
    file_path TEXT NOT NULL,
    start_line INTEGER NOT NULL,
    end_line INTEGER NOT NULL,
    level INTEGER DEFAULT 0,
    metadata TEXT,
    body_excerpt TEXT,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS edges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL,
    target TEXT NOT NULL,
    kind TEXT NOT NULL,
    metadata TEXT,
    line INTEGER,
    FOREIGN KEY (source) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (target) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS files (
    path TEXT PRIMARY KEY,
    content_hash TEXT NOT NULL,
    size INTEGER NOT NULL,
    modified_at INTEGER NOT NULL,
    indexed_at INTEGER NOT NULL,
    node_count INTEGER DEFAULT 0,
    has_frontmatter INTEGER DEFAULT 0,
    errors TEXT
);

CREATE TABLE IF NOT EXISTS unresolved_refs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_node_id TEXT NOT NULL,
    reference_text TEXT NOT NULL,
    reference_kind TEXT NOT NULL,
    line INTEGER NOT NULL,
    col INTEGER NOT NULL,
    file_path TEXT NOT NULL,
    FOREIGN KEY (from_node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS project_metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS file_history (
    path TEXT PRIMARY KEY,
    commit_count INTEGER NOT NULL DEFAULT 0,
    first_commit_at INTEGER NOT NULL DEFAULT 0,
    last_commit_at INTEGER NOT NULL DEFAULT 0,
    author_count INTEGER NOT NULL DEFAULT 0,
    last_author TEXT NOT NULL DEFAULT '',
    last_subject TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS embeddings (
    doc_id       TEXT NOT NULL,
    model_id     TEXT NOT NULL,
    dim          INTEGER NOT NULL,
    vector       BLOB NOT NULL,
    content_hash TEXT NOT NULL,
    updated_at   INTEGER NOT NULL,
    PRIMARY KEY (doc_id, model_id),
    FOREIGN KEY (doc_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS section_chunks (
    node_id       TEXT    NOT NULL,
    file_path     TEXT    NOT NULL,
    start_line    INTEGER,
    end_line      INTEGER,
    content_hash  TEXT    NOT NULL,
    section_hash  TEXT    NOT NULL,
    heading_path  TEXT    NOT NULL,
    text          TEXT    NOT NULL,
    PRIMARY KEY (node_id),
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS document_metadata (
    node_id    TEXT    NOT NULL,
    key        TEXT    NOT NULL,
    value      TEXT    NOT NULL,
    value_type TEXT    NOT NULL DEFAULT 'string',
    source     TEXT    NOT NULL DEFAULT 'frontmatter',
    confidence REAL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (node_id, key, source),
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS governance_metadata (
    node_id          TEXT    PRIMARY KEY,
    status           TEXT,
    owner            TEXT,
    approver         TEXT,
    department       TEXT,
    effective_date   TEXT,
    review_due       TEXT,
    supersedes       TEXT,
    superseded_by    TEXT,
    sensitivity      TEXT,
    allowed_audience TEXT,
    canonical_source TEXT,
    updated_at       INTEGER NOT NULL,
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS research_metadata (
    node_id          TEXT    PRIMARY KEY,
    claim_id         TEXT,
    evidence         TEXT,
    source_type      TEXT,
    confidence       TEXT,
    event_date       TEXT,
    assessment_date  TEXT,
    last_verified    TEXT,
    valid_until      TEXT,
    analyst_status   TEXT,
    client           TEXT,
    deliverable_id   TEXT,
    updated_at       INTEGER NOT NULL,
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS domain_packs (
    id                 TEXT    PRIMARY KEY,
    name               TEXT    NOT NULL,
    version            TEXT    NOT NULL,
    domain             TEXT    NOT NULL,
    enabled            INTEGER NOT NULL DEFAULT 1,
    builtin            INTEGER NOT NULL DEFAULT 0,
    min_schema_version INTEGER NOT NULL DEFAULT 0,
    status             TEXT    NOT NULL DEFAULT 'stable',
    description        TEXT    NOT NULL DEFAULT '',
    loaded_at          INTEGER NOT NULL,
    metadata           TEXT    NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS domain_pack_fields (
    pack_id     TEXT    NOT NULL,
    field_key   TEXT    NOT NULL,
    column_name TEXT    NOT NULL,
    value_type  TEXT    NOT NULL,
    required    INTEGER NOT NULL DEFAULT 0,
    aliases     TEXT    NOT NULL DEFAULT '[]',
    description TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (pack_id, field_key),
    FOREIGN KEY (pack_id) REFERENCES domain_packs(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS entities (
    id                        TEXT    PRIMARY KEY,
    entity_type               TEXT    NOT NULL DEFAULT '',
    canonical_name            TEXT    NOT NULL,
    canonical_name_normalized TEXT    NOT NULL,
    aliases                   TEXT    NOT NULL DEFAULT '[]',
    properties                TEXT    NOT NULL DEFAULT '{}',
    pack_id                   TEXT,
    updated_at                INTEGER NOT NULL,
    UNIQUE(entity_type, canonical_name_normalized)
);

CREATE TABLE IF NOT EXISTS entity_mentions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_id    TEXT    NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    node_id      TEXT    NOT NULL REFERENCES nodes(id)   ON DELETE CASCADE,
    file_path    TEXT    NOT NULL,
    line         INTEGER NOT NULL DEFAULT 0,
    context      TEXT    NOT NULL DEFAULT '',
    mention_type TEXT    NOT NULL DEFAULT 'reference',
    updated_at   INTEGER NOT NULL
);

CREATE VIRTUAL TABLE IF NOT EXISTS nodes_fts USING fts5(
    name, qualified_name, body_excerpt, metadata,
    content='nodes', content_rowid='rowid',
    tokenize='trigram'
);

CREATE VIRTUAL TABLE IF NOT EXISTS section_chunks_fts USING fts5(
    heading_path,
    text,
    content='section_chunks',
    content_rowid='rowid',
    tokenize='trigram'
);

CREATE INDEX IF NOT EXISTS idx_nodes_kind ON nodes(kind);
CREATE INDEX IF NOT EXISTS idx_nodes_name ON nodes(name);
CREATE INDEX IF NOT EXISTS idx_nodes_file_path ON nodes(file_path);
CREATE INDEX IF NOT EXISTS idx_nodes_qualified_name ON nodes(qualified_name);
CREATE INDEX IF NOT EXISTS idx_edges_source_kind ON edges(source, kind);
CREATE INDEX IF NOT EXISTS idx_edges_target_kind ON edges(target, kind);
CREATE INDEX IF NOT EXISTS idx_unresolved_refs_from_node ON unresolved_refs(from_node_id);
CREATE INDEX IF NOT EXISTS idx_embeddings_model ON embeddings(model_id);
CREATE INDEX IF NOT EXISTS idx_section_chunks_file         ON section_chunks(file_path);
CREATE INDEX IF NOT EXISTS idx_section_chunks_section_hash ON section_chunks(section_hash);
CREATE INDEX IF NOT EXISTS idx_docmeta_key_value ON document_metadata(key, value);
CREATE INDEX IF NOT EXISTS idx_docmeta_node      ON document_metadata(node_id);
CREATE INDEX IF NOT EXISTS idx_docmeta_source    ON document_metadata(source);
CREATE INDEX IF NOT EXISTS idx_govmeta_status         ON governance_metadata(status);
CREATE INDEX IF NOT EXISTS idx_govmeta_sensitivity    ON governance_metadata(sensitivity);
CREATE INDEX IF NOT EXISTS idx_govmeta_effective_date ON governance_metadata(effective_date);
CREATE INDEX IF NOT EXISTS idx_govmeta_review_due     ON governance_metadata(review_due);
CREATE INDEX IF NOT EXISTS idx_research_claim_id      ON research_metadata(claim_id);
CREATE INDEX IF NOT EXISTS idx_research_source_type   ON research_metadata(source_type);
CREATE INDEX IF NOT EXISTS idx_research_confidence    ON research_metadata(confidence);
CREATE INDEX IF NOT EXISTS idx_research_last_verified ON research_metadata(last_verified);
CREATE INDEX IF NOT EXISTS idx_domain_packs_domain    ON domain_packs(domain);
CREATE INDEX IF NOT EXISTS idx_domain_packs_enabled   ON domain_packs(enabled);
CREATE INDEX IF NOT EXISTS idx_domain_pack_fields_key ON domain_pack_fields(field_key);
CREATE INDEX IF NOT EXISTS idx_entities_type          ON entities(entity_type);
CREATE INDEX IF NOT EXISTS idx_entities_pack          ON entities(pack_id);
CREATE INDEX IF NOT EXISTS idx_mentions_entity        ON entity_mentions(entity_id);
CREATE INDEX IF NOT EXISTS idx_mentions_node          ON entity_mentions(node_id);
CREATE INDEX IF NOT EXISTS idx_mentions_file          ON entity_mentions(file_path);
CREATE UNIQUE INDEX IF NOT EXISTS idx_mentions_unique ON entity_mentions(entity_id, node_id, line);

CREATE TABLE IF NOT EXISTS ai_summaries (
    node_id      TEXT    PRIMARY KEY,
    summary      TEXT    NOT NULL,
    model_hint   TEXT    NOT NULL DEFAULT '',
    content_hash TEXT    NOT NULL,
    updated_at   INTEGER NOT NULL,
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_ai_summaries_updated ON ai_summaries(updated_at);

CREATE TABLE IF NOT EXISTS agent_enrichment_runs (
    run_id        TEXT    PRIMARY KEY,
    node_id       TEXT    NOT NULL,
    provider      TEXT    NOT NULL DEFAULT '',
    model_id      TEXT    NOT NULL,
    agent_id      TEXT    NOT NULL DEFAULT '',
    content_hash  TEXT    NOT NULL,
    summary_hash  TEXT    NOT NULL DEFAULT '',
    metadata_hash TEXT    NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS agent_enrichment_current (
    node_id    TEXT    PRIMARY KEY,
    run_id     TEXT    NOT NULL,
    updated_at INTEGER NOT NULL,
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (run_id) REFERENCES agent_enrichment_runs(run_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS agent_metadata_provenance (
    node_id    TEXT    NOT NULL,
    key        TEXT    NOT NULL,
    run_id     TEXT    NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (node_id, key),
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (run_id) REFERENCES agent_enrichment_runs(run_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_agent_enrichment_runs_node ON agent_enrichment_runs(node_id);
CREATE INDEX IF NOT EXISTS idx_agent_enrichment_runs_model ON agent_enrichment_runs(model_id);
CREATE INDEX IF NOT EXISTS idx_agent_metadata_provenance_run ON agent_metadata_provenance(run_id);

CREATE TRIGGER IF NOT EXISTS nodes_fts_insert AFTER INSERT ON nodes BEGIN
    INSERT INTO nodes_fts(rowid, name, qualified_name, body_excerpt, metadata)
    VALUES (NEW.rowid, NEW.name, NEW.qualified_name, NEW.body_excerpt, NEW.metadata);
END;

CREATE TRIGGER IF NOT EXISTS nodes_fts_update AFTER UPDATE ON nodes BEGIN
    INSERT INTO nodes_fts(nodes_fts, rowid, name, qualified_name, body_excerpt, metadata)
    VALUES ('delete', OLD.rowid, OLD.name, OLD.qualified_name, OLD.body_excerpt, OLD.metadata);
    INSERT INTO nodes_fts(rowid, name, qualified_name, body_excerpt, metadata)
    VALUES (NEW.rowid, NEW.name, NEW.qualified_name, NEW.body_excerpt, NEW.metadata);
END;

CREATE TRIGGER IF NOT EXISTS nodes_fts_delete AFTER DELETE ON nodes BEGIN
    INSERT INTO nodes_fts(nodes_fts, rowid, name, qualified_name, body_excerpt, metadata)
    VALUES ('delete', OLD.rowid, OLD.name, OLD.qualified_name, OLD.body_excerpt, OLD.metadata);
END;

CREATE TRIGGER IF NOT EXISTS section_chunks_fts_insert AFTER INSERT ON section_chunks BEGIN
    INSERT INTO section_chunks_fts(rowid, heading_path, text)
    VALUES (NEW.rowid, NEW.heading_path, NEW.text);
END;

CREATE TRIGGER IF NOT EXISTS section_chunks_fts_update AFTER UPDATE ON section_chunks BEGIN
    INSERT INTO section_chunks_fts(section_chunks_fts, rowid, heading_path, text)
    VALUES ('delete', OLD.rowid, OLD.heading_path, OLD.text);
    INSERT INTO section_chunks_fts(rowid, heading_path, text)
    VALUES (NEW.rowid, NEW.heading_path, NEW.text);
END;

CREATE TRIGGER IF NOT EXISTS section_chunks_fts_delete AFTER DELETE ON section_chunks BEGIN
    INSERT INTO section_chunks_fts(section_chunks_fts, rowid, heading_path, text)
    VALUES ('delete', OLD.rowid, OLD.heading_path, OLD.text);
END;
`

func bootstrapSchema(db *sql.DB) error {
	_, err := db.Exec(SchemaSQL)
	return err
}

package store

import "database/sql"

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

CREATE INDEX IF NOT EXISTS idx_embeddings_model ON embeddings(model_id);

CREATE VIRTUAL TABLE IF NOT EXISTS nodes_fts USING fts5(
    name, qualified_name, body_excerpt, metadata_text,
    content='nodes', content_rowid='rowid',
    tokenize='trigram'
);

CREATE INDEX IF NOT EXISTS idx_nodes_kind ON nodes(kind);
CREATE INDEX IF NOT EXISTS idx_nodes_name ON nodes(name);
CREATE INDEX IF NOT EXISTS idx_nodes_file_path ON nodes(file_path);
CREATE INDEX IF NOT EXISTS idx_nodes_qualified_name ON nodes(qualified_name);
CREATE INDEX IF NOT EXISTS idx_edges_source_kind ON edges(source, kind);
CREATE INDEX IF NOT EXISTS idx_edges_target_kind ON edges(target, kind);
CREATE INDEX IF NOT EXISTS idx_unresolved_refs_from_node ON unresolved_refs(from_node_id);
`

func initSchema(db *sql.DB) error {
	if _, err := db.Exec(SchemaSQL); err != nil {
		return err
	}

	// Triggers cannot use IF NOT EXISTS, so drop before creating.
	triggers := []string{
		`DROP TRIGGER IF EXISTS nodes_fts_insert`,
		`CREATE TRIGGER nodes_fts_insert AFTER INSERT ON nodes BEGIN
			INSERT INTO nodes_fts(rowid, name, qualified_name, body_excerpt, metadata_text)
			VALUES (NEW.rowid, NEW.name, NEW.qualified_name, NEW.body_excerpt, NEW.metadata);
		END`,

		`DROP TRIGGER IF EXISTS nodes_fts_update`,
		`CREATE TRIGGER nodes_fts_update AFTER UPDATE ON nodes BEGIN
			INSERT INTO nodes_fts(nodes_fts, rowid, name, qualified_name, body_excerpt, metadata_text)
			VALUES ('delete', OLD.rowid, OLD.name, OLD.qualified_name, OLD.body_excerpt, OLD.metadata);
			INSERT INTO nodes_fts(rowid, name, qualified_name, body_excerpt, metadata_text)
			VALUES (NEW.rowid, NEW.name, NEW.qualified_name, NEW.body_excerpt, NEW.metadata);
		END`,

		`DROP TRIGGER IF EXISTS nodes_fts_delete`,
		`CREATE TRIGGER nodes_fts_delete AFTER DELETE ON nodes BEGIN
			INSERT INTO nodes_fts(nodes_fts, rowid, name, qualified_name, body_excerpt, metadata_text)
			VALUES ('delete', OLD.rowid, OLD.name, OLD.qualified_name, OLD.body_excerpt, OLD.metadata);
		END`,
	}

	for _, t := range triggers {
		if _, err := db.Exec(t); err != nil {
			return err
		}
	}

	return nil
}

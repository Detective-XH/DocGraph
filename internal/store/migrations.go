package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Migration is a forward-only schema migration step.
type Migration struct {
	Version  int
	Name     string
	Checksum string // SHA-256 hex of SQL field, populated by init()
	SQL      string
}

// Exported project_metadata key constants — used by statusHandler and workspace reporter.
const (
	MetaKeyMigrationLastFailure = "migration_last_failure"
	MetaKeyReindexRequired      = "reindex_required"
	MetaKeyReindexReason        = "reindex_reason"
	MetaKeyReindexScope         = "reindex_scope"
)

// Sentinel errors.
var (
	ErrFutureSchema     = errors.New("docgraph: database was created by a newer version of DocGraph; upgrade your binary")
	ErrChecksumMismatch = errors.New("docgraph: migration checksum mismatch; database may have been tampered with")
)

// migration001SQL is the baseline schema: nodes/edges/files/unresolved_refs/project_metadata,
// FTS5 virtual table, all indexes (except embeddings), and FTS5 triggers.
// NOTE: Triggers here use plain CREATE TRIGGER (no DROP/IF NOT EXISTS) because
// migrations run exactly once per DB — idempotency is not needed.
const migration001SQL = `
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

CREATE TRIGGER nodes_fts_insert AFTER INSERT ON nodes BEGIN
    INSERT INTO nodes_fts(rowid, name, qualified_name, body_excerpt, metadata_text)
    VALUES (NEW.rowid, NEW.name, NEW.qualified_name, NEW.body_excerpt, NEW.metadata);
END;

CREATE TRIGGER nodes_fts_update AFTER UPDATE ON nodes BEGIN
    INSERT INTO nodes_fts(nodes_fts, rowid, name, qualified_name, body_excerpt, metadata_text)
    VALUES ('delete', OLD.rowid, OLD.name, OLD.qualified_name, OLD.body_excerpt, OLD.metadata);
    INSERT INTO nodes_fts(rowid, name, qualified_name, body_excerpt, metadata_text)
    VALUES (NEW.rowid, NEW.name, NEW.qualified_name, NEW.body_excerpt, NEW.metadata);
END;

CREATE TRIGGER nodes_fts_delete AFTER DELETE ON nodes BEGIN
    INSERT INTO nodes_fts(nodes_fts, rowid, name, qualified_name, body_excerpt, metadata_text)
    VALUES ('delete', OLD.rowid, OLD.name, OLD.qualified_name, OLD.body_excerpt, OLD.metadata);
END;
`

const migration002SQL = `
CREATE TABLE IF NOT EXISTS file_history (
    path TEXT PRIMARY KEY,
    commit_count INTEGER NOT NULL DEFAULT 0,
    first_commit_at INTEGER NOT NULL DEFAULT 0,
    last_commit_at INTEGER NOT NULL DEFAULT 0,
    author_count INTEGER NOT NULL DEFAULT 0,
    last_author TEXT NOT NULL DEFAULT '',
    last_subject TEXT NOT NULL DEFAULT ''
);
`

const migration003SQL = `
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
`

const migration004SQL = `
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
CREATE INDEX IF NOT EXISTS idx_section_chunks_file         ON section_chunks(file_path);
CREATE INDEX IF NOT EXISTS idx_section_chunks_section_hash ON section_chunks(section_hash);
INSERT OR REPLACE INTO project_metadata(key,value,updated_at)
    VALUES('reindex_required','true',unixepoch());
INSERT OR REPLACE INTO project_metadata(key,value,updated_at)
    VALUES('reindex_scope','sections',unixepoch());
INSERT OR REPLACE INTO project_metadata(key,value,updated_at)
    VALUES('reindex_reason','section_chunks table added; run docgraph index --force',unixepoch());
`

const migration005SQL = `
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
CREATE INDEX IF NOT EXISTS idx_docmeta_key_value ON document_metadata(key, value);
CREATE INDEX IF NOT EXISTS idx_docmeta_node      ON document_metadata(node_id);
CREATE INDEX IF NOT EXISTS idx_docmeta_source    ON document_metadata(source);
INSERT OR REPLACE INTO project_metadata(key,value,updated_at)
    VALUES('reindex_required','true',unixepoch());
INSERT OR REPLACE INTO project_metadata(key,value,updated_at)
    VALUES('reindex_scope','metadata',unixepoch());
INSERT OR REPLACE INTO project_metadata(key,value,updated_at)
    VALUES('reindex_reason','document_metadata table added; run docgraph index --force',unixepoch());
`

const migration006SQL = `
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
CREATE INDEX IF NOT EXISTS idx_govmeta_status         ON governance_metadata(status);
CREATE INDEX IF NOT EXISTS idx_govmeta_sensitivity    ON governance_metadata(sensitivity);
CREATE INDEX IF NOT EXISTS idx_govmeta_effective_date ON governance_metadata(effective_date);
CREATE INDEX IF NOT EXISTS idx_govmeta_review_due     ON governance_metadata(review_due);
`

const migration007SQL = `
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
CREATE INDEX IF NOT EXISTS idx_research_claim_id      ON research_metadata(claim_id);
CREATE INDEX IF NOT EXISTS idx_research_source_type   ON research_metadata(source_type);
CREATE INDEX IF NOT EXISTS idx_research_confidence    ON research_metadata(confidence);
CREATE INDEX IF NOT EXISTS idx_research_last_verified ON research_metadata(last_verified);
`

const migration008SQL = `
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

CREATE INDEX IF NOT EXISTS idx_domain_packs_domain  ON domain_packs(domain);
CREATE INDEX IF NOT EXISTS idx_domain_packs_enabled ON domain_packs(enabled);
CREATE INDEX IF NOT EXISTS idx_domain_pack_fields_key ON domain_pack_fields(field_key);
`

const migration009SQL = `
CREATE VIRTUAL TABLE IF NOT EXISTS section_chunks_fts USING fts5(
    heading_path,
    text,
    content='section_chunks',
    content_rowid='rowid',
    tokenize='trigram'
);

INSERT INTO section_chunks_fts(rowid, heading_path, text)
    SELECT rowid, heading_path, text FROM section_chunks;

CREATE TRIGGER section_chunks_fts_insert AFTER INSERT ON section_chunks BEGIN
    INSERT INTO section_chunks_fts(rowid, heading_path, text)
    VALUES (NEW.rowid, NEW.heading_path, NEW.text);
END;

CREATE TRIGGER section_chunks_fts_update AFTER UPDATE ON section_chunks BEGIN
    INSERT INTO section_chunks_fts(section_chunks_fts, rowid, heading_path, text)
    VALUES ('delete', OLD.rowid, OLD.heading_path, OLD.text);
    INSERT INTO section_chunks_fts(rowid, heading_path, text)
    VALUES (NEW.rowid, NEW.heading_path, NEW.text);
END;

CREATE TRIGGER section_chunks_fts_delete AFTER DELETE ON section_chunks BEGIN
    INSERT INTO section_chunks_fts(section_chunks_fts, rowid, heading_path, text)
    VALUES ('delete', OLD.rowid, OLD.heading_path, OLD.text);
END;
`

// migrations is the ordered, append-only list of forward-only migrations.
// F-18 delivers 001–003; F-19 delivers 004; F-21 delivers 005–006; F-22 delivers 007; F-23 delivers 008; F-24 delivers 009.
// Future migrations (010+) are added by their corresponding F-feature.
var migrations = []Migration{
	{Version: 1, Name: "initial_schema", SQL: migration001SQL},
	{Version: 2, Name: "file_history", SQL: migration002SQL},
	{Version: 3, Name: "embeddings", SQL: migration003SQL},
	{Version: 4, Name: "section_chunks", SQL: migration004SQL},
	{Version: 5, Name: "document_metadata", SQL: migration005SQL},
	{Version: 6, Name: "governance_metadata", SQL: migration006SQL},
	{Version: 7, Name: "research_metadata", SQL: migration007SQL},
	{Version: 8, Name: "domain_pack_registry", SQL: migration008SQL},
	{Version: 9, Name: "section_search_fts", SQL: migration009SQL},
}

func init() {
	// Populate checksums from SQL strings at startup so they're guaranteed in sync.
	for i := range migrations {
		sum := sha256.Sum256([]byte(migrations[i].SQL))
		migrations[i].Checksum = hex.EncodeToString(sum[:])
	}
}

// sqlChecksum computes SHA-256 hex of a SQL string.
func sqlChecksum(sql string) string {
	sum := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(sum[:])
}

// schemaBootstrapSQL creates the schema_migrations table itself.
// This is the ONLY DDL allowed outside the versioned migration list.
const schemaBootstrapSQL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    checksum   TEXT NOT NULL,
    applied_at INTEGER NOT NULL
)
`

// RunMigrations applies any pending forward-only migrations to db.
// It is safe to call multiple times (idempotent).
func RunMigrations(db *sql.DB) error {
	return runMigrationsList(db, migrations)
}

// runMigrationsList is the testable core — accepts an explicit migrations list.
func runMigrationsList(db *sql.DB, migs []Migration) error {
	// Step 1: Bootstrap the schema_migrations table.
	if _, err := db.Exec(schemaBootstrapSQL); err != nil {
		return fmt.Errorf("bootstrap schema_migrations: %w", err)
	}

	// Step 3: Check for duplicate version numbers.
	seen := make(map[int]bool, len(migs))
	for _, m := range migs {
		if seen[m.Version] {
			return fmt.Errorf("duplicate migration version %d in migrations list", m.Version)
		}
		seen[m.Version] = true
	}

	// Determine the highest known version in the binary.
	highestKnown := 0
	for _, m := range migs {
		if m.Version > highestKnown {
			highestKnown = m.Version
		}
	}

	// Step 2: Baseline detection — pre-F-18 DB has no schema_migrations rows.
	var appliedCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&appliedCount); err != nil {
		return fmt.Errorf("count schema_migrations: %w", err)
	}

	if appliedCount == 0 {
		// Check if the pre-F-18 tables already exist.
		var tableCount int
		err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('nodes','edges','files')`).Scan(&tableCount)
		if err != nil {
			return fmt.Errorf("check existing tables: %w", err)
		}
		if tableCount == 3 {
			// Pre-F-18 DB: mark only the legacy baseline migrations as applied.
			// Later migrations still need to run so their tables actually exist.
			now := time.Now().Unix()
			for _, m := range migs {
				if m.Version > 3 {
					continue
				}
				if _, err := db.Exec(
					`INSERT OR IGNORE INTO schema_migrations(version, name, checksum, applied_at) VALUES(?,?,?,?)`,
					m.Version, m.Name, m.Checksum, now,
				); err != nil {
					return fmt.Errorf("baseline insert migration %d: %w", m.Version, err)
				}
			}
			if err := setUserVersion(db, 3); err != nil {
				return err
			}
		}
	}

	// Step 4: Check for future schema (DB was created by a newer binary).
	var maxApplied int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&maxApplied); err != nil {
		return fmt.Errorf("query max migration version: %w", err)
	}
	if maxApplied > highestKnown {
		return ErrFutureSchema
	}

	// Step 5: For each known migration, verify or apply.
	for _, m := range migs {
		var storedChecksum string
		err := db.QueryRow(`SELECT checksum FROM schema_migrations WHERE version = ?`, m.Version).Scan(&storedChecksum)
		if err == nil {
			// Already recorded — verify checksum (H-13 silent drift detection).
			if storedChecksum != m.Checksum {
				return fmt.Errorf("%w: version %d (%s)", ErrChecksumMismatch, m.Version, m.Name)
			}
			// Already applied and checksum matches — skip.
			continue
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("query migration %d: %w", m.Version, err)
		}

		// Not yet applied — run in a transaction.
		if applyErr := applyMigration(db, m); applyErr != nil {
			// Write failure marker to project_metadata (best-effort; may fail if migration 001 itself failed).
			marker := fmt.Sprintf("%d:%s:%s", m.Version, m.Name, applyErr.Error())
			_, _ = db.Exec(
				`INSERT INTO project_metadata(key,value,updated_at) VALUES(?,?,?)
				 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
				MetaKeyMigrationLastFailure, marker, time.Now().Unix(),
			)
			return fmt.Errorf("apply migration %d (%s): %w", m.Version, m.Name, applyErr)
		}
	}

	// Step 6: Set PRAGMA user_version to the latest applied version.
	var finalMax int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&finalMax); err != nil {
		return fmt.Errorf("query final max version: %w", err)
	}
	return setUserVersion(db, finalMax)
}

// applyMigration executes a single migration inside a transaction and records it.
func applyMigration(db *sql.DB, m Migration) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	if _, err := tx.Exec(m.SQL); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("exec sql: %w", err)
	}

	if _, err := tx.Exec(
		`INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES(?,?,?,?)`,
		m.Version, m.Name, m.Checksum, time.Now().Unix(),
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record migration: %w", err)
	}

	return tx.Commit()
}

// setUserVersion sets PRAGMA user_version to v.
func setUserVersion(db *sql.DB, v int) error {
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", v)); err != nil {
		return fmt.Errorf("set user_version: %w", err)
	}
	return nil
}

// ReadMigrationStatus returns the last migration failure marker stored in
// project_metadata, if any. Returns ("", false, nil) when no marker exists.
// It tolerates any garbage in the value field and never panics.
func ReadMigrationStatus(db *sql.DB) (string, bool, error) {
	var v string
	err := db.QueryRow(`SELECT value FROM project_metadata WHERE key = ?`, MetaKeyMigrationLastFailure).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

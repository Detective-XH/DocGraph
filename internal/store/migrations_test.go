package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// openRawDB opens an in-memory (or temp-file) SQLite DB for fixture building,
// without running any migrations.
func openRawDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("openRawDB: %v", err)
	}
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			t.Fatalf("pragma %q: %v", p, err)
		}
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// buildV001Fixture creates a DB with the pre-F-18 schema (no schema_migrations table).
// This simulates an existing database that was created before F-18.
func buildV001Fixture(t *testing.T) *sql.DB {
	t.Helper()
	db := openRawDB(t)

	// Apply the old SchemaSQL directly (all tables in one block).
	if _, err := db.Exec(SchemaSQL); err != nil {
		t.Fatalf("buildV001Fixture exec SchemaSQL: %v", err)
	}

	// Recreate the old-style DROP+CREATE triggers (as initSchema used to do).
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
	for _, t2 := range triggers {
		if _, err := db.Exec(t2); err != nil {
			t.Fatalf("buildV001Fixture trigger: %v", err)
		}
	}

	// Verify: schema_migrations must NOT exist.
	var cnt int
	db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&cnt)
	if cnt != 0 {
		t.Fatal("buildV001Fixture: schema_migrations should not exist")
	}
	return db
}

// buildFutureFixture creates a DB with schema_migrations containing version=999.
func buildFutureFixture(t *testing.T) *sql.DB {
	t.Helper()
	db := openRawDB(t)

	// Bootstrap the migrations table.
	if _, err := db.Exec(schemaBootstrapSQL); err != nil {
		t.Fatalf("buildFutureFixture bootstrap: %v", err)
	}

	// Insert a future version row.
	if _, err := db.Exec(
		`INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES(999,'future','abc123',?)`,
		time.Now().Unix(),
	); err != nil {
		t.Fatalf("buildFutureFixture insert: %v", err)
	}
	return db
}

// tableExists checks whether a table (or virtual table) exists in db.
func tableExists(db *sql.DB, name string) bool {
	var cnt int
	db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table','shadow') AND name=?`, name).Scan(&cnt)
	if cnt > 0 {
		return true
	}
	// Also check virtual tables.
	db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name=?`, name).Scan(&cnt)
	return cnt > 0
}

func getUserVersion(db *sql.DB) int {
	var v int
	db.QueryRow(`PRAGMA user_version`).Scan(&v)
	return v
}

func countMigrationRows(db *sql.DB) int {
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n)
	return n
}

// ── Test 1: Fresh DB ──────────────────────────────────────────────────────────

func TestRunMigrations_FreshDB(t *testing.T) {
	db := openRawDB(t)

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations on fresh DB: %v", err)
	}

	// 9 rows in schema_migrations (001–009).
	if n := countMigrationRows(db); n != 9 {
		t.Errorf("expected 9 migration rows, got %d", n)
	}

	// PRAGMA user_version = 9.
	if v := getUserVersion(db); v != 9 {
		t.Errorf("expected user_version=9, got %d", v)
	}

	// All expected tables exist.
	for _, tbl := range []string{"nodes", "edges", "files", "unresolved_refs", "project_metadata", "file_history", "embeddings", "section_chunks", "document_metadata", "governance_metadata", "research_metadata", "domain_packs", "domain_pack_fields", "nodes_fts", "section_chunks_fts", "schema_migrations"} {
		if !tableExists(db, tbl) {
			t.Errorf("table %q not found after fresh migration", tbl)
		}
	}
}

// ── Test 2: Old DB baseline ───────────────────────────────────────────────────

func TestRunMigrations_OldDBBaseline(t *testing.T) {
	db := buildV001Fixture(t)

	// Insert a test node so we can verify FTS still works.
	_, err := db.Exec(
		`INSERT INTO nodes(id,kind,name,qualified_name,file_path,start_line,end_line,updated_at)
		 VALUES('doc1','document','Alpha Doc','doc1','doc1.md',1,10,?)`,
		time.Now().Unix(),
	)
	if err != nil {
		t.Fatalf("insert test node: %v", err)
	}

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations on old DB: %v", err)
	}

	// 9 rows inserted (legacy 001–003 are baselined; 004–009 run normally).
	if n := countMigrationRows(db); n != 9 {
		t.Errorf("expected 9 migration rows, got %d", n)
	}
	if v := getUserVersion(db); v != 9 {
		t.Errorf("expected user_version=9 after applying post-baseline migrations, got %d", v)
	}
	for _, tbl := range []string{"section_chunks", "section_chunks_fts", "document_metadata", "governance_metadata", "research_metadata", "domain_packs", "domain_pack_fields"} {
		if !tableExists(db, tbl) {
			t.Errorf("post-baseline table %q not found after old DB migration", tbl)
		}
	}

	// FTS search still works.
	rows, err := db.Query(`SELECT rowid FROM nodes_fts WHERE nodes_fts MATCH 'Alpha'`)
	if err != nil {
		t.Fatalf("FTS query after baseline: %v", err)
	}
	defer rows.Close()
	var found bool
	for rows.Next() {
		found = true
	}
	if !found {
		t.Error("FTS search returned no results after baseline detection")
	}
}

// ── Test 3: Idempotent reopen ─────────────────────────────────────────────────

func TestRunMigrations_IdempotentReopen(t *testing.T) {
	db := openRawDB(t)

	if err := RunMigrations(db); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}

	// Still exactly 9 rows — no duplicates.
	if n := countMigrationRows(db); n != 9 {
		t.Errorf("expected 9 migration rows after double run, got %d", n)
	}
}

// ── Test 4: Checksum mismatch ─────────────────────────────────────────────────

func TestRunMigrations_ChecksumMismatch(t *testing.T) {
	db := openRawDB(t)

	// Apply migrations successfully first.
	if err := RunMigrations(db); err != nil {
		t.Fatalf("initial RunMigrations: %v", err)
	}

	// Corrupt the checksum of migration version 1.
	if _, err := db.Exec(`UPDATE schema_migrations SET checksum='deadbeef' WHERE version=1`); err != nil {
		t.Fatalf("corrupt checksum: %v", err)
	}

	err := RunMigrations(db)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("expected ErrChecksumMismatch, got: %v", err)
	}

	// DB should still have 9 rows (unchanged).
	if n := countMigrationRows(db); n != 9 {
		t.Errorf("expected 9 migration rows after mismatch, got %d", n)
	}
}

// ── Test 5: Future schema refusal ─────────────────────────────────────────────

func TestRunMigrations_FutureSchema(t *testing.T) {
	db := buildFutureFixture(t)

	err := RunMigrations(db)
	if !errors.Is(err, ErrFutureSchema) {
		t.Errorf("expected ErrFutureSchema, got: %v", err)
	}
}

// ── Test 6: Failed migration rollback ─────────────────────────────────────────

func TestRunMigrations_FailedMigrationRollback(t *testing.T) {
	db := openRawDB(t)

	// Use a custom migration list: migrations 1-4 succeed, migration 5 fails.
	badSQL := `THIS IS NOT VALID SQL $$%##`
	customMigs := append([]Migration{
		{Version: 1, Name: "initial_schema", SQL: migration001SQL},
		{Version: 2, Name: "file_history", SQL: migration002SQL},
		{Version: 3, Name: "embeddings", SQL: migration003SQL},
		{Version: 4, Name: "section_chunks", SQL: migration004SQL},
		{Version: 5, Name: "bad_migration", SQL: badSQL},
	})
	// Compute checksums.
	for i := range customMigs {
		customMigs[i].Checksum = sqlChecksum(customMigs[i].SQL)
	}

	err := runMigrationsList(db, customMigs)
	if err == nil {
		t.Fatal("expected error from bad migration, got nil")
	}

	// Migrations 1-4 should still be intact.
	if n := countMigrationRows(db); n != 4 {
		t.Errorf("expected 4 migration rows after failed migration 5, got %d", n)
	}

	// project_metadata should have migration_last_failure entry.
	marker, found, readErr := ReadMigrationStatus(db)
	if readErr != nil {
		t.Fatalf("ReadMigrationStatus: %v", readErr)
	}
	if !found {
		t.Error("expected migration_last_failure marker in project_metadata, not found")
	}
	expectedPrefix := "5:bad_migration:"
	if len(marker) < len(expectedPrefix) || marker[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("expected marker to start with %q, got %q", expectedPrefix, marker)
	}

	// Tables from migrations 1-4 should still exist.
	for _, tbl := range []string{"nodes", "edges", "files", "file_history", "embeddings", "section_chunks"} {
		if !tableExists(db, tbl) {
			t.Errorf("table %q missing after failed migration 5", tbl)
		}
	}
}

// ── Test 7: Duplicate version ─────────────────────────────────────────────────

func TestRunMigrations_DuplicateVersion(t *testing.T) {
	db := openRawDB(t)

	dupMigs := []Migration{
		{Version: 1, Name: "first", SQL: `CREATE TABLE IF NOT EXISTS t1 (id INTEGER PRIMARY KEY)`},
		{Version: 1, Name: "duplicate", SQL: `CREATE TABLE IF NOT EXISTS t2 (id INTEGER PRIMARY KEY)`},
	}
	for i := range dupMigs {
		dupMigs[i].Checksum = sqlChecksum(dupMigs[i].SQL)
	}

	err := runMigrationsList(db, dupMigs)
	if err == nil {
		t.Fatal("expected error for duplicate version, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected error to mention 'duplicate', got: %v", err)
	}
}

// ── Test 8: Workspace mixed state ─────────────────────────────────────────────

func TestRunMigrations_WorkspaceMixedState(t *testing.T) {
	normalDB := openRawDB(t)
	futureDB := buildFutureFixture(t)

	// Normal DB should succeed.
	if err := RunMigrations(normalDB); err != nil {
		t.Errorf("normal DB migrations failed: %v", err)
	}
	if n := countMigrationRows(normalDB); n != 9 {
		t.Errorf("normal DB: expected 9 migration rows, got %d", n)
	}

	// Future DB should return ErrFutureSchema.
	err := RunMigrations(futureDB)
	if !errors.Is(err, ErrFutureSchema) {
		t.Errorf("future DB: expected ErrFutureSchema, got: %v", err)
	}
}

// ── Test 9: Garbage failure marker ────────────────────────────────────────────

func TestRunMigrations_GarbageFailureMarker(t *testing.T) {
	db := openRawDB(t)

	// Bootstrap project_metadata (migration 001 normally creates it).
	if err := RunMigrations(db); err != nil {
		t.Fatalf("initial RunMigrations: %v", err)
	}

	// Write garbage to the migration_last_failure key.
	garbage := string([]byte{0x00, 0x01, 0xff, 0xfe, 0xfd}) + "not\x00valid\nUTF" + fmt.Sprintf("%d", time.Now().UnixNano())
	if _, err := db.Exec(
		`INSERT INTO project_metadata(key,value,updated_at) VALUES(?,?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		MetaKeyMigrationLastFailure, garbage, time.Now().Unix(),
	); err != nil {
		t.Fatalf("write garbage marker: %v", err)
	}

	// ReadMigrationStatus must not panic and must return the raw value.
	val, found, err := ReadMigrationStatus(db)
	if err != nil {
		t.Fatalf("ReadMigrationStatus with garbage: %v", err)
	}
	if !found {
		t.Error("expected marker to be found")
	}
	if val != garbage {
		t.Errorf("expected garbage value to be returned verbatim, got %q", val)
	}
}

// ── Test 10: Migration 005 — document_metadata table + reindex markers ───────

func TestMigration005_FreshDB(t *testing.T) {
	db := openRawDB(t)

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations on fresh DB: %v", err)
	}

	// document_metadata table must exist after migration 005.
	if !tableExists(db, "document_metadata") {
		t.Error("document_metadata table not found after migration 005")
	}

	// Migration 005 writes reindex_required="true".
	var reindexVal string
	err := db.QueryRow(`SELECT value FROM project_metadata WHERE key = ?`, MetaKeyReindexRequired).Scan(&reindexVal)
	if err != nil {
		t.Fatalf("query reindex_required: %v", err)
	}
	if reindexVal != "true" {
		t.Errorf("reindex_required: got %q want %q", reindexVal, "true")
	}

	// Migration 005 writes reindex_scope="metadata".
	var scopeVal string
	err = db.QueryRow(`SELECT value FROM project_metadata WHERE key = ?`, MetaKeyReindexScope).Scan(&scopeVal)
	if err != nil {
		t.Fatalf("query reindex_scope: %v", err)
	}
	if scopeVal != "metadata" {
		t.Errorf("reindex_scope: got %q want %q", scopeVal, "metadata")
	}
}

// ── Test 11: Migration 006 — governance_metadata table, no new reindex markers

func TestMigration006_AfterFive(t *testing.T) {
	db := openRawDB(t)

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations on fresh DB: %v", err)
	}

	// governance_metadata table must exist after migration 006.
	if !tableExists(db, "governance_metadata") {
		t.Error("governance_metadata table not found after migration 006")
	}

	// Migration 006 does NOT write any new reindex markers.
	// The scope should still be "metadata" from migration 005 (not overwritten).
	var scopeVal string
	err := db.QueryRow(`SELECT value FROM project_metadata WHERE key = ?`, MetaKeyReindexScope).Scan(&scopeVal)
	if err != nil {
		t.Fatalf("query reindex_scope: %v", err)
	}
	if scopeVal != "metadata" {
		t.Errorf("reindex_scope after 006: got %q want %q (006 must not overwrite 005 markers)", scopeVal, "metadata")
	}

	// Reason should still be from migration 005.
	var reasonVal string
	err = db.QueryRow(`SELECT value FROM project_metadata WHERE key = ?`, MetaKeyReindexReason).Scan(&reasonVal)
	if err != nil {
		t.Fatalf("query reindex_reason: %v", err)
	}
	if reasonVal == "" {
		t.Error("reindex_reason should not be empty (set by migration 005)")
	}
}

// ── Test 12: Migrations 005–009 are idempotent (file-backed DB, second Open) ─

func TestMigration005To007_Idempotent(t *testing.T) {
	dbPath := t.TempDir() + "/test_idempotent.db"

	// First open: applies all 9 migrations.
	db1, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := RunMigrations(db1); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}
	if n := countMigrationRows(db1); n != 9 {
		t.Errorf("after first open: expected 9 migration rows, got %d", n)
	}
	db1.Close()

	// Second open: same DB file — migrations must not be re-run.
	db2, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() { db2.Close() })

	if err := RunMigrations(db2); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}
	if n := countMigrationRows(db2); n != 9 {
		t.Errorf("after second open: expected 9 migration rows (no re-runs), got %d", n)
	}

	// Tables from 005–007 still exist.
	if !tableExists(db2, "document_metadata") {
		t.Error("document_metadata table missing after second open")
	}
	if !tableExists(db2, "governance_metadata") {
		t.Error("governance_metadata table missing after second open")
	}
	if !tableExists(db2, "research_metadata") {
		t.Error("research_metadata table missing after second open")
	}
	if !tableExists(db2, "domain_packs") {
		t.Error("domain_packs table missing after second open")
	}
	if !tableExists(db2, "domain_pack_fields") {
		t.Error("domain_pack_fields table missing after second open")
	}
}

// ── Test 13: Migration 007 — research_metadata table, no new reindex markers

func TestMigration007_AfterSix(t *testing.T) {
	db := openRawDB(t)

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations on fresh DB: %v", err)
	}

	if !tableExists(db, "research_metadata") {
		t.Error("research_metadata table not found after migration 007")
	}

	var scopeVal string
	err := db.QueryRow(`SELECT value FROM project_metadata WHERE key = ?`, MetaKeyReindexScope).Scan(&scopeVal)
	if err != nil {
		t.Fatalf("query reindex_scope: %v", err)
	}
	if scopeVal != "metadata" {
		t.Errorf("reindex_scope after 007: got %q want %q", scopeVal, "metadata")
	}
}

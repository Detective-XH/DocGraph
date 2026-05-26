package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrCorruptDB is returned by validateDB when core tables are absent from a
// non-empty database file — indicating a corrupt or truncated DB.
var ErrCorruptDB = errors.New("docgraph: database file is corrupt or missing core tables")

type Node struct {
	ID            string
	Kind          string
	Name          string
	QualifiedName string
	FilePath      string
	// ProjectName is runtime-only workspace context. It is not persisted in SQLite.
	ProjectName string
	StartLine   int
	EndLine     int
	Level       int
	Metadata    string
	BodyExcerpt string
	UpdatedAt   int64
}

type Edge struct {
	Source   string
	Target   string
	Kind     string
	Metadata string
	Line     int
}

type FileInfo struct {
	Path           string
	ContentHash    string
	Size           int64
	ModifiedAt     int64
	IndexedAt      int64
	NodeCount      int
	HasFrontmatter bool
	Errors         string
}

type UnresolvedRef struct {
	FromNodeID    string
	ReferenceText string
	ReferenceKind string
	Line          int
	Col           int
	FilePath      string
}

type Stats struct {
	FileCount       int
	NodeCount       int
	EdgeCount       int
	UnresolvedCount int
	DBSizeBytes     int64
	NodesByKind     map[string]int
	EdgesByKind     map[string]int
}

type SearchResult struct {
	Node Node
	Rank float64
}

type Store struct {
	db *sql.DB
}

func Open(dbPath string) (*Store, error) {
	switch err := validateDB(dbPath); {
	case err == nil:
		// healthy or fresh file — proceed
	case errors.Is(err, ErrFutureSchema):
		return nil, err // do NOT remove the file
	case errors.Is(err, ErrCorruptDB):
		os.Remove(dbPath)
		os.Remove(dbPath + "-wal")
		os.Remove(dbPath + "-shm")
		// fall through and open a fresh DB
	default:
		return nil, err
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA cache_size = -64000",
		"PRAGMA busy_timeout = 5000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %q: %w", p, err)
		}
	}

	if err := RunMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}

	return &Store{db: db}, nil
}

func validateDB(dbPath string) error {
	// 1. Fresh file — nothing to validate.
	fi, err := os.Stat(dbPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	// 2. Open read-only for inspection.
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()

	// 3. Check for future schema (DB was created by a newer binary).
	var hasMigrationsTable int
	_ = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&hasMigrationsTable)
	if hasMigrationsTable > 0 {
		// Determine the highest known version in this binary.
		highestKnown := 0
		for _, m := range migrations {
			if m.Version > highestKnown {
				highestKnown = m.Version
			}
		}
		var maxApplied int
		if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&maxApplied); err == nil {
			if maxApplied > highestKnown {
				return ErrFutureSchema
			}
		}
	}

	// 4. Core tables missing in a non-empty file → corrupt.
	var coreCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('nodes','edges','files')`).Scan(&coreCount)
	if coreCount != 3 && fi.Size() > 0 {
		return ErrCorruptDB
	}

	// 5. Otherwise healthy (old pre-migration DB, fresh empty file, or already-migrated DB).
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) InsertNodes(nodes []Node) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO nodes (id, kind, name, qualified_name, file_path, start_line, end_line, level, metadata, body_excerpt, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, n := range nodes {
		excerpt := n.BodyExcerpt
		if len(excerpt) > 500 {
			excerpt = excerpt[:500]
		}
		if _, err := stmt.Exec(n.ID, n.Kind, n.Name, n.QualifiedName, n.FilePath, n.StartLine, n.EndLine, n.Level, n.Metadata, excerpt, n.UpdatedAt); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) InsertEdges(edges []Edge) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO edges (source, target, kind, metadata, line) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, e := range edges {
		if _, err := stmt.Exec(e.Source, e.Target, e.Kind, e.Metadata, e.Line); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) UpsertFile(f FileInfo) error {
	hasFM := 0
	if f.HasFrontmatter {
		hasFM = 1
	}
	_, err := s.db.Exec(`INSERT INTO files (path, content_hash, size, modified_at, indexed_at, node_count, has_frontmatter, errors)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			content_hash = excluded.content_hash,
			size = excluded.size,
			modified_at = excluded.modified_at,
			indexed_at = excluded.indexed_at,
			node_count = excluded.node_count,
			has_frontmatter = excluded.has_frontmatter,
			errors = excluded.errors`,
		f.Path, f.ContentHash, f.Size, f.ModifiedAt, f.IndexedAt, f.NodeCount, hasFM, f.Errors)
	return err
}

func (s *Store) DeleteFileData(filePath string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Delete nodes (cascade deletes edges via FK)
	if _, err := tx.Exec(`DELETE FROM nodes WHERE file_path = ?`, filePath); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM unresolved_refs WHERE file_path = ?`, filePath); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM files WHERE path = ?`, filePath); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) InsertUnresolvedRefs(refs []UnresolvedRef) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO unresolved_refs (from_node_id, reference_text, reference_kind, line, col, file_path)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range refs {
		if _, err := stmt.Exec(r.FromNodeID, r.ReferenceText, r.ReferenceKind, r.Line, r.Col, r.FilePath); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) GetFileHash(path string) (string, error) {
	var hash string
	err := s.db.QueryRow(`SELECT content_hash FROM files WHERE path = ?`, path).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return hash, err
}

func (s *Store) GetStats() (Stats, error) {
	var st Stats

	s.db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&st.FileCount)
	s.db.QueryRow(`SELECT COUNT(*) FROM nodes`).Scan(&st.NodeCount)
	s.db.QueryRow(`SELECT COUNT(*) FROM edges`).Scan(&st.EdgeCount)
	s.db.QueryRow(`SELECT COUNT(*) FROM unresolved_refs`).Scan(&st.UnresolvedCount)

	var pageCount, pageSize int64
	s.db.QueryRow(`PRAGMA page_count`).Scan(&pageCount)
	s.db.QueryRow(`PRAGMA page_size`).Scan(&pageSize)
	st.DBSizeBytes = pageCount * pageSize

	st.NodesByKind = make(map[string]int)
	rows, err := s.db.Query(`SELECT kind, COUNT(*) FROM nodes GROUP BY kind`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var c int
		if err := rows.Scan(&k, &c); err != nil {
			return st, err
		}
		st.NodesByKind[k] = c
	}
	if err := rows.Err(); err != nil {
		return st, err
	}

	st.EdgesByKind = make(map[string]int)
	rows2, err := s.db.Query(`SELECT kind, COUNT(*) FROM edges GROUP BY kind`)
	if err != nil {
		return st, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var k string
		var c int
		if err := rows2.Scan(&k, &c); err != nil {
			return st, err
		}
		st.EdgesByKind[k] = c
	}
	return st, rows2.Err()
}

func (s *Store) GetFiles(pathFilter string) ([]FileInfo, error) {
	var rows *sql.Rows
	var err error

	if pathFilter != "" {
		rows, err = s.db.Query(`SELECT path, content_hash, size, modified_at, indexed_at, node_count, has_frontmatter, errors
			FROM files WHERE path LIKE ? ORDER BY path`, pathFilter+"%")
	} else {
		rows, err = s.db.Query(`SELECT path, content_hash, size, modified_at, indexed_at, node_count, has_frontmatter, errors
			FROM files ORDER BY path`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []FileInfo
	for rows.Next() {
		var f FileInfo
		var hasFM int
		if err := rows.Scan(&f.Path, &f.ContentHash, &f.Size, &f.ModifiedAt, &f.IndexedAt, &f.NodeCount, &hasFM, &f.Errors); err != nil {
			return nil, err
		}
		f.HasFrontmatter = hasFM != 0
		files = append(files, f)
	}
	return files, rows.Err()
}

func (s *Store) GetNodeByID(id string) (*Node, error) {
	var n Node
	err := s.db.QueryRow(`SELECT id, kind, name, qualified_name, file_path, start_line, end_line, level, metadata, body_excerpt, updated_at
		FROM nodes WHERE id = ?`, id).Scan(
		&n.ID, &n.Kind, &n.Name, &n.QualifiedName, &n.FilePath,
		&n.StartLine, &n.EndLine, &n.Level, &n.Metadata, &n.BodyExcerpt, &n.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func (s *Store) GetNodesByFile(filePath string) ([]Node, error) {
	rows, err := s.db.Query(`SELECT id, kind, name, qualified_name, file_path, start_line, end_line, level, metadata, body_excerpt, updated_at
		FROM nodes WHERE file_path = ? ORDER BY start_line`, filePath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Kind, &n.Name, &n.QualifiedName, &n.FilePath,
			&n.StartLine, &n.EndLine, &n.Level, &n.Metadata, &n.BodyExcerpt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

func (s *Store) GetAllDocumentNodes() ([]Node, error) {
	rows, err := s.db.Query(`SELECT id, kind, name, qualified_name, file_path, start_line, end_line, level, metadata, body_excerpt, updated_at
		FROM nodes WHERE kind = 'document' ORDER BY file_path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Kind, &n.Name, &n.QualifiedName, &n.FilePath,
			&n.StartLine, &n.EndLine, &n.Level, &n.Metadata, &n.BodyExcerpt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

func (s *Store) GetUnresolvedRefs() ([]UnresolvedRef, error) {
	rows, err := s.db.Query(`SELECT from_node_id, reference_text, reference_kind, line, col, file_path FROM unresolved_refs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var refs []UnresolvedRef
	for rows.Next() {
		var r UnresolvedRef
		if err := rows.Scan(&r.FromNodeID, &r.ReferenceText, &r.ReferenceKind, &r.Line, &r.Col, &r.FilePath); err != nil {
			return nil, err
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}

func (s *Store) DeleteAllUnresolvedRefs() error {
	_, err := s.db.Exec(`DELETE FROM unresolved_refs`)
	return err
}

func (s *Store) UpsertProjectMeta(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO project_metadata(key,value,updated_at) VALUES(?,?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, time.Now().Unix())
	return err
}

func (s *Store) GetProjectMeta(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM project_metadata WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// DeleteProjectMeta removes one or more keys from project_metadata.
// Missing keys are silently ignored.
func (s *Store) DeleteProjectMeta(keys ...string) error {
	for _, k := range keys {
		if _, err := s.db.Exec(`DELETE FROM project_metadata WHERE key=?`, k); err != nil {
			return err
		}
	}
	return nil
}

// SchemaVersion returns the latest applied migration version and name.
// Returns (0, "", nil) if schema_migrations table is empty or missing.
func (s *Store) SchemaVersion() (version int, name string, err error) {
	row := s.db.QueryRow(`SELECT version, name FROM schema_migrations ORDER BY version DESC LIMIT 1`)
	err = row.Scan(&version, &name)
	if err == sql.ErrNoRows {
		return 0, "", nil
	}
	if err != nil {
		// Table may not exist yet (pre-migration DB that hasn't been opened since F-18).
		return 0, "", nil
	}
	return version, name, nil
}

// ReadSectionContent reads the content of a section from its source file.
// filePath is relative to projectRoot. startLine and endLine are 1-based.
// If the result exceeds maxBytes, it is truncated with a marker.
func ReadSectionContent(filePath string, startLine, endLine int, projectRoot string, maxBytes int) (string, error) {
	if maxBytes <= 0 {
		maxBytes = 2000
	}

	absPath := filepath.Join(projectRoot, filePath)
	// Prevent path traversal: resolved path must stay within projectRoot.
	rel, err := filepath.Rel(projectRoot, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes project root")
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	lines := strings.Split(string(data), "\n")

	// Clamp to valid range (1-based to 0-based).
	start := startLine - 1
	if start < 0 {
		start = 0
	}
	end := endLine
	if end > len(lines) {
		end = len(lines)
	}
	if start >= end {
		return "", nil
	}

	content := strings.Join(lines[start:end], "\n")

	if len(content) > maxBytes {
		content = content[:maxBytes] + fmt.Sprintf("\n[content truncated at %d bytes, use Read tool for full text]", maxBytes)
	}

	return content, nil
}

package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Detective-XH/docgraph/internal/docformat"
	"github.com/Detective-XH/docgraph/internal/domainpacks"

	_ "modernc.org/sqlite"
)

// maxLiveReadBytes bounds on-demand section reads. It sits above the largest
// supported physical-file cap (50 MB for PDF) so any legitimately indexed file
// is always readable, while a file that grew unbounded after indexing is
// rejected instead of loaded whole into memory.
const maxLiveReadBytes = 64 * 1024 * 1024

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

// baseDB is the shared SQLite handle plus the store-wide reindex lock. Store and
// every domain sub-store (e.g. *entityStore) embed the SAME *baseDB so they all
// operate on one connection and one mutex — IndexMu MUST stay shared to serialise
// whole-store reindexing. This is the foundation of the Step 2 sub-store split:
// methods move from (s *Store) to their domain sub-store but keep reaching the DB
// via the embedded baseDB, so sub-stores never need to reference one another.
type baseDB struct {
	db      *sql.DB
	IndexMu sync.Mutex // serialises concurrent index/reindex calls on the same store
}

type Store struct {
	*baseDB
	Entity *entityStore // entity graph: entities + entity_mentions
}

func Open(dbPath string) (*Store, error) {
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

	if err := bootstrapSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("bootstrap schema: %w", err)
	}

	base := &baseDB{db: db}
	st := &Store{baseDB: base, Entity: &entityStore{baseDB: base}}
	if err := st.SyncDomainPacks(domainpacks.Packs()); err != nil {
		db.Close()
		return nil, fmt.Errorf("sync domain packs: %w", err)
	}

	return st, nil
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

// NodesIsEmpty reports whether the nodes base table has no rows — i.e. this index
// run started against a fresh/cold DB. Callers use it to skip the eager per-file
// stale-row delete block on a cold-start, where every DELETE matches 0 rows and is
// a true no-op (no rows → no FK cascade → no AFTER DELETE trigger). See the call
// site for why an empty nodes table implies the delete targets are empty too.
// EXISTS short-circuits on the first row, so it stays O(1) on a populated DB.
func (s *Store) NodesIsEmpty() (bool, error) {
	var present int
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM nodes)`).Scan(&present); err != nil {
		return false, fmt.Errorf("NodesIsEmpty: %w", err)
	}
	return present == 0, nil
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

	if err := s.db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&st.FileCount); err != nil {
		return st, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM nodes`).Scan(&st.NodeCount); err != nil {
		return st, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM edges`).Scan(&st.EdgeCount); err != nil {
		return st, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM unresolved_refs`).Scan(&st.UnresolvedCount); err != nil {
		return st, err
	}

	var pageCount, pageSize int64
	if err := s.db.QueryRow(`PRAGMA page_count`).Scan(&pageCount); err != nil {
		return st, err
	}
	if err := s.db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		return st, err
	}
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

// GetTopLevelDirs returns the deduplicated, sorted list of first path segments
// found in the files table. Files stored at the repo root (no slash in the
// path) yield an empty segment and are excluded. The result is suitable for
// surfacing as "known indexed directories" when a path-filtered query returns
// zero results.
func (s *Store) GetTopLevelDirs() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT SUBSTR(path, 1, INSTR(path||'/', '/') - 1) FROM files ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dirs []string
	for rows.Next() {
		var seg string
		if err := rows.Scan(&seg); err != nil {
			return nil, err
		}
		if seg != "" {
			dirs = append(dirs, seg)
		}
	}
	return dirs, rows.Err()
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

// GetNodesByIDs loads nodes for many IDs in one query. Missing IDs are absent from the map.
func (s *Store) GetNodesByIDs(ids []string) (map[string]*Node, error) {
	out := make(map[string]*Node, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(`SELECT id, kind, name, qualified_name, file_path, start_line, end_line, level, metadata, body_excerpt, updated_at
		FROM nodes WHERE id IN (`+inPlaceholders(len(ids))+`)`, toArgs(ids)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Kind, &n.Name, &n.QualifiedName, &n.FilePath,
			&n.StartLine, &n.EndLine, &n.Level, &n.Metadata, &n.BodyExcerpt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out[n.ID] = &n
	}
	return out, rows.Err()
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

// ReadSectionContent reads the content of a section from its source file.
// filePath is relative to projectRoot. startLine and endLine are 1-based.
// If the result exceeds maxBytes, it is truncated with a marker.
func ReadSectionContent(filePath string, startLine, endLine int, projectRoot string, maxBytes int) (string, error) {
	if maxBytes <= 0 {
		maxBytes = 2000
	}

	if filepath.IsAbs(filePath) {
		return "", fmt.Errorf("path escapes project root")
	}
	rootAbs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	absPath := filepath.Join(rootAbs, filePath)

	// Prevent lexical traversal before resolving symlinks.
	rel, err := filepath.Rel(rootAbs, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes project root")
	}

	// Resolve both sides before reading. os.ReadFile follows symlinks, so a
	// lexical Rel check alone is not enough for stale or adversarial DB paths.
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	fileReal, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("resolve file: %w", err)
	}
	realRel, err := filepath.Rel(rootReal, fileReal)
	if err != nil || realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes project root")
	}

	data, err := docformat.ReadFileCapped(fileReal, maxLiveReadBytes)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	lines := strings.Split(string(data), "\n")

	// Clamp to valid range (1-based to 0-based).
	start := max(startLine-1, 0)
	end := min(endLine, len(lines))
	if start >= end {
		return "", nil
	}

	content := strings.Join(lines[start:end], "\n")

	if len(content) > maxBytes {
		content = content[:maxBytes] + fmt.Sprintf("\n[content truncated at %d bytes, use Read tool for full text]", maxBytes)
	}

	return content, nil
}

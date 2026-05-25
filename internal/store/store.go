package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type Node struct {
	ID            string
	Kind          string
	Name          string
	QualifiedName string
	FilePath      string
	StartLine     int
	EndLine       int
	Level         int
	Metadata      string
	BodyExcerpt   string
	UpdatedAt     int64
}

type Edge struct {
	Source   string
	Target  string
	Kind    string
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

type TagCount struct {
	Name  string
	Count int
}

type Store struct {
	db *sql.DB
}

func Open(dbPath string) (*Store, error) {
	if err := validateDB(dbPath); err != nil {
		os.Remove(dbPath)
		os.Remove(dbPath + "-wal")
		os.Remove(dbPath + "-shm")
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

	if err := initSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return &Store{db: db}, nil
}

func validateDB(dbPath string) error {
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('nodes','edges','files')`).Scan(&count)
	if err != nil || count != 3 {
		return fmt.Errorf("invalid schema")
	}
	var triggerCount int
	db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger'`).Scan(&triggerCount)
	if triggerCount > 10 {
		return fmt.Errorf("suspicious trigger count: %d", triggerCount)
	}
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

func (s *Store) Search(query string, kind string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	// Cap query length to prevent resource exhaustion
	if len(query) > 1000 {
		query = query[:1000]
	}

	words := strings.Fields(query)
	short := len(query) < 3 || (len(words) == 1 && len([]rune(words[0])) < 3)

	var rows *sql.Rows
	var err error

	if short {
		escaped := strings.NewReplacer("%", "\\%", "_", "\\_").Replace(query)
		likePattern := "%" + escaped + "%"
		if kind != "" {
			rows, err = s.db.Query(`
				SELECT n.id, n.kind, n.name, n.qualified_name, n.file_path,
					n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at,
					0.0 as rank
				FROM nodes n
				WHERE (n.name LIKE ? ESCAPE '\' OR n.body_excerpt LIKE ? ESCAPE '\') AND n.kind = ?
				ORDER BY n.name
				LIMIT ?`, likePattern, likePattern, kind, limit)
		} else {
			rows, err = s.db.Query(`
				SELECT n.id, n.kind, n.name, n.qualified_name, n.file_path,
					n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at,
					0.0 as rank
				FROM nodes n
				WHERE n.name LIKE ? ESCAPE '\' OR n.body_excerpt LIKE ? ESCAPE '\'
				ORDER BY n.name
				LIMIT ?`, likePattern, likePattern, limit)
		}
	} else {
		quoted := make([]string, len(words))
		for i, w := range words {
			quoted[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
		}
		ftsQuery := strings.Join(quoted, " ")

		if kind != "" {
			rows, err = s.db.Query(`
				SELECT n.id, n.kind, n.name, n.qualified_name, n.file_path,
					n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at,
					rank
				FROM nodes_fts f
				JOIN nodes n ON n.rowid = f.rowid
				WHERE nodes_fts MATCH ? AND n.kind = ?
				ORDER BY rank
				LIMIT ?`, ftsQuery, kind, limit)
		} else {
			rows, err = s.db.Query(`
				SELECT n.id, n.kind, n.name, n.qualified_name, n.file_path,
					n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at,
					rank
				FROM nodes_fts f
				JOIN nodes n ON n.rowid = f.rowid
				WHERE nodes_fts MATCH ?
				ORDER BY rank
				LIMIT ?`, ftsQuery, limit)
		}
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var sr SearchResult
		if err := rows.Scan(
			&sr.Node.ID, &sr.Node.Kind, &sr.Node.Name, &sr.Node.QualifiedName,
			&sr.Node.FilePath, &sr.Node.StartLine, &sr.Node.EndLine, &sr.Node.Level,
			&sr.Node.Metadata, &sr.Node.BodyExcerpt, &sr.Node.UpdatedAt,
			&sr.Rank,
		); err != nil {
			return nil, err
		}
		results = append(results, sr)
	}
	return results, rows.Err()
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

func (s *Store) DeleteEdgesByKind(kind string) error {
	_, err := s.db.Exec(`DELETE FROM edges WHERE kind = ?`, kind)
	return err
}

func (s *Store) GetIncomingEdges(nodeID string) ([]Edge, error) {
	n, _ := s.GetNodeByID(nodeID)
	var rows *sql.Rows
	var err error
	if n != nil && n.Kind == "document" {
		rows, err = s.db.Query(`SELECT e.source, e.target, e.kind, e.metadata, e.line FROM edges e
			JOIN nodes t ON t.id = e.target
			WHERE t.file_path = ? AND e.kind IN ('references','wikilinks_to','related_to','embeds')
			ORDER BY e.source`, n.FilePath)
	} else {
		rows, err = s.db.Query(`SELECT source, target, kind, metadata, line FROM edges
			WHERE target = ? AND kind IN ('references','wikilinks_to','related_to','embeds')
			ORDER BY source`, nodeID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.Source, &e.Target, &e.Kind, &e.Metadata, &e.Line); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

func (s *Store) GetOutgoingEdges(nodeID string) ([]Edge, error) {
	n, _ := s.GetNodeByID(nodeID)
	var rows *sql.Rows
	var err error
	if n != nil && n.Kind == "document" {
		rows, err = s.db.Query(`SELECT e.source, e.target, e.kind, e.metadata, e.line FROM edges e
			JOIN nodes s ON s.id = e.source
			WHERE s.file_path = ? AND e.kind IN ('references','wikilinks_to','related_to','embeds','links_external')
			ORDER BY e.target`, n.FilePath)
	} else {
		rows, err = s.db.Query(`SELECT source, target, kind, metadata, line FROM edges
			WHERE source = ? AND kind IN ('references','wikilinks_to','related_to','embeds','links_external')
			ORDER BY target`, nodeID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.Source, &e.Target, &e.Kind, &e.Metadata, &e.Line); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

func (s *Store) FindNodeByName(name string) (*Node, error) {
	var n Node
	err := s.db.QueryRow(`SELECT id, kind, name, qualified_name, file_path, start_line, end_line, level, metadata, body_excerpt, updated_at
		FROM nodes WHERE lower(name) = lower(?) AND kind = 'document' LIMIT 1`, name).Scan(
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

func (s *Store) FindNodeByPath(path string) (*Node, error) {
	var n Node
	err := s.db.QueryRow(`SELECT id, kind, name, qualified_name, file_path, start_line, end_line, level, metadata, body_excerpt, updated_at
		FROM nodes WHERE (id = ? OR file_path = ? OR qualified_name = ?) LIMIT 1`, path, path, path).Scan(
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

func (s *Store) GetChildHeadings(docFilePath string) ([]Node, error) {
	rows, err := s.db.Query(`SELECT id, kind, name, qualified_name, file_path, start_line, end_line, level, metadata, body_excerpt, updated_at
		FROM nodes WHERE file_path = ? AND kind = 'heading' ORDER BY start_line`, docFilePath)
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

func (s *Store) GetAllDocumentIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM nodes WHERE kind = 'document' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) GetEdgesByTarget(targetID string) ([]Edge, error) {
	rows, err := s.db.Query(`SELECT source, target, kind, metadata, line FROM edges
		WHERE target = ? AND kind IN ('references','wikilinks_to','related_to','embeds')`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var edges []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.Source, &e.Target, &e.Kind, &e.Metadata, &e.Line); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

func (s *Store) GetEdgesBySource(sourceID string) ([]Edge, error) {
	rows, err := s.db.Query(`SELECT source, target, kind, metadata, line FROM edges
		WHERE source = ? AND kind IN ('references','wikilinks_to','related_to','embeds')`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var edges []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.Source, &e.Target, &e.Kind, &e.Metadata, &e.Line); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
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

// GetAllTags returns all tag names with the count of documents that use them,
// ordered by count descending then name ascending.
func (s *Store) GetAllTags() ([]TagCount, error) {
	rows, err := s.db.Query(`
		SELECT name, COUNT(*) as cnt
		FROM nodes
		WHERE kind = 'tag'
		GROUP BY name
		ORDER BY cnt DESC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []TagCount
	for rows.Next() {
		var t TagCount
		if err := rows.Scan(&t.Name, &t.Count); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

// GetDocumentsByTag returns all document nodes that are tagged with the given tag name
// (case-insensitive). Results are ordered by file path.
func (s *Store) GetDocumentsByTag(tagName string) ([]Node, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT n.id, n.kind, n.name, n.qualified_name, n.file_path,
		       n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at
		FROM nodes n
		JOIN edges e ON e.source = n.id AND e.kind = 'tagged'
		JOIN nodes t ON t.id = e.target AND t.kind = 'tag' AND lower(t.name) = lower(?)
		WHERE n.kind = 'document'
		ORDER BY n.file_path`, tagName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []Node
	for rows.Next() {
		var nd Node
		if err := rows.Scan(&nd.ID, &nd.Kind, &nd.Name, &nd.QualifiedName,
			&nd.FilePath, &nd.StartLine, &nd.EndLine, &nd.Level,
			&nd.Metadata, &nd.BodyExcerpt, &nd.UpdatedAt); err != nil {
			return nil, err
		}
		nodes = append(nodes, nd)
	}
	return nodes, rows.Err()
}

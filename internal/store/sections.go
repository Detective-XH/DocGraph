package store

import (
	"database/sql"
	"fmt"
)

// SectionChunk is an indexed snapshot of a section's content captured at index time.
// It backs reads and diffs without live file I/O (resolves TOCTOU limitation L-8).
//
// start_line and end_line are -1 for non-line-based sources.
type SectionChunk struct {
	NodeID      string
	FilePath    string
	StartLine   int
	EndLine     int
	ContentHash string // file-level SHA-256 at index time
	SectionHash string // SHA-256 of section text; drift/diff primitive
	HeadingPath string // breadcrumb "Parent > Child"; "" for document nodes
	Text        string // bounded section content (≤ 10KB, H-19)
}

// UpsertSectionChunks inserts or replaces all chunks in a single transaction.
func (s *Store) UpsertSectionChunks(chunks []SectionChunk) error {
	if len(chunks) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("UpsertSectionChunks begin tx: %w", err)
	}
	stmt, err := tx.Prepare(`
		INSERT INTO section_chunks (node_id, file_path, start_line, end_line, content_hash, section_hash, heading_path, text)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			file_path    = excluded.file_path,
			start_line   = excluded.start_line,
			end_line     = excluded.end_line,
			content_hash = excluded.content_hash,
			section_hash = excluded.section_hash,
			heading_path = excluded.heading_path,
			text         = excluded.text`)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("UpsertSectionChunks prepare: %w", err)
	}
	defer stmt.Close()

	for _, c := range chunks {
		if _, err := stmt.Exec(c.NodeID, c.FilePath, c.StartLine, c.EndLine,
			c.ContentHash, c.SectionHash, c.HeadingPath, c.Text); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("UpsertSectionChunks exec node_id=%s: %w", c.NodeID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("UpsertSectionChunks commit: %w", err)
	}
	return nil
}

// GetSectionChunk retrieves the indexed snapshot for a node. Returns (nil, false, nil) if not found.
func (s *Store) GetSectionChunk(nodeID string) (*SectionChunk, bool, error) {
	row := s.db.QueryRow(`
		SELECT node_id, file_path, start_line, end_line, content_hash, section_hash, heading_path, text
		FROM section_chunks WHERE node_id = ?`, nodeID)
	var c SectionChunk
	if err := row.Scan(&c.NodeID, &c.FilePath, &c.StartLine, &c.EndLine,
		&c.ContentHash, &c.SectionHash, &c.HeadingPath, &c.Text); err == sql.ErrNoRows {
		return nil, false, nil
	} else if err != nil {
		return nil, false, fmt.Errorf("GetSectionChunk: %w", err)
	}
	return &c, true, nil
}

// DeleteSectionChunksByFile removes all chunks for a file path (called before reindexing a file).
func (s *Store) DeleteSectionChunksByFile(filePath string) error {
	_, err := s.db.Exec(`DELETE FROM section_chunks WHERE file_path = ?`, filePath)
	if err != nil {
		return fmt.Errorf("DeleteSectionChunksByFile: %w", err)
	}
	return nil
}

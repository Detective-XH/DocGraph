package store

import "database/sql"

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
			ORDER BY e.source, e.kind, e.line, e.target`, n.FilePath)
	} else {
		rows, err = s.db.Query(`SELECT source, target, kind, metadata, line FROM edges
			WHERE target = ? AND kind IN ('references','wikilinks_to','related_to','embeds')
			ORDER BY source, kind, line, target`, nodeID)
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

// GetIncomingEdgesBatch returns incoming edges for many nodeIDs in one call.
// Returns map[nodeID][]Edge where each slice is in SQL result-row order.
// Invariant: the impact BFS queues only document-node IDs; each document
// maps to exactly one file_path. If that invariant ever breaks, doc-branch
// results would be silently merged for nodes sharing a file_path.
func (s *Store) GetIncomingEdgesBatch(nodeIDs []string) (map[string][]Edge, error) {
	result := make(map[string][]Edge, len(nodeIDs))
	if len(nodeIDs) == 0 {
		return result, nil
	}
	nodes, err := s.GetNodesByIDs(nodeIDs)
	if err != nil {
		return nil, err
	}

	// Partition into document nodes and non-document nodes.
	var docFilePaths []string
	filePathToNodeID := make(map[string]string) // file_path → original nodeID for map-back
	var nonDocIDs []string
	for _, nodeID := range nodeIDs {
		n := nodes[nodeID]
		if n != nil && n.Kind == "document" {
			docFilePaths = append(docFilePaths, n.FilePath)
			filePathToNodeID[n.FilePath] = nodeID
		} else {
			nonDocIDs = append(nonDocIDs, nodeID)
		}
	}

	if err := loadDocIncomingEdges(s.db, docFilePaths, filePathToNodeID, result); err != nil {
		return nil, err
	}
	if err := loadNonDocIncomingEdges(s.db, nonDocIDs, result); err != nil {
		return nil, err
	}
	return result, nil
}

// loadDocIncomingEdges fills result with incoming edges for document-kind nodes.
// SELECT includes t.file_path so each row can be demuxed back to the frontier nodeID.
func loadDocIncomingEdges(db *sql.DB, docFilePaths []string, filePathToNodeID map[string]string, result map[string][]Edge) error {
	if len(docFilePaths) == 0 {
		return nil
	}
	rows, err := db.Query(`SELECT e.source, e.target, e.kind, e.metadata, e.line, t.file_path
		FROM edges e JOIN nodes t ON t.id = e.target
		WHERE t.file_path IN (`+inPlaceholders(len(docFilePaths))+`)
		  AND e.kind IN ('references','wikilinks_to','related_to','embeds')
		ORDER BY e.source, e.kind, e.line, e.target`, toArgs(docFilePaths)...) // #nosec G202 -- structural SQL: column names are compile-time constants and inPlaceholders(n)/constant fragments; all user values are bound via ? parameters, never interpolated
	if err != nil {
		return err
	}
	for rows.Next() {
		var e Edge
		var fp string
		if err := rows.Scan(&e.Source, &e.Target, &e.Kind, &e.Metadata, &e.Line, &fp); err != nil {
			rows.Close()
			return err
		}
		nid := filePathToNodeID[fp]
		result[nid] = append(result[nid], e)
	}
	return rows.Close()
}

// loadNonDocIncomingEdges fills result with incoming edges for non-document-kind nodes.
func loadNonDocIncomingEdges(db *sql.DB, nonDocIDs []string, result map[string][]Edge) error {
	if len(nonDocIDs) == 0 {
		return nil
	}
	rows, err := db.Query(`SELECT source, target, kind, metadata, line
		FROM edges WHERE target IN (`+inPlaceholders(len(nonDocIDs))+`)
		  AND kind IN ('references','wikilinks_to','related_to','embeds')
		ORDER BY source, kind, line, target`, toArgs(nonDocIDs)...) // #nosec G202 -- structural SQL: column names are compile-time constants and inPlaceholders(n)/constant fragments; all user values are bound via ? parameters, never interpolated
	if err != nil {
		return err
	}
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.Source, &e.Target, &e.Kind, &e.Metadata, &e.Line); err != nil {
			rows.Close()
			return err
		}
		result[e.Target] = append(result[e.Target], e)
	}
	return rows.Close()
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

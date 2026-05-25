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

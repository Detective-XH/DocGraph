package store

type TagCount struct {
	Name  string
	Count int
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

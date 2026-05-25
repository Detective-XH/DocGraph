package store

import (
	"database/sql"
	"strings"
)

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

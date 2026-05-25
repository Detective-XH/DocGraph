package store

import "strings"

// DeleteSimilarityEdgesForDocs removes similar_to edges where source or target
// is in nodeIDs. Used by incremental similarity to clear stale edges before
// recomputing only the affected pairs.
func (s *Store) DeleteSimilarityEdgesForDocs(nodeIDs []string) error {
	if len(nodeIDs) == 0 {
		return nil
	}
	placeholders := make([]string, len(nodeIDs))
	args := make([]interface{}, 0, len(nodeIDs)*2+1)
	args = append(args, "similar_to")
	for i, id := range nodeIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	ph := strings.Join(placeholders, ",")
	for _, id := range nodeIDs {
		args = append(args, id)
	}
	q := "DELETE FROM edges WHERE kind=? AND (source IN (" + ph + ") OR target IN (" + ph + "))"
	_, err := s.db.Exec(q, args...)
	return err
}

// GetSimilarEdgesForDoc returns all similar_to edges where source or target equals docID.
func (s *Store) GetSimilarEdgesForDoc(docID string) ([]Edge, error) {
	rows, err := s.db.Query(`
		SELECT source, target, kind, metadata, line FROM edges
		WHERE kind = 'similar_to' AND (source = ? OR target = ?)`, docID, docID)
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

func (s *Store) DeleteNeuralSimilarityEdgesByModel(modelID string) (int64, error) {
	res, err := s.db.Exec(`
		DELETE FROM edges
		WHERE kind = 'similar_to'
		  AND json_extract(metadata, '$.engine') = 'neural'
		  AND json_extract(metadata, '$.model_id') = ?`, modelID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *Store) DeleteNeuralSimilarityEdgesForDoc(docID string) error {
	_, err := s.db.Exec(`
		DELETE FROM edges
		WHERE kind = 'similar_to'
		  AND json_extract(metadata, '$.engine') = 'neural'
		  AND (source = ? OR target = ?)`, docID, docID)
	return err
}

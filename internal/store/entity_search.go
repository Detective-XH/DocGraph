package store

// collectEntityFilteredCandidates adds candidates for nodes that mention
// entities matching the Entity filter in req.  It mirrors the score bump
// used by collectMetadataFilteredCandidates (score += 16).
//
// Two paths:
//  1. EntityID set  — direct lookup in entity_mentions WHERE entity_id = ?
//  2. EntityType set — first resolve all entities of that type, then look up
//     their mentions.
func (es *entityStore) collectEntityFilteredCandidates(
	req searchRequest,
	candidates map[string]*searchCandidate,
) error {
	var nodeIDs []string

	if req.Entity.EntityID != "" {
		ids, err := es.nodeIDsByEntityID(req.Entity.EntityID)
		if err != nil {
			return err
		}
		nodeIDs = ids
	} else if req.Entity.EntityType != "" {
		entityIDs, err := es.entityIDsByType(req.Entity.EntityType)
		if err != nil {
			return err
		}
		for _, eid := range entityIDs {
			ids, err := es.nodeIDsByEntityID(eid)
			if err != nil {
				return err
			}
			nodeIDs = append(nodeIDs, ids...)
		}
	}

	for _, nodeID := range nodeIDs {
		if _, ok := candidates[nodeID]; ok {
			// Already in the candidate map — just add source tag and bump.
			c := candidates[nodeID]
			c.Sources["entity_filter"] = true
			c.Score += 16
			continue
		}
		// Fetch the node so we can add it.
		n, err := es.getNodeByID(nodeID)
		if err != nil {
			// Node not found (deleted race) — skip.
			continue
		}
		c := addCandidate(candidates, n, "entity_filter", 0)
		c.Score += 16
	}
	return nil
}

// nodeIDsByEntityID returns the distinct node_ids mentioned by a given entity.
// Capped at 1000 rows to match the FTS5 query result cap.
func (es *entityStore) nodeIDsByEntityID(entityID string) ([]string, error) {
	rows, err := es.db.Query(
		`SELECT DISTINCT node_id FROM entity_mentions WHERE entity_id = ? LIMIT 1000`,
		entityID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// entityIDsByType returns entity UUIDs for a given entity_type.
// Capped at 1000 rows to match the FTS5 query result cap.
func (es *entityStore) entityIDsByType(entityType string) ([]string, error) {
	rows, err := es.db.Query(
		`SELECT id FROM entities WHERE entity_type = ? LIMIT 1000`,
		entityType,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// getNodeByID fetches a single node row by primary key.
func (es *entityStore) getNodeByID(nodeID string) (Node, error) {
	row := es.db.QueryRow(`
		SELECT id, kind, name, qualified_name, file_path,
		       start_line, end_line, level, metadata, body_excerpt, updated_at
		FROM nodes WHERE id = ?`, nodeID)
	return scanNode(row)
}

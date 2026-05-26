package store

import "fmt"

// findConflictingPolicies returns policy.conflicting findings from three signals:
//  1. Two approved docs connected by a similar_to edge (competing active authorities).
//  2. Two approved docs connected by a similar_to edge with different canonical_source values.
//  3. Multiple non-archived/non-superseded docs that all claim the same supersedes target.
func (s *Store) findConflictingPolicies(opts DriftAuditOpts) ([]DriftFinding, error) {
	var all []DriftFinding

	sig1, err := s.conflictingByAuthority(opts)
	if err != nil {
		return nil, err
	}
	all = append(all, sig1...)

	sig2, err := s.conflictingByCanonicalSource(opts)
	if err != nil {
		return nil, err
	}
	all = append(all, sig2...)

	sig3, err := s.conflictingBySupersedes(opts)
	if err != nil {
		return nil, err
	}
	all = append(all, sig3...)

	return all, nil
}

// conflictingByAuthority finds pairs of approved documents connected by a
// similar_to edge whose score meets the threshold. These represent two active
// authorities claiming the same topic, which is an active policy conflict.
// Pairs are deduplicated by requiring source < target in the edge direction.
func (s *Store) conflictingByAuthority(opts DriftAuditOpts) ([]DriftFinding, error) {
	rows, err := s.db.Query(`
		SELECT
			e.source, ns.file_path,
			e.target, nt.file_path,
			CAST(json_extract(e.metadata, '$.score') AS REAL) AS score
		FROM edges e
		JOIN governance_metadata gm_src ON gm_src.node_id = e.source
		JOIN governance_metadata gm_tgt ON gm_tgt.node_id = e.target
		JOIN nodes ns ON ns.id = e.source
		JOIN nodes nt ON nt.id = e.target
		WHERE e.kind = 'similar_to'
		  AND e.source < e.target
		  AND CAST(json_extract(e.metadata, '$.score') AS REAL) >= ?
		  AND gm_src.status = 'approved'
		  AND gm_tgt.status = 'approved'
		LIMIT 1000
	`, opts.SimilarityMin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var findings []DriftFinding
	for rows.Next() {
		var srcID, srcPath, tgtID, tgtPath string
		var score float64
		if err := rows.Scan(&srcID, &srcPath, &tgtID, &tgtPath, &score); err != nil {
			return nil, err
		}
		findings = append(findings, DriftFinding{
			Code:          CodePolicyConflicting,
			NodeID:        srcID,
			FilePath:      srcPath,
			RelatedNodeID: tgtID,
			RelatedPath:   tgtPath,
			Severity:      "error",
			Message:       fmt.Sprintf("Approved documents claim same topic (similarity %.2f)", score),
			Evidence:      fmt.Sprintf("similar_to score=%.2f, both status=approved", score),
		})
	}
	return findings, rows.Err()
}

// conflictingByCanonicalSource finds pairs of approved documents connected by a
// similar_to edge where both docs declare a non-empty canonical_source but the
// values differ. This means two documents each assert a different authoritative
// source for the same topic — a direct policy conflict.
// Pairs are deduplicated by requiring source < target in the edge direction.
func (s *Store) conflictingByCanonicalSource(opts DriftAuditOpts) ([]DriftFinding, error) {
	rows, err := s.db.Query(`
		SELECT
			e.source, ns.file_path, COALESCE(gm_src.canonical_source, ''),
			e.target, nt.file_path, COALESCE(gm_tgt.canonical_source, ''),
			CAST(json_extract(e.metadata, '$.score') AS REAL) AS score
		FROM edges e
		JOIN governance_metadata gm_src ON gm_src.node_id = e.source
		JOIN governance_metadata gm_tgt ON gm_tgt.node_id = e.target
		JOIN nodes ns ON ns.id = e.source
		JOIN nodes nt ON nt.id = e.target
		WHERE e.kind = 'similar_to'
		  AND e.source < e.target
		  AND CAST(json_extract(e.metadata, '$.score') AS REAL) >= ?
		  AND gm_src.status = 'approved'
		  AND gm_tgt.status = 'approved'
		  AND COALESCE(gm_src.canonical_source, '') != ''
		  AND COALESCE(gm_tgt.canonical_source, '') != ''
		  AND gm_src.canonical_source != gm_tgt.canonical_source
		LIMIT 1000
	`, opts.SimilarityMin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var findings []DriftFinding
	for rows.Next() {
		var srcID, srcPath, srcCanon, tgtID, tgtPath, tgtCanon string
		var score float64
		if err := rows.Scan(&srcID, &srcPath, &srcCanon, &tgtID, &tgtPath, &tgtCanon, &score); err != nil {
			return nil, err
		}
		findings = append(findings, DriftFinding{
			Code:          CodePolicyConflicting,
			NodeID:        srcID,
			FilePath:      srcPath,
			RelatedNodeID: tgtID,
			RelatedPath:   tgtPath,
			Severity:      "error",
			Message:       fmt.Sprintf("Approved documents with conflicting canonical sources claim same topic (similarity %.2f)", score),
			Evidence:      fmt.Sprintf("canonical_source: %q vs %q", srcCanon, tgtCanon),
		})
	}
	return findings, rows.Err()
}

// conflictingBySupersedes finds cases where two or more non-archived/non-superseded
// documents both declare the same supersedes target. Only one document should
// legitimately supersede any given target; multiple claimants signal a conflict.
// Emits one finding per node involved.
func (s *Store) conflictingBySupersedes(opts DriftAuditOpts) ([]DriftFinding, error) {
	rows, err := s.db.Query(`
		SELECT gm.node_id, n.file_path, gm.supersedes, gm.status
		FROM governance_metadata gm
		JOIN nodes n ON n.id = gm.node_id
		WHERE gm.supersedes != ''
		  AND gm.status NOT IN ('archived', 'superseded')
		  AND gm.supersedes IN (
		      SELECT supersedes
		      FROM governance_metadata
		      WHERE supersedes != ''
		        AND status NOT IN ('archived', 'superseded')
		      GROUP BY supersedes
		      HAVING COUNT(*) > 1
		  )
		ORDER BY gm.supersedes, gm.node_id
		LIMIT 1000
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var findings []DriftFinding
	for rows.Next() {
		var nodeID, filePath, supersedes, status string
		if err := rows.Scan(&nodeID, &filePath, &supersedes, &status); err != nil {
			return nil, err
		}
		findings = append(findings, DriftFinding{
			Code:     CodePolicyConflicting,
			NodeID:   nodeID,
			FilePath: filePath,
			Severity: "error",
			Message:  fmt.Sprintf("Multiple active documents supersede %q", supersedes),
			Evidence: fmt.Sprintf("supersedes=%q, status=%s", supersedes, status),
		})
	}
	return findings, rows.Err()
}

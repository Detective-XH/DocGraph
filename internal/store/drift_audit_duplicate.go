package store

import (
	"fmt"
)

// findDuplicatePolicies returns policy.duplicate findings.
//
// A pair is flagged when:
//   - Both nodes have status = 'approved' in governance_metadata.
//   - A similar_to edge exists between them whose metadata JSON contains
//     "score" >= opts.SimilarityMin (default 0.75).
//
// Similar_to edges are always inserted with source < target (the similarity
// engine normalises ordering at line 118-120 of similarity/similarity.go), so
// the WHERE source < target condition provides a correct dedup without
// producing phantom results.
func (s *Store) findDuplicatePolicies(opts DriftAuditOpts) ([]DriftFinding, error) {
	const q = `
SELECT
    e.source,
    e.target,
    ns.file_path,
    nt.file_path,
    CAST(json_extract(e.metadata, '$.score') AS REAL) AS score
FROM edges e
JOIN governance_metadata gs ON gs.node_id = e.source AND gs.status = 'approved'
JOIN governance_metadata gt ON gt.node_id = e.target AND gt.status = 'approved'
JOIN nodes ns ON ns.id = e.source
JOIN nodes nt ON nt.id = e.target
WHERE e.kind = 'similar_to'
  AND e.source < e.target
  AND CAST(json_extract(e.metadata, '$.score') AS REAL) >= ?
ORDER BY score DESC, e.source, e.target
LIMIT ?`

	rows, err := s.db.Query(q, opts.SimilarityMin, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("findDuplicatePolicies: %w", err)
	}
	defer rows.Close()

	var findings []DriftFinding
	for rows.Next() {
		var src, tgt, srcPath, tgtPath string
		var score float64
		if err := rows.Scan(&src, &tgt, &srcPath, &tgtPath, &score); err != nil {
			return nil, fmt.Errorf("findDuplicatePolicies scan: %w", err)
		}
		findings = append(findings, DriftFinding{
			Code:          CodePolicyDuplicate,
			NodeID:        src,
			FilePath:      srcPath,
			RelatedNodeID: tgt,
			RelatedPath:   tgtPath,
			Severity:      "warning",
			Message:       fmt.Sprintf("Duplicate approved policies (similarity %.2f)", score),
			Evidence:      fmt.Sprintf("similar_to score=%.2f", score),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("findDuplicatePolicies rows: %w", err)
	}
	return findings, nil
}

// findNonCanonicalCopies returns policy.non_canonical findings.
//
// A conflict exists when two or more non-archived/non-superseded documents
// share the same non-empty canonical_source value.  One finding is emitted
// per conflicting node so callers can surface every affected document.
//
// NULL status is treated as non-archived (only explicit 'archived' and
// 'superseded' values are excluded) so that documents without a status field
// are still checked for canonical-source conflicts.
func (s *Store) findNonCanonicalCopies(opts DriftAuditOpts) ([]DriftFinding, error) {
	// Step 1: find canonical_source values claimed by more than one active node.
	const qConflicts = `
SELECT canonical_source, COUNT(*) AS cnt
FROM governance_metadata
WHERE canonical_source IS NOT NULL
  AND canonical_source != ''
  AND (status IS NULL OR status NOT IN ('archived', 'superseded'))
GROUP BY canonical_source
HAVING cnt > 1
LIMIT ?`

	crows, err := s.db.Query(qConflicts, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("findNonCanonicalCopies conflicts: %w", err)
	}
	defer crows.Close()

	type conflict struct {
		canonicalSource string
		count           int
	}
	var conflicts []conflict
	for crows.Next() {
		var c conflict
		if err := crows.Scan(&c.canonicalSource, &c.count); err != nil {
			return nil, fmt.Errorf("findNonCanonicalCopies conflict scan: %w", err)
		}
		conflicts = append(conflicts, c)
	}
	if err := crows.Err(); err != nil {
		return nil, fmt.Errorf("findNonCanonicalCopies conflict rows: %w", err)
	}

	if len(conflicts) == 0 {
		return nil, nil
	}

	// Step 2: for each conflicted canonical_source, emit one finding per node.
	const qNodes = `
SELECT gm.node_id, n.file_path
FROM governance_metadata gm
JOIN nodes n ON n.id = gm.node_id
WHERE gm.canonical_source = ?
  AND (gm.status IS NULL OR gm.status NOT IN ('archived', 'superseded'))
ORDER BY gm.node_id`

	var findings []DriftFinding
	for _, c := range conflicts {
		nrows, err := s.db.Query(qNodes, c.canonicalSource)
		if err != nil {
			return nil, fmt.Errorf("findNonCanonicalCopies nodes: %w", err)
		}
		for nrows.Next() {
			var nodeID, filePath string
			if err := nrows.Scan(&nodeID, &filePath); err != nil {
				nrows.Close()
				return nil, fmt.Errorf("findNonCanonicalCopies node scan: %w", err)
			}
			findings = append(findings, DriftFinding{
				Code:     CodePolicyNonCanonical,
				NodeID:   nodeID,
				FilePath: filePath,
				Severity: "warning",
				Message:  fmt.Sprintf("Multiple documents claim canonical_source=%q (%d total)", c.canonicalSource, c.count),
				Evidence: fmt.Sprintf("canonical_source=%q", c.canonicalSource),
			})
		}
		nrows.Close()
		if err := nrows.Err(); err != nil {
			return nil, fmt.Errorf("findNonCanonicalCopies node rows: %w", err)
		}
	}
	return findings, nil
}

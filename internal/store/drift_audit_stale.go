package store

import "fmt"

// findStaleReview returns policy.stale_review findings: documents whose
// review_due date is non-empty, is strictly before opts.AsOf, and whose
// status is not in {'archived','superseded','non-binding'}.
func (s *Store) findStaleReview(opts DriftAuditOpts) ([]DriftFinding, error) {
	asOfStr := opts.AsOf.Format("2006-01-02")

	rows, err := s.db.Query(`
		SELECT n.id, n.file_path, gm.review_due, gm.status, gm.owner
		FROM governance_metadata gm
		JOIN nodes n ON n.id = gm.node_id
		WHERE gm.review_due != ''
		  AND gm.review_due < ?
		  AND gm.status NOT IN ('archived', 'superseded', 'non-binding')
		ORDER BY gm.review_due, n.id
		LIMIT ?
	`, asOfStr, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("findStaleReview query: %w", err)
	}
	defer rows.Close()

	var findings []DriftFinding
	for rows.Next() {
		var nodeID, filePath, reviewDue, status, owner string
		if err := rows.Scan(&nodeID, &filePath, &reviewDue, &status, &owner); err != nil {
			return nil, fmt.Errorf("findStaleReview scan: %w", err)
		}
		findings = append(findings, DriftFinding{
			Code:     CodePolicyStaleReview,
			NodeID:   nodeID,
			FilePath: filePath,
			Severity: "warning",
			Message:  fmt.Sprintf("Review overdue since %s (status: %s)", reviewDue, status),
			Evidence: fmt.Sprintf("owner=%s, review_due=%s", owner, reviewDue),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("findStaleReview rows: %w", err)
	}
	return findings, nil
}

// findSupersededReferenced returns policy.superseded_referenced findings:
// documents marked superseded_by that still have incoming reference edges
// (kind IN references, wikilinks_to, related_to) from non-superseded,
// non-archived source documents.
//
// The approach iterates superseded nodes and calls GetIncomingEdges for each,
// then checks whether the source document is itself superseded/archived.
// One finding is emitted per unique (superseded node, source node) pair.
func (s *Store) findSupersededReferenced(opts DriftAuditOpts) ([]DriftFinding, error) {
	// Fetch all nodes with superseded_by set.
	rows, err := s.db.Query(`
		SELECT n.id, n.file_path, gm.superseded_by
		FROM governance_metadata gm
		JOIN nodes n ON n.id = gm.node_id
		WHERE gm.superseded_by != ''
		ORDER BY n.id
	`)
	if err != nil {
		return nil, fmt.Errorf("findSupersededReferenced query: %w", err)
	}
	defer rows.Close()

	type supersededEntry struct {
		nodeID       string
		filePath     string
		supersededBy string
	}
	var superseded []supersededEntry
	for rows.Next() {
		var e supersededEntry
		if err := rows.Scan(&e.nodeID, &e.filePath, &e.supersededBy); err != nil {
			return nil, fmt.Errorf("findSupersededReferenced scan: %w", err)
		}
		superseded = append(superseded, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("findSupersededReferenced rows: %w", err)
	}

	var findings []DriftFinding
	// allowedKinds restricts to the three reference edge kinds per spec.
	allowedKinds := map[string]bool{
		"references":   true,
		"wikilinks_to": true,
		"related_to":   true,
	}

	for _, sup := range superseded {
		if len(findings) >= opts.Limit {
			break
		}

		edges, err := s.GetIncomingEdges(sup.nodeID)
		if err != nil {
			return nil, fmt.Errorf("findSupersededReferenced GetIncomingEdges(%s): %w", sup.nodeID, err)
		}

		// Deduplicate by source node ID: one finding per (superseded, source) pair.
		seen := make(map[string]bool)
		for _, e := range edges {
			if len(findings) >= opts.Limit {
				break
			}
			// Filter to the three allowed kinds only.
			if !allowedKinds[e.Kind] {
				continue
			}

			sourceID := e.Source
			if seen[sourceID] {
				continue
			}

			// Check whether the source document is itself archived or superseded.
			srcGov, err := s.GetGovernanceMetadata(sourceID)
			if err != nil {
				return nil, fmt.Errorf("findSupersededReferenced GetGovernanceMetadata(%s): %w", sourceID, err)
			}
			// nil record = no governance data = not archived/superseded → emit finding.
			if srcGov != nil && (srcGov.Status == "archived" || srcGov.Status == "superseded") {
				continue
			}

			// Resolve the source node's file path.
			srcNode, err := s.GetNodeByID(sourceID)
			if err != nil {
				return nil, fmt.Errorf("findSupersededReferenced GetNodeByID(%s): %w", sourceID, err)
			}
			srcPath := sourceID
			if srcNode != nil {
				srcPath = srcNode.FilePath
			}

			seen[sourceID] = true
			findings = append(findings, DriftFinding{
				Code:          CodePolicySupersedeReferenced,
				NodeID:        sup.nodeID,
				FilePath:      sup.filePath,
				RelatedNodeID: sourceID,
				RelatedPath:   srcPath,
				Severity:      "warning",
				Message:       fmt.Sprintf("Superseded document still referenced by %s", srcPath),
				Evidence:      fmt.Sprintf("superseded_by=%s", sup.supersededBy),
			})
		}
	}
	return findings, nil
}

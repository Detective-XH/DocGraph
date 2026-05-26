package store

import "fmt"

// findStaleAssessment returns research.stale_assessment findings: research
// documents whose valid_until date is non-empty and strictly before opts.AsOf.
func (s *Store) findStaleAssessment(opts DriftAuditOpts) ([]DriftFinding, error) {
	asOfStr := opts.AsOf.Format("2006-01-02")

	rows, err := s.db.Query(`
		SELECT n.id, n.file_path, rm.valid_until, rm.claim_id
		FROM research_metadata rm
		JOIN nodes n ON n.id = rm.node_id
		WHERE rm.valid_until != ''
		  AND rm.valid_until < ?
		ORDER BY rm.valid_until, n.id
		LIMIT ?
	`, asOfStr, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("findStaleAssessment query: %w", err)
	}
	defer rows.Close()

	var findings []DriftFinding
	for rows.Next() {
		var nodeID, filePath, validUntil, claimID string
		if err := rows.Scan(&nodeID, &filePath, &validUntil, &claimID); err != nil {
			return nil, fmt.Errorf("findStaleAssessment scan: %w", err)
		}
		msg := fmt.Sprintf("Assessment expired on %s", validUntil)
		ev := fmt.Sprintf("valid_until=%s", validUntil)
		if claimID != "" {
			msg = fmt.Sprintf("Assessment expired on %s (claim: %s)", validUntil, claimID)
			ev = fmt.Sprintf("valid_until=%s, claim_id=%s", validUntil, claimID)
		}
		findings = append(findings, DriftFinding{
			Code:     CodeResearchStaleAssessment,
			NodeID:   nodeID,
			FilePath: filePath,
			Severity: "warning",
			Message:  msg,
			Evidence: ev,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("findStaleAssessment rows: %w", err)
	}
	return findings, nil
}

// findUnverifiedEvidence returns research.unverified_evidence findings:
// research documents whose last_verified date is older than
// opts.AsOf minus opts.UnverifiedAfterDays.
func (s *Store) findUnverifiedEvidence(opts DriftAuditOpts) ([]DriftFinding, error) {
	threshold := opts.AsOf.AddDate(0, 0, -opts.UnverifiedAfterDays).Format("2006-01-02")

	rows, err := s.db.Query(`
		SELECT n.id, n.file_path, rm.last_verified, rm.claim_id
		FROM research_metadata rm
		JOIN nodes n ON n.id = rm.node_id
		WHERE rm.last_verified != ''
		  AND rm.last_verified < ?
		ORDER BY rm.last_verified, n.id
		LIMIT ?
	`, threshold, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("findUnverifiedEvidence query: %w", err)
	}
	defer rows.Close()

	var findings []DriftFinding
	for rows.Next() {
		var nodeID, filePath, lastVerified, claimID string
		if err := rows.Scan(&nodeID, &filePath, &lastVerified, &claimID); err != nil {
			return nil, fmt.Errorf("findUnverifiedEvidence scan: %w", err)
		}
		ev := fmt.Sprintf("last_verified=%s", lastVerified)
		if claimID != "" {
			ev = fmt.Sprintf("last_verified=%s, claim_id=%s", lastVerified, claimID)
		}
		findings = append(findings, DriftFinding{
			Code:     CodeResearchUnverifiedEvidence,
			NodeID:   nodeID,
			FilePath: filePath,
			Severity: "info",
			Message:  fmt.Sprintf("Evidence not verified in the last %d days (last: %s)", opts.UnverifiedAfterDays, lastVerified),
			Evidence: ev,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("findUnverifiedEvidence rows: %w", err)
	}
	return findings, nil
}

// findCompetingInterpretations returns research.competing_interpretations
// findings: claim_ids that have multiple distinct confidence or analyst_status
// values across research documents.
func (s *Store) findCompetingInterpretations(opts DriftAuditOpts) ([]DriftFinding, error) {
	rows, err := s.db.Query(`
		SELECT rm.claim_id,
		       GROUP_CONCAT(DISTINCT rm.confidence) AS confidences,
		       GROUP_CONCAT(DISTINCT rm.analyst_status) AS statuses,
		       MIN(n.id) AS node_id,
		       MIN(n.file_path) AS file_path
		FROM research_metadata rm
		JOIN nodes n ON n.id = rm.node_id
		WHERE rm.claim_id != ''
		GROUP BY rm.claim_id
		HAVING COUNT(DISTINCT rm.confidence) > 1 OR COUNT(DISTINCT rm.analyst_status) > 1
		ORDER BY rm.claim_id
		LIMIT ?
	`, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("findCompetingInterpretations query: %w", err)
	}
	defer rows.Close()

	var findings []DriftFinding
	for rows.Next() {
		var claimID, confidences, statuses, nodeID, filePath string
		if err := rows.Scan(&claimID, &confidences, &statuses, &nodeID, &filePath); err != nil {
			return nil, fmt.Errorf("findCompetingInterpretations scan: %w", err)
		}
		findings = append(findings, DriftFinding{
			Code:     CodeResearchCompetingInterpretations,
			NodeID:   nodeID,
			FilePath: filePath,
			Severity: "warning",
			Message:  fmt.Sprintf("Competing interpretations for claim %s: confidence=%s, analyst_status=%s", claimID, confidences, statuses),
			Evidence: fmt.Sprintf("claim_id=%s, confidences=%s, statuses=%s", claimID, confidences, statuses),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("findCompetingInterpretations rows: %w", err)
	}
	return findings, nil
}

// findResearchSupersededClaim returns research.superseded_claim findings:
// nodes that are both in governance_metadata (superseded_by set) and
// research_metadata, and still have incoming reference edges from active docs.
func (s *Store) findResearchSupersededClaim(opts DriftAuditOpts) ([]DriftFinding, error) {
	rows, err := s.db.Query(`
		SELECT n.id, n.file_path, gm.superseded_by
		FROM governance_metadata gm
		JOIN nodes n ON n.id = gm.node_id
		JOIN research_metadata rm ON rm.node_id = gm.node_id
		WHERE gm.superseded_by != ''
		ORDER BY n.id
	`)
	if err != nil {
		return nil, fmt.Errorf("findResearchSupersededClaim query: %w", err)
	}
	defer rows.Close()

	type entry struct {
		nodeID       string
		filePath     string
		supersededBy string
	}
	var superseded []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.nodeID, &e.filePath, &e.supersededBy); err != nil {
			return nil, fmt.Errorf("findResearchSupersededClaim scan: %w", err)
		}
		superseded = append(superseded, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("findResearchSupersededClaim rows: %w", err)
	}

	allowedKinds := map[string]bool{"references": true, "wikilinks_to": true, "related_to": true}
	var findings []DriftFinding

	for _, sup := range superseded {
		if len(findings) >= opts.Limit {
			break
		}
		edges, err := s.GetIncomingEdges(sup.nodeID)
		if err != nil {
			return nil, fmt.Errorf("findResearchSupersededClaim GetIncomingEdges(%s): %w", sup.nodeID, err)
		}
		seen := make(map[string]bool)
		for _, e := range edges {
			if len(findings) >= opts.Limit {
				break
			}
			if !allowedKinds[e.Kind] {
				continue
			}
			sourceID := e.Source
			if seen[sourceID] {
				continue
			}
			srcGov, err := s.GetGovernanceMetadata(sourceID)
			if err != nil {
				return nil, fmt.Errorf("findResearchSupersededClaim GetGovernanceMetadata(%s): %w", sourceID, err)
			}
			if srcGov != nil && (srcGov.Status == "archived" || srcGov.Status == "superseded") {
				continue
			}
			srcNode, err := s.GetNodeByID(sourceID)
			if err != nil {
				return nil, fmt.Errorf("findResearchSupersededClaim GetNodeByID(%s): %w", sourceID, err)
			}
			srcPath := sourceID
			if srcNode != nil {
				srcPath = srcNode.FilePath
			}
			seen[sourceID] = true
			findings = append(findings, DriftFinding{
				Code:          CodeResearchSupersededClaim,
				NodeID:        sup.nodeID,
				FilePath:      sup.filePath,
				RelatedNodeID: sourceID,
				RelatedPath:   srcPath,
				Severity:      "warning",
				Message:       fmt.Sprintf("Superseded research document still referenced by %s", srcPath),
				Evidence:      fmt.Sprintf("superseded_by=%s", sup.supersededBy),
			})
		}
	}
	return findings, nil
}

// findImpactedDeliverable returns research.impacted_deliverable findings:
// deliverable documents linked (bidirectionally) to research nodes whose
// valid_until date is before opts.AsOf.
func (s *Store) findImpactedDeliverable(opts DriftAuditOpts) ([]DriftFinding, error) {
	asOfStr := opts.AsOf.Format("2006-01-02")

	rows, err := s.db.Query(`
		SELECT n_d.id, n_d.file_path, rm_d.deliverable_id, n_s.id, n_s.file_path
		FROM research_metadata rm_d
		JOIN nodes n_d ON n_d.id = rm_d.node_id
		JOIN edges e ON e.source = rm_d.node_id
		  AND e.kind IN ('references', 'wikilinks_to', 'related_to')
		JOIN research_metadata rm_s ON rm_s.node_id = e.target
		JOIN nodes n_s ON n_s.id = e.target
		WHERE rm_d.deliverable_id != ''
		  AND rm_s.valid_until != ''
		  AND rm_s.valid_until < ?
		  AND e.target != rm_d.node_id

		UNION

		SELECT n_d.id, n_d.file_path, rm_d.deliverable_id, n_s.id, n_s.file_path
		FROM research_metadata rm_d
		JOIN nodes n_d ON n_d.id = rm_d.node_id
		JOIN edges e ON e.target = rm_d.node_id
		  AND e.kind IN ('references', 'wikilinks_to', 'related_to')
		JOIN research_metadata rm_s ON rm_s.node_id = e.source
		JOIN nodes n_s ON n_s.id = e.source
		WHERE rm_d.deliverable_id != ''
		  AND rm_s.valid_until != ''
		  AND rm_s.valid_until < ?
		  AND e.source != rm_d.node_id

		ORDER BY 1, 4
		LIMIT ?
	`, asOfStr, asOfStr, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("findImpactedDeliverable query: %w", err)
	}
	defer rows.Close()

	var findings []DriftFinding
	for rows.Next() {
		var delivNodeID, delivPath, deliverableID, staleNodeID, stalePath string
		if err := rows.Scan(&delivNodeID, &delivPath, &deliverableID, &staleNodeID, &stalePath); err != nil {
			return nil, fmt.Errorf("findImpactedDeliverable scan: %w", err)
		}
		findings = append(findings, DriftFinding{
			Code:          CodeResearchImpactedDeliverable,
			NodeID:        delivNodeID,
			FilePath:      delivPath,
			RelatedNodeID: staleNodeID,
			RelatedPath:   stalePath,
			Severity:      "info",
			Message:       fmt.Sprintf("Deliverable %s linked to expired assessment %s", deliverableID, stalePath),
			Evidence:      fmt.Sprintf("deliverable_id=%s, stale_path=%s", deliverableID, stalePath),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("findImpactedDeliverable rows: %w", err)
	}
	return findings, nil
}

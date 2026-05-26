package store

import "fmt"

// findDocsCodeDrift gates on IsPackEnabled("code_doc") and dispatches to three
// sub-checks. Returns (nil, nil) immediately if the code_doc pack is disabled
// or not registered.
func (s *Store) findDocsCodeDrift(opts DriftAuditOpts) ([]DriftFinding, error) {
	enabled, err := s.IsPackEnabled("code_doc")
	if err != nil {
		return nil, fmt.Errorf("findDocsCodeDrift IsPackEnabled: %w", err)
	}
	if !enabled {
		return nil, nil
	}

	var all []DriftFinding

	missing, err := s.findMissingSymbol(opts)
	if err != nil {
		return nil, err
	}
	all = append(all, missing...)

	undoc, err := s.findUndocumentedExport(opts)
	if err != nil {
		return nil, err
	}
	all = append(all, undoc...)

	unanchored, err := s.findUnanchoredFeature(opts)
	if err != nil {
		return nil, err
	}
	all = append(all, unanchored...)

	return all, nil
}

// findMissingSymbol returns code.missing_symbol findings: unresolved references
// whose reference_text ends with a known code file extension (e.g. ".go", ".py").
// These represent documentation that links to a code path that was never indexed.
func (s *Store) findMissingSymbol(opts DriftAuditOpts) ([]DriftFinding, error) {
	// Extensions mirror the set registered by internal/codedoc language extractors.
	// Cannot call codedoc.SupportedExts() here: store imports codedoc indirectly through
	// the indexer, so importing it directly would create a circular dependency.
	// When a new language extractor is added to internal/codedoc, add its extension here.
	rows, err := s.db.Query(`
		SELECT ur.from_node_id, ur.file_path, ur.reference_text, ur.reference_kind
		FROM unresolved_refs ur
		WHERE (
		    ur.reference_text LIKE '%.go'   OR ur.reference_text LIKE '%.py'
		    OR ur.reference_text LIKE '%.js'  OR ur.reference_text LIKE '%.ts'
		    OR ur.reference_text LIKE '%.rs'  OR ur.reference_text LIKE '%.c'
		    OR ur.reference_text LIKE '%.cpp' OR ur.reference_text LIKE '%.java'
		    OR ur.reference_text LIKE '%.rb'  OR ur.reference_text LIKE '%.kt'
		    OR ur.reference_text LIKE '%.cs'  OR ur.reference_text LIKE '%.swift'
		    OR ur.reference_text LIKE '%.tsx' OR ur.reference_text LIKE '%.jsx'
		)
		ORDER BY ur.file_path, ur.reference_text
		LIMIT ?
	`, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("findMissingSymbol query: %w", err)
	}
	defer rows.Close()

	var findings []DriftFinding
	for rows.Next() {
		var fromNodeID, filePath, referenceText, referenceKind string
		if err := rows.Scan(&fromNodeID, &filePath, &referenceText, &referenceKind); err != nil {
			return nil, fmt.Errorf("findMissingSymbol scan: %w", err)
		}
		findings = append(findings, DriftFinding{
			Code:     CodeCodeMissingSymbol,
			NodeID:   fromNodeID,
			FilePath: filePath,
			Severity: "warning",
			Message:  fmt.Sprintf("Doc references code path not indexed: %s", referenceText),
			Evidence: fmt.Sprintf("kind=%s, from=%s", referenceKind, fromNodeID),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("findMissingSymbol rows: %w", err)
	}
	return findings, nil
}

// findUndocumentedExport returns code.undocumented_export findings: code_file
// nodes that have no incoming reference edges (references, wikilinks_to, or
// related_to) from any non-code_file node. These represent code files that no
// documentation points to.
func (s *Store) findUndocumentedExport(opts DriftAuditOpts) ([]DriftFinding, error) {
	rows, err := s.db.Query(`
		SELECT n.id, n.file_path
		FROM nodes n
		WHERE n.kind = 'code_file'
		  AND NOT EXISTS (
		    SELECT 1 FROM edges e
		    JOIN nodes src ON src.id = e.source
		    WHERE e.target = n.id
		      AND e.kind IN ('references', 'wikilinks_to', 'related_to')
		      AND src.kind != 'code_file'
		  )
		ORDER BY n.file_path
		LIMIT ?
	`, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("findUndocumentedExport query: %w", err)
	}
	defer rows.Close()

	var findings []DriftFinding
	for rows.Next() {
		var nodeID, filePath string
		if err := rows.Scan(&nodeID, &filePath); err != nil {
			return nil, fmt.Errorf("findUndocumentedExport scan: %w", err)
		}
		findings = append(findings, DriftFinding{
			Code:     CodeCodeUndocumentedExport,
			NodeID:   nodeID,
			FilePath: filePath,
			Severity: "info",
			Message:  "Code file has no incoming documentation references",
			Evidence: fmt.Sprintf("file=%s", filePath),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("findUndocumentedExport rows: %w", err)
	}
	return findings, nil
}

// findUnanchoredFeature returns code.unanchored_feature findings: document
// nodes with governance status 'approved' or 'review' that have no outgoing
// edge pointing to a code_file node. These represent feature specs or design
// docs that are not connected to any indexed implementation.
func (s *Store) findUnanchoredFeature(opts DriftAuditOpts) ([]DriftFinding, error) {
	rows, err := s.db.Query(`
		SELECT n.id, n.file_path, gm.status
		FROM governance_metadata gm
		JOIN nodes n ON n.id = gm.node_id
		WHERE gm.status IN ('approved', 'review')
		  AND n.kind = 'document'
		  AND NOT EXISTS (
		    SELECT 1 FROM edges e
		    JOIN nodes t ON t.id = e.target
		    WHERE e.source = n.id
		      AND t.kind = 'code_file'
		      AND e.kind IN ('references', 'wikilinks_to', 'related_to')
		  )
		ORDER BY n.file_path
		LIMIT ?
	`, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("findUnanchoredFeature query: %w", err)
	}
	defer rows.Close()

	var findings []DriftFinding
	for rows.Next() {
		var nodeID, filePath, status string
		if err := rows.Scan(&nodeID, &filePath, &status); err != nil {
			return nil, fmt.Errorf("findUnanchoredFeature scan: %w", err)
		}
		findings = append(findings, DriftFinding{
			Code:     CodeCodeUnanchoredFeature,
			NodeID:   nodeID,
			FilePath: filePath,
			Severity: "info",
			Message:  fmt.Sprintf("Approved/review doc has no reference to any indexed code file (status: %s)", status),
			Evidence: fmt.Sprintf("governance_status=%s", status),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("findUnanchoredFeature rows: %w", err)
	}
	return findings, nil
}

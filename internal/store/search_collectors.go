package store

import "strings"

func (s *Store) collectExactCandidates(req searchRequest, candidates map[string]*searchCandidate) error {
	if req.Intent != SearchIntentExact && req.Intent != SearchIntentSection {
		return nil
	}
	q := strings.Trim(req.Query, `"`)
	rows, err := s.db.Query(`
		SELECT id, kind, name, qualified_name, file_path, start_line, end_line, level, metadata, body_excerpt, updated_at
		FROM nodes
		WHERE (id = ? OR file_path = ? OR qualified_name = ? OR lower(name) = lower(?))
		  AND (? = '' OR kind = ?)
		  AND (? OR NOT EXISTS (SELECT 1 FROM nodes cf WHERE cf.file_path = nodes.file_path AND cf.kind = 'code_file'))
		LIMIT ?`, q, q, q, q, req.Kind, req.Kind, req.IncludeCode, req.CandidateLimit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return err
		}
		addCandidate(candidates, n, "exact", 0).Score += 80
	}
	return rows.Err()
}

func (s *Store) collectNodeCandidates(req searchRequest, candidates map[string]*searchCandidate) error {
	if req.Short {
		return s.collectNodeLikeCandidates(req, candidates)
	}
	ftsQuery := buildFTSQuery(append(req.Terms, req.ExpandedTerms...))
	if ftsQuery == "" {
		return nil
	}
	rows, err := s.db.Query(`
		SELECT n.id, n.kind, n.name, n.qualified_name, n.file_path,
		       n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at,
		       bm25(nodes_fts, 8.0, 5.0, 2.0, 3.0) AS rank
		FROM nodes_fts
		JOIN nodes n ON n.rowid = nodes_fts.rowid
		WHERE nodes_fts MATCH ?
		  AND (? = '' OR n.kind = ?)
		  AND (? OR NOT EXISTS (SELECT 1 FROM nodes cf WHERE cf.file_path = n.file_path AND cf.kind = 'code_file'))
		ORDER BY rank
		LIMIT ?`, ftsQuery, req.Kind, req.Kind, req.IncludeCode, req.CandidateLimit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		n, rank, err := scanRankedNode(rows)
		if err != nil {
			return err
		}
		c := addCandidate(candidates, n, "node_fts", rank)
		c.Score += ftsRankBoost(rank)
	}
	return rows.Err()
}

func (s *Store) collectNodeLikeCandidates(req searchRequest, candidates map[string]*searchCandidate) error {
	pattern := "%" + escapeLike(req.Query) + "%"
	rows, err := s.db.Query(`
		SELECT id, kind, name, qualified_name, file_path, start_line, end_line, level, metadata, body_excerpt, updated_at
		FROM nodes
		WHERE (name LIKE ? ESCAPE '\' OR qualified_name LIKE ? ESCAPE '\' OR body_excerpt LIKE ? ESCAPE '\' OR metadata LIKE ? ESCAPE '\')
		  AND (? = '' OR kind = ?)
		  AND (? OR NOT EXISTS (SELECT 1 FROM nodes cf WHERE cf.file_path = nodes.file_path AND cf.kind = 'code_file'))
		ORDER BY name
		LIMIT ?`, pattern, pattern, pattern, pattern, req.Kind, req.Kind, req.IncludeCode, req.CandidateLimit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return err
		}
		addCandidate(candidates, n, "node_like", 0).Score += 4
	}
	return rows.Err()
}

func (s *Store) collectSectionCandidates(req searchRequest, candidates map[string]*searchCandidate) error {
	if req.Short {
		return s.collectSectionLikeCandidates(req, candidates)
	}
	ftsQuery := buildFTSQuery(append(req.Terms, req.ExpandedTerms...))
	if ftsQuery == "" {
		return nil
	}
	rows, err := s.db.Query(`
		SELECT n.id, n.kind, n.name, n.qualified_name, n.file_path,
		       n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at,
		       sc.heading_path, sc.text,
		       bm25(section_chunks_fts, 6.0, 1.0) AS rank
		FROM section_chunks_fts
		JOIN section_chunks sc ON sc.rowid = section_chunks_fts.rowid
		JOIN nodes n ON n.id = sc.node_id
		WHERE section_chunks_fts MATCH ?
		  AND (? = '' OR n.kind = ?)
		  AND (? OR NOT EXISTS (SELECT 1 FROM nodes cf WHERE cf.file_path = n.file_path AND cf.kind = 'code_file'))
		ORDER BY rank
		LIMIT ?`, ftsQuery, req.Kind, req.Kind, req.IncludeCode, req.CandidateLimit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		n, headingPath, text, rank, err := scanRankedSectionNode(rows)
		if err != nil {
			return err
		}
		c := addCandidate(candidates, n, "section_fts", rank)
		c.HeadingPath = headingPath
		c.SectionText = text
		c.Score += ftsRankBoost(rank)
	}
	return rows.Err()
}

func (s *Store) collectSectionLikeCandidates(req searchRequest, candidates map[string]*searchCandidate) error {
	pattern := "%" + escapeLike(req.Query) + "%"
	rows, err := s.db.Query(`
		SELECT n.id, n.kind, n.name, n.qualified_name, n.file_path,
		       n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at,
		       sc.heading_path, sc.text
		FROM section_chunks sc
		JOIN nodes n ON n.id = sc.node_id
		WHERE (sc.heading_path LIKE ? ESCAPE '\' OR sc.text LIKE ? ESCAPE '\')
		  AND (? = '' OR n.kind = ?)
		  AND (? OR NOT EXISTS (SELECT 1 FROM nodes cf WHERE cf.file_path = n.file_path AND cf.kind = 'code_file'))
		ORDER BY n.file_path, n.start_line
		LIMIT ?`, pattern, pattern, req.Kind, req.Kind, req.IncludeCode, req.CandidateLimit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		n, headingPath, text, err := scanSectionNode(rows)
		if err != nil {
			return err
		}
		c := addCandidate(candidates, n, "section_like", 0)
		c.HeadingPath = headingPath
		c.SectionText = text
		c.Score += 4
	}
	return rows.Err()
}

func (s *Store) collectTagCandidates(req searchRequest, candidates map[string]*searchCandidate) error {
	if req.Kind != "" && req.Kind != "document" {
		return nil
	}
	for _, term := range append(req.Terms, req.ExpandedTerms...) {
		rows, err := s.db.Query(`
			SELECT DISTINCT n.id, n.kind, n.name, n.qualified_name, n.file_path,
			       n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at
			FROM nodes t
			JOIN edges e ON e.target = t.id AND e.kind = 'tagged'
			JOIN nodes n ON n.id = e.source
			WHERE t.kind = 'tag'
			  AND lower(t.name) = lower(?)
			  AND (? = '' OR n.kind = ?)
			LIMIT ?`, term, req.Kind, req.Kind, req.CandidateLimit)
		if err != nil {
			return err
		}
		for rows.Next() {
			n, err := scanNode(rows)
			if err != nil {
				rows.Close()
				return err
			}
			addCandidate(candidates, n, "tag", 0).Score += 24
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}

func (s *Store) collectDefinitionContextCandidates(req searchRequest, candidates map[string]*searchCandidate) error {
	if req.Kind != "" && req.Kind != "definition" && req.Kind != "heading" && req.Kind != "document" {
		return nil
	}
	for _, term := range req.Terms {
		pattern := "%" + escapeLike(term) + "%"
		rows, err := s.db.Query(`
			SELECT DISTINCT n.id, n.kind, n.name, n.qualified_name, n.file_path,
			       n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at
			FROM nodes d
			LEFT JOIN edges e ON e.target = d.id AND e.kind = 'contains'
			JOIN nodes n ON n.id = CASE
				WHEN ? = 'definition' THEN d.id
				WHEN ? = 'document' THEN d.file_path
				WHEN e.source IS NOT NULL THEN e.source
				ELSE d.file_path
			END
			WHERE d.kind = 'definition'
			  AND lower(d.name) LIKE ? ESCAPE '\'
			  AND (? = '' OR n.kind = ?)
			LIMIT ?`, req.Kind, req.Kind, pattern, req.Kind, req.Kind, req.CandidateLimit)
		if err != nil {
			return err
		}
		for rows.Next() {
			n, err := scanNode(rows)
			if err != nil {
				rows.Close()
				return err
			}
			addCandidate(candidates, n, "definition_context", 0).Score += 18
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}

func (s *Store) collectMetadataFilteredCandidates(req searchRequest, candidates map[string]*searchCandidate) error {
	nodes, err := s.getNodesByRetrievalFilters(req)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		matches, headingPath, sectionText, err := s.nodeMatchesRetrievalQuery(req, n)
		if err != nil {
			return err
		}
		if !matches {
			continue
		}
		c := addCandidate(candidates, n, "metadata_filter", 0)
		c.Score += 16
		c.HeadingPath = headingPath
		c.SectionText = sectionText
	}
	return nil
}

func (s *Store) getNodesByRetrievalFilters(req searchRequest) ([]Node, error) {
	args := []any{}
	where := []string{"n.kind = 'document'"}
	if req.Governance.Status != "" ||
		req.Governance.Sensitivity != "" ||
		req.Governance.CanonicalSource != "" ||
		req.Governance.AllowedAudience != "" {
		where = append(where, "gm.node_id IS NOT NULL")
	}
	if req.Research.ClaimID != "" ||
		req.Research.SourceType != "" ||
		req.Research.Confidence != "" ||
		req.Research.AnalystStatus != "" {
		where = append(where, "rm.node_id IS NOT NULL")
	}
	if req.Governance.Status != "" {
		where = append(where, "lower(gm.status) = lower(?)")
		args = append(args, req.Governance.Status)
	}
	if req.Governance.Sensitivity != "" {
		where = append(where, "lower(gm.sensitivity) = lower(?)")
		args = append(args, req.Governance.Sensitivity)
	}
	if req.Governance.CanonicalSource != "" {
		where = append(where, "lower(gm.canonical_source) = lower(?)")
		args = append(args, req.Governance.CanonicalSource)
	}
	if req.Governance.AsOfDate != "" {
		where = append(where, "(gm.node_id IS NULL OR gm.effective_date = '' OR date(gm.effective_date) <= date(?))")
		args = append(args, req.Governance.AsOfDate)
		where = append(where, "(rm.node_id IS NULL OR rm.valid_until = '' OR date(rm.valid_until) >= date(?))")
		args = append(args, req.Governance.AsOfDate)
	}
	if req.Research.ClaimID != "" {
		where = append(where, "lower(rm.claim_id) = lower(?)")
		args = append(args, req.Research.ClaimID)
	}
	if req.Research.SourceType != "" {
		where = append(where, "lower(rm.source_type) = lower(?)")
		args = append(args, req.Research.SourceType)
	}
	if req.Research.Confidence != "" {
		where = append(where, "lower(rm.confidence) = lower(?)")
		args = append(args, req.Research.Confidence)
	}
	if req.Research.AnalystStatus != "" {
		where = append(where, "lower(rm.analyst_status) = lower(?)")
		args = append(args, req.Research.AnalystStatus)
	}

	q := `
		SELECT n.id, n.kind, n.name, n.qualified_name, n.file_path,
		       n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at
		FROM nodes n
		LEFT JOIN governance_metadata gm ON gm.node_id = n.id
		LEFT JOIN research_metadata rm ON rm.node_id = n.id
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY n.id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) nodeMatchesRetrievalQuery(req searchRequest, n Node) (bool, string, string, error) {
	if req.Kind != "" && n.Kind != req.Kind {
		return false, "", "", nil
	}
	text := strings.Join([]string{n.Name, n.QualifiedName, n.Metadata, n.BodyExcerpt}, "\n")
	if queryMatchesText(req, text) {
		return true, "", "", nil
	}
	chunk, ok, err := s.GetSectionChunk(n.ID)
	if err != nil {
		return false, "", "", err
	}
	if ok && queryMatchesText(req, chunk.HeadingPath+"\n"+chunk.Text) {
		return true, chunk.HeadingPath, chunk.Text, nil
	}
	return false, "", "", nil
}

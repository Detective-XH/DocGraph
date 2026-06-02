package store

import "strings"

// search_batch.go set-based loaders for the SearchWithOptions ranking phase.
//
// The ranking loop used to call loadRetrievalMetadata (3 QueryRows) and
// graphSignals (2 + len(terms) QueryRows) once PER CANDIDATE. On a saturated
// search (~320 candidates, ~34 terms after expansion) that is ~12,500 SQLite
// round-trips per search; a null-out A/B attributed ~34% of search wall time
// and ~81% of its allocations to those per-candidate queries. Each helper here
// replaces N per-candidate queries with one IN(...)/GROUP BY query.
//
// Equivalence is the entire correctness contract: every batch result must equal
// what the per-candidate reference (GetGovernanceMetadata / GetResearchMetadata /
// GetFileHistory / graphSignals) returns for the same input. The reference impls
// are retained and TestSearchBatchEquivalence asserts batch == per-candidate on a
// populated store.

// retrievalDocID is the metadata key for a candidate: a document keys on Node.ID
// (which equals its file path), a heading/definition keys on its owning doc's
// FilePath. Mirrors the docID derivation in loadRetrievalMetadata.
func retrievalDocID(n Node) string {
	if n.Kind != "document" {
		return n.FilePath
	}
	return n.ID
}

// inPlaceholders returns "?,?,…" with n placeholders (n>0; callers guard empty).
func inPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

// toArgs copies a string slice into a []any for db.Query variadic args.
func toArgs(keys []string) []any {
	args := make([]any, len(keys))
	for i, k := range keys {
		args[i] = k
	}
	return args
}

// getGovernanceMetadataBatch loads governance projections for many node IDs in
// one query. Absent IDs are simply not in the map — mirrors GetGovernanceMetadata
// returning (nil, nil) for a missing row, so a candidate with no row leaves
// c.Governance nil exactly as before.
func (s *Store) getGovernanceMetadataBatch(ids []string) (map[string]*GovernanceRecord, error) {
	out := make(map[string]*GovernanceRecord, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(`
		SELECT node_id, status, owner, approver, department,
		       effective_date, review_due, supersedes, superseded_by,
		       sensitivity, allowed_audience, canonical_source, updated_at
		FROM governance_metadata
		WHERE node_id IN (`+inPlaceholders(len(ids))+`)`, toArgs(ids)...) // #nosec G202 -- structural SQL: column names are compile-time constants and inPlaceholders(n)/constant fragments; all user values are bound via ? parameters, never interpolated
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rec GovernanceRecord
		if err := rows.Scan(
			&rec.NodeID, &rec.Status, &rec.Owner, &rec.Approver, &rec.Department,
			&rec.EffectiveDate, &rec.ReviewDue, &rec.Supersedes, &rec.SupersededBy,
			&rec.Sensitivity, &rec.AllowedAudience, &rec.CanonicalSource, &rec.UpdatedAt,
		); err != nil {
			return nil, err
		}
		r := rec
		out[rec.NodeID] = &r
	}
	return out, rows.Err()
}

// getResearchMetadataBatch loads research projections for many node IDs in one
// query. Absent IDs are not in the map (mirrors GetResearchMetadata nil,nil).
func (s *Store) getResearchMetadataBatch(ids []string) (map[string]*ResearchRecord, error) {
	out := make(map[string]*ResearchRecord, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(`
		SELECT node_id, claim_id, evidence, source_type, confidence,
		       event_date, assessment_date, last_verified, valid_until,
		       analyst_status, client, deliverable_id, updated_at
		FROM research_metadata
		WHERE node_id IN (`+inPlaceholders(len(ids))+`)`, toArgs(ids)...) // #nosec G202 -- structural SQL: column names are compile-time constants and inPlaceholders(n)/constant fragments; all user values are bound via ? parameters, never interpolated
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rec ResearchRecord
		if err := rows.Scan(
			&rec.NodeID, &rec.ClaimID, &rec.Evidence, &rec.SourceType, &rec.Confidence,
			&rec.EventDate, &rec.AssessmentDate, &rec.LastVerified, &rec.ValidUntil,
			&rec.AnalystStatus, &rec.Client, &rec.DeliverableID, &rec.UpdatedAt,
		); err != nil {
			return nil, err
		}
		r := rec
		out[rec.NodeID] = &r
	}
	return out, rows.Err()
}

// getFileHistoryBatch loads file_history rows for many paths in one query.
// Absent paths are not in the map (mirrors GetFileHistory nil,nil).
func (s *Store) getFileHistoryBatch(paths []string) (map[string]*FileHistory, error) {
	out := make(map[string]*FileHistory, len(paths))
	if len(paths) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(`
		SELECT path, commit_count, first_commit_at, last_commit_at, author_count, last_author, last_subject
		FROM file_history
		WHERE path IN (`+inPlaceholders(len(paths))+`)`, toArgs(paths)...) // #nosec G202 -- structural SQL: column names are compile-time constants and inPlaceholders(n)/constant fragments; all user values are bound via ? parameters, never interpolated
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var h FileHistory
		if err := rows.Scan(&h.Path, &h.CommitCount, &h.FirstCommitAt, &h.LastCommitAt,
			&h.AuthorCount, &h.LastAuthor, &h.LastSubject); err != nil {
			return nil, err
		}
		r := h
		out[h.Path] = &r
	}
	return out, rows.Err()
}

// graphSig is one candidate's reference/tag signal counts (the inputs to the
// graph rerank formula in applyGraphScore).
type graphSig struct {
	incoming   int
	outgoing   int
	tagMatches int
}

// graphSignalsBatch computes incoming/outgoing reference counts and tag-match
// counts for every candidate, keyed by candidate Node.ID. It is the set-based
// equivalent of calling graphSignals per candidate: documents resolve in/out via
// file_path joins and use Node.ID as the tagged-edge source; non-documents
// resolve in/out by Node.ID and use Node.FilePath as the tagged-edge source —
// the exact partitioning graphSignals applies per row.
//
// Two candidates can share a tag source (a document's ID equals its file path,
// which is also the tag source of every heading in that file), so the source→
// candidates maps hold slices and each count is fanned out to all sharers — the
// per-candidate code would have run the identical query for each.
func (s *Store) graphSignalsBatch(req searchRequest, cands []*searchCandidate) (map[string]graphSig, error) {
	sigs := make(map[string]graphSig, len(cands))
	if len(cands) == 0 {
		return sigs, nil
	}

	docCandsByPath := map[string][]*searchCandidate{}  // file_path -> document candidates
	nonDocCandsByID := map[string][]*searchCandidate{} // node id   -> non-document candidates
	tagSrcToCands := map[string][]*searchCandidate{}   // tag source -> candidates
	for _, c := range cands {
		sigs[c.Node.ID] = graphSig{}
		if c.Node.Kind == "document" {
			docCandsByPath[c.Node.FilePath] = append(docCandsByPath[c.Node.FilePath], c)
			tagSrcToCands[c.Node.ID] = append(tagSrcToCands[c.Node.ID], c)
		} else {
			nonDocCandsByID[c.Node.ID] = append(nonDocCandsByID[c.Node.ID], c)
			tagSrcToCands[c.Node.FilePath] = append(tagSrcToCands[c.Node.FilePath], c)
		}
	}

	const refKinds = "('references','wikilinks_to','related_to','embeds')"

	setIncoming := func(c *searchCandidate, n int) { sig := sigs[c.Node.ID]; sig.incoming = n; sigs[c.Node.ID] = sig }
	setOutgoing := func(c *searchCandidate, n int) { sig := sigs[c.Node.ID]; sig.outgoing = n; sigs[c.Node.ID] = sig }

	// Document candidates: in/out resolved through the target/source node's file_path.
	if len(docCandsByPath) > 0 {
		paths := mapKeys(docCandsByPath)
		in, err := s.scanGroupCounts(`SELECT t.file_path, COUNT(*) FROM edges e JOIN nodes t ON t.id = e.target
			WHERE t.file_path IN (`+inPlaceholders(len(paths))+`) AND e.kind IN `+refKinds+` GROUP BY t.file_path`, paths)
		if err != nil {
			return nil, err
		}
		out, err := s.scanGroupCounts(`SELECT src.file_path, COUNT(*) FROM edges e JOIN nodes src ON src.id = e.source
			WHERE src.file_path IN (`+inPlaceholders(len(paths))+`) AND e.kind IN `+refKinds+` GROUP BY src.file_path`, paths)
		if err != nil {
			return nil, err
		}
		for path, cs := range docCandsByPath {
			for _, c := range cs {
				setIncoming(c, in[path])
				setOutgoing(c, out[path])
			}
		}
	}

	// Non-document candidates: in/out resolved directly by node id.
	if len(nonDocCandsByID) > 0 {
		ids := mapKeys(nonDocCandsByID)
		in, err := s.scanGroupCounts(`SELECT target, COUNT(*) FROM edges
			WHERE target IN (`+inPlaceholders(len(ids))+`) AND kind IN `+refKinds+` GROUP BY target`, ids)
		if err != nil {
			return nil, err
		}
		out, err := s.scanGroupCounts(`SELECT source, COUNT(*) FROM edges
			WHERE source IN (`+inPlaceholders(len(ids))+`) AND kind IN `+refKinds+` GROUP BY source`, ids)
		if err != nil {
			return nil, err
		}
		for id, cs := range nonDocCandsByID {
			for _, c := range cs {
				setIncoming(c, in[id])
				setOutgoing(c, out[id])
			}
		}
	}

	// Tag matches: graphSignals sums COUNT(name == term) over the distinct term
	// set; since each tagged edge points at exactly one tag name, that sum equals
	// COUNT(name IN terms) — one query. Terms are lowered to match the per-term
	// lower(t.name)=lower(?) comparison.
	terms := lowerDistinct(append(append([]string{}, req.Terms...), req.ExpandedTerms...))
	if len(tagSrcToCands) > 0 && len(terms) > 0 {
		srcs := mapKeys(tagSrcToCands)
		args := make([]any, 0, len(srcs)+len(terms))
		args = append(args, toArgs(srcs)...)
		args = append(args, toArgs(terms)...)
		rows, err := s.db.Query(`SELECT e.source, COUNT(*) FROM edges e JOIN nodes t ON t.id = e.target
			WHERE e.source IN (`+inPlaceholders(len(srcs))+`)
			  AND e.kind = 'tagged' AND t.kind = 'tag'
			  AND lower(t.name) IN (`+inPlaceholders(len(terms))+`)
			GROUP BY e.source`, args...) // #nosec G202 -- structural SQL: column names are compile-time constants and inPlaceholders(n)/constant fragments; all user values are bound via ? parameters, never interpolated
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var src string
			var n int
			if err := rows.Scan(&src, &n); err != nil {
				return nil, err
			}
			for _, c := range tagSrcToCands[src] {
				sig := sigs[c.Node.ID]
				sig.tagMatches = n
				sigs[c.Node.ID] = sig
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	return sigs, nil
}

// scanGroupCounts runs a "SELECT key, COUNT(*) … GROUP BY key" query whose only
// bound args are keys, returning a key→count map. Keys absent from the result
// (count 0) are simply not in the map, so callers read map[k] as 0 by default.
func (s *Store) scanGroupCounts(query string, keys []string) (map[string]int, error) {
	out := make(map[string]int, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(query, toArgs(keys)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// mapKeys returns the keys of a string-keyed candidate-slice map.
func mapKeys(m map[string][]*searchCandidate) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// setKeys returns the keys of a string set.
func setKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// lowerDistinct lowercases and de-duplicates terms, preserving first-seen order.
func lowerDistinct(terms []string) []string {
	seen := make(map[string]bool, len(terms))
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		t = strings.ToLower(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

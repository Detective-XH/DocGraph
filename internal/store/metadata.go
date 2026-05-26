package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/Detective-XH/docgraph/internal/domainpacks"
)

// MetadataTuple is a normalized key/value pair extracted from a document.
// source must be one of: "frontmatter", "extractor", "skill_advisory", "derived".
// value_type must be one of: "string", "number", "date", "bool", "list", "ref".
// list values are JSON-encoded arrays. Confidence is only set for skill_advisory/derived.
type MetadataTuple struct {
	Key        string
	Value      string
	ValueType  string
	Source     string
	Confidence *float64
}

// GovernanceRecord is the typed projection of governance fields for a document node.
type GovernanceRecord struct {
	NodeID          string
	Status          string
	Owner           string
	Approver        string
	Department      string
	EffectiveDate   string
	ReviewDue       string
	Supersedes      string
	SupersededBy    string
	Sensitivity     string
	AllowedAudience string
	CanonicalSource string
	UpdatedAt       int64
}

// ResearchRecord is the typed projection of research provenance fields for a document node.
type ResearchRecord struct {
	NodeID         string
	ClaimID        string
	Evidence       string
	SourceType     string
	Confidence     string
	EventDate      string
	AssessmentDate string
	LastVerified   string
	ValidUntil     string
	AnalystStatus  string
	Client         string
	DeliverableID  string
	UpdatedAt      int64
}

// MetadataStats reports aggregate metadata index state for docgraph_status.
type MetadataStats struct {
	TotalDocs        int
	DocsWithMetadata int
	DocsWithResearch int
}

// valid source values (application-level enum; not SQL CHECK to allow future extension).
var validSources = map[string]bool{
	"frontmatter":    true,
	"docx_core_xml":  true, // DOCX Dublin Core metadata (peer of frontmatter)
	"html_meta":      true, // HTML <meta> tags (peer of frontmatter)
	"pdf_info":       true, // PDF Info dict (peer of frontmatter)
	"extractor":      true,
	"skill_advisory": true,
	"derived":        true,
}

// sourcePriority defines authority ordering: higher value = higher authority.
var sourcePriority = map[string]int{
	"frontmatter":    4,
	"docx_core_xml":  4, // same authority as frontmatter
	"html_meta":      4, // same authority as frontmatter
	"pdf_info":       4, // same authority as frontmatter
	"extractor":      3,
	"derived":        2,
	"skill_advisory": 1,
}

// InsertDocumentMetadata upserts normalized metadata tuples for a document node.
// It is an audit-trail insert: all (node_id, key, source) triples coexist as separate rows.
// Authority ordering is NOT enforced here — it is applied at projection time in
// UpsertGovernanceMetadata. Source enum is validated; invalid values return an error.
// Cap: at most 200 tuples (defence-in-depth; parser is the primary enforcement point).
func (s *Store) InsertDocumentMetadata(nodeID string, tuples []MetadataTuple) error {
	if len(tuples) > 200 {
		return fmt.Errorf("InsertDocumentMetadata: tuple count %d exceeds cap of 200", len(tuples))
	}
	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO document_metadata(node_id, key, value, value_type, source, confidence, updated_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(node_id, key, source) DO UPDATE SET
			value      = excluded.value,
			value_type = excluded.value_type,
			confidence = excluded.confidence,
			updated_at = excluded.updated_at
		WHERE excluded.updated_at >= document_metadata.updated_at
	`)
	if err != nil {
		return fmt.Errorf("prepare stmt: %w", err)
	}
	defer stmt.Close()

	for _, t := range tuples {
		if !validSources[t.Source] {
			return fmt.Errorf("InsertDocumentMetadata: invalid source %q", t.Source)
		}
		if _, err := stmt.Exec(nodeID, t.Key, t.Value, t.ValueType, t.Source, t.Confidence, now); err != nil {
			return fmt.Errorf("insert metadata %q/%q: %w", t.Key, t.Source, err)
		}
	}
	return tx.Commit()
}

// UpsertGovernanceMetadata projects governance keys from tuples into governance_metadata,
// applying authority ordering: highest-priority source per key wins; on ties, newer updated_at.
// Called after InsertDocumentMetadata during indexing.
func (s *Store) UpsertGovernanceMetadata(nodeID string, tuples []MetadataTuple) error {
	// Build winning value per governance key.
	type winner struct {
		value     string
		priority  int
		updatedAt int64
	}
	now := time.Now().Unix()
	projectionKeys := domainpacks.FieldColumnMap(domainpacks.PackGovernance)
	winners := make(map[string]winner, len(projectionKeys))

	for _, t := range tuples {
		col, ok := projectionKeys[t.Key]
		if !ok {
			continue
		}
		prio := sourcePriority[t.Source]
		if w, exists := winners[col]; exists {
			if prio < w.priority || (prio == w.priority && now <= w.updatedAt) {
				continue
			}
		}
		winners[col] = winner{value: t.Value, priority: prio, updatedAt: now}
	}

	if len(winners) == 0 {
		return nil
	}

	rec := &GovernanceRecord{NodeID: nodeID, UpdatedAt: now}
	if w, ok := winners["status"]; ok {
		rec.Status = w.value
	}
	if w, ok := winners["owner"]; ok {
		rec.Owner = w.value
	}
	if w, ok := winners["approver"]; ok {
		rec.Approver = w.value
	}
	if w, ok := winners["department"]; ok {
		rec.Department = w.value
	}
	if w, ok := winners["effective_date"]; ok {
		rec.EffectiveDate = w.value
	}
	if w, ok := winners["review_due"]; ok {
		rec.ReviewDue = w.value
	}
	if w, ok := winners["supersedes"]; ok {
		rec.Supersedes = w.value
	}
	if w, ok := winners["superseded_by"]; ok {
		rec.SupersededBy = w.value
	}
	if w, ok := winners["sensitivity"]; ok {
		rec.Sensitivity = w.value
	}
	if w, ok := winners["allowed_audience"]; ok {
		rec.AllowedAudience = w.value
	}
	if w, ok := winners["canonical_source"]; ok {
		rec.CanonicalSource = w.value
	}

	_, err := s.db.Exec(`
		INSERT INTO governance_metadata(
			node_id, status, owner, approver, department,
			effective_date, review_due, supersedes, superseded_by,
			sensitivity, allowed_audience, canonical_source, updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(node_id) DO UPDATE SET
			status           = excluded.status,
			owner            = excluded.owner,
			approver         = excluded.approver,
			department       = excluded.department,
			effective_date   = excluded.effective_date,
			review_due       = excluded.review_due,
			supersedes       = excluded.supersedes,
			superseded_by    = excluded.superseded_by,
			sensitivity      = excluded.sensitivity,
			allowed_audience = excluded.allowed_audience,
			canonical_source = excluded.canonical_source,
			updated_at       = excluded.updated_at
	`,
		rec.NodeID, rec.Status, rec.Owner, rec.Approver, rec.Department,
		rec.EffectiveDate, rec.ReviewDue, rec.Supersedes, rec.SupersededBy,
		rec.Sensitivity, rec.AllowedAudience, rec.CanonicalSource, rec.UpdatedAt,
	)
	return err
}

// UpsertResearchMetadata projects research provenance keys from tuples into research_metadata,
// applying the same source authority ordering as governance metadata.
func (s *Store) UpsertResearchMetadata(nodeID string, tuples []MetadataTuple) error {
	type winner struct {
		value     string
		priority  int
		updatedAt int64
	}
	now := time.Now().Unix()
	projectionKeys := domainpacks.FieldColumnMap(domainpacks.PackResearchProvenance)
	winners := make(map[string]winner, len(projectionKeys))

	for _, t := range tuples {
		col, ok := projectionKeys[t.Key]
		if !ok {
			continue
		}
		prio := sourcePriority[t.Source]
		if w, exists := winners[col]; exists {
			if prio < w.priority || (prio == w.priority && now <= w.updatedAt) {
				continue
			}
		}
		winners[col] = winner{value: t.Value, priority: prio, updatedAt: now}
	}

	if len(winners) == 0 {
		return nil
	}

	rec := &ResearchRecord{NodeID: nodeID, UpdatedAt: now}
	if w, ok := winners["claim_id"]; ok {
		rec.ClaimID = w.value
	}
	if w, ok := winners["evidence"]; ok {
		rec.Evidence = w.value
	}
	if w, ok := winners["source_type"]; ok {
		rec.SourceType = w.value
	}
	if w, ok := winners["confidence"]; ok {
		rec.Confidence = w.value
	}
	if w, ok := winners["event_date"]; ok {
		rec.EventDate = w.value
	}
	if w, ok := winners["assessment_date"]; ok {
		rec.AssessmentDate = w.value
	}
	if w, ok := winners["last_verified"]; ok {
		rec.LastVerified = w.value
	}
	if w, ok := winners["valid_until"]; ok {
		rec.ValidUntil = w.value
	}
	if w, ok := winners["analyst_status"]; ok {
		rec.AnalystStatus = w.value
	}
	if w, ok := winners["client"]; ok {
		rec.Client = w.value
	}
	if w, ok := winners["deliverable_id"]; ok {
		rec.DeliverableID = w.value
	}

	_, err := s.db.Exec(`
		INSERT INTO research_metadata(
			node_id, claim_id, evidence, source_type, confidence,
			event_date, assessment_date, last_verified, valid_until,
			analyst_status, client, deliverable_id, updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(node_id) DO UPDATE SET
			claim_id        = excluded.claim_id,
			evidence        = excluded.evidence,
			source_type     = excluded.source_type,
			confidence      = excluded.confidence,
			event_date      = excluded.event_date,
			assessment_date = excluded.assessment_date,
			last_verified   = excluded.last_verified,
			valid_until     = excluded.valid_until,
			analyst_status  = excluded.analyst_status,
			client          = excluded.client,
			deliverable_id  = excluded.deliverable_id,
			updated_at      = excluded.updated_at
	`,
		rec.NodeID, rec.ClaimID, rec.Evidence, rec.SourceType, rec.Confidence,
		rec.EventDate, rec.AssessmentDate, rec.LastVerified, rec.ValidUntil,
		rec.AnalystStatus, rec.Client, rec.DeliverableID, rec.UpdatedAt,
	)
	return err
}

// DeleteDocumentMetadataByFile removes normalized metadata and typed projections
// for nodes belonging to the given file path.
// Called before re-indexing a file to ensure a clean slate.
func (s *Store) DeleteDocumentMetadataByFile(filePath string) error {
	if _, err := s.db.Exec(`
		DELETE FROM governance_metadata
		WHERE node_id IN (SELECT id FROM nodes WHERE file_path = ?)
	`, filePath); err != nil {
		return err
	}
	if _, err := s.db.Exec(`
		DELETE FROM research_metadata
		WHERE node_id IN (SELECT id FROM nodes WHERE file_path = ?)
	`, filePath); err != nil {
		return err
	}
	_, err := s.db.Exec(`
		DELETE FROM document_metadata
		WHERE node_id IN (SELECT id FROM nodes WHERE file_path = ?)
	`, filePath)
	return err
}

// GetDocumentMetadata returns all metadata tuples for a node (all sources coexist).
func (s *Store) GetDocumentMetadata(nodeID string) ([]MetadataTuple, error) {
	rows, err := s.db.Query(`
		SELECT key, value, value_type, source, confidence
		FROM document_metadata
		WHERE node_id = ?
		ORDER BY source, key
	`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MetadataTuple
	for rows.Next() {
		var t MetadataTuple
		var conf sql.NullFloat64
		if err := rows.Scan(&t.Key, &t.Value, &t.ValueType, &t.Source, &conf); err != nil {
			return nil, err
		}
		if conf.Valid {
			v := conf.Float64
			t.Confidence = &v
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetGovernanceMetadata returns the typed governance projection for a node.
// Returns nil, nil when no governance data exists.
func (s *Store) GetGovernanceMetadata(nodeID string) (*GovernanceRecord, error) {
	row := s.db.QueryRow(`
		SELECT node_id, status, owner, approver, department,
		       effective_date, review_due, supersedes, superseded_by,
		       sensitivity, allowed_audience, canonical_source, updated_at
		FROM governance_metadata
		WHERE node_id = ?
	`, nodeID)

	var rec GovernanceRecord
	err := row.Scan(
		&rec.NodeID, &rec.Status, &rec.Owner, &rec.Approver, &rec.Department,
		&rec.EffectiveDate, &rec.ReviewDue, &rec.Supersedes, &rec.SupersededBy,
		&rec.Sensitivity, &rec.AllowedAudience, &rec.CanonicalSource, &rec.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// GetResearchMetadata returns the typed research provenance projection for a node.
// Returns nil, nil when no research data exists.
func (s *Store) GetResearchMetadata(nodeID string) (*ResearchRecord, error) {
	row := s.db.QueryRow(`
		SELECT node_id, claim_id, evidence, source_type, confidence,
		       event_date, assessment_date, last_verified, valid_until,
		       analyst_status, client, deliverable_id, updated_at
		FROM research_metadata
		WHERE node_id = ?
	`, nodeID)

	var rec ResearchRecord
	err := row.Scan(
		&rec.NodeID, &rec.ClaimID, &rec.Evidence, &rec.SourceType, &rec.Confidence,
		&rec.EventDate, &rec.AssessmentDate, &rec.LastVerified, &rec.ValidUntil,
		&rec.AnalystStatus, &rec.Client, &rec.DeliverableID, &rec.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// GetMetadataStats returns counts used by docgraph_status.
func (s *Store) GetMetadataStats() (MetadataStats, error) {
	var stats MetadataStats
	err := s.db.QueryRow(`SELECT COUNT(DISTINCT id) FROM nodes WHERE kind = 'document'`).Scan(&stats.TotalDocs)
	if err != nil {
		return stats, err
	}
	err = s.db.QueryRow(`SELECT COUNT(DISTINCT node_id) FROM document_metadata`).Scan(&stats.DocsWithMetadata)
	if err != nil {
		return stats, err
	}
	err = s.db.QueryRow(`SELECT COUNT(DISTINCT node_id) FROM research_metadata`).Scan(&stats.DocsWithResearch)
	return stats, err
}

// GetNodesByGovernance returns document nodes whose governance_metadata matches
// the given status and/or sensitivity filter. Empty string means "no filter".
// Results are ordered by node_id; limit 0 means no cap.
func (s *Store) GetNodesByGovernance(status, sensitivity string, limit int) ([]Node, error) {
	args := []interface{}{}
	where := "1=1"
	if status != "" {
		where += " AND gm.status = ?"
		args = append(args, status)
	}
	if sensitivity != "" {
		where += " AND gm.sensitivity = ?"
		args = append(args, sensitivity)
	}
	limitClause := ""
	if limit > 0 {
		limitClause = fmt.Sprintf(" LIMIT %d", limit)
	}
	q := fmt.Sprintf(`
		SELECT n.id, n.kind, n.name, n.qualified_name, n.file_path,
		       n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at
		FROM governance_metadata gm
		JOIN nodes n ON n.id = gm.node_id
		WHERE %s
		ORDER BY n.id
		%s
	`, where, limitClause)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Kind, &n.Name, &n.QualifiedName, &n.FilePath,
			&n.StartLine, &n.EndLine, &n.Level, &n.Metadata, &n.BodyExcerpt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// GetNodesByResearch returns document nodes whose research_metadata matches all
// non-empty filters. limit 0 means no cap.
func (s *Store) GetNodesByResearch(claimID, sourceType, confidence, analystStatus string, limit int) ([]Node, error) {
	args := []interface{}{}
	where := "1=1"
	if claimID != "" {
		where += " AND rm.claim_id = ?"
		args = append(args, claimID)
	}
	if sourceType != "" {
		where += " AND rm.source_type = ?"
		args = append(args, sourceType)
	}
	if confidence != "" {
		where += " AND rm.confidence = ?"
		args = append(args, confidence)
	}
	if analystStatus != "" {
		where += " AND rm.analyst_status = ?"
		args = append(args, analystStatus)
	}
	limitClause := ""
	if limit > 0 {
		limitClause = fmt.Sprintf(" LIMIT %d", limit)
	}
	q := fmt.Sprintf(`
		SELECT n.id, n.kind, n.name, n.qualified_name, n.file_path,
		       n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at
		FROM research_metadata rm
		JOIN nodes n ON n.id = rm.node_id
		WHERE %s
		ORDER BY n.id
		%s
	`, where, limitClause)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Kind, &n.Name, &n.QualifiedName, &n.FilePath,
			&n.StartLine, &n.EndLine, &n.Level, &n.Metadata, &n.BodyExcerpt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// IsGovernanceEmpty reports whether a GovernanceRecord has any non-empty fields.
func IsGovernanceEmpty(r *GovernanceRecord) bool {
	if r == nil {
		return true
	}
	return r.Status == "" && r.Owner == "" && r.Approver == "" &&
		r.Department == "" && r.EffectiveDate == "" && r.ReviewDue == "" &&
		r.Supersedes == "" && r.SupersededBy == "" && r.Sensitivity == "" &&
		r.AllowedAudience == "" && r.CanonicalSource == ""
}

// IsResearchEmpty reports whether a ResearchRecord has any non-empty fields.
func IsResearchEmpty(r *ResearchRecord) bool {
	if r == nil {
		return true
	}
	return r.ClaimID == "" && r.Evidence == "" && r.SourceType == "" &&
		r.Confidence == "" && r.EventDate == "" && r.AssessmentDate == "" &&
		r.LastVerified == "" && r.ValidUntil == "" && r.AnalystStatus == "" &&
		r.Client == "" && r.DeliverableID == ""
}

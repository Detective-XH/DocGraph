package store

import (
	"database/sql"
	"fmt"
	"time"
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

// MetadataStats reports aggregate metadata index state for docgraph_status.
type MetadataStats struct {
	TotalDocs        int
	DocsWithMetadata int
}

// valid source values (application-level enum; not SQL CHECK to allow future extension).
var validSources = map[string]bool{
	"frontmatter":   true,
	"extractor":     true,
	"skill_advisory": true,
	"derived":       true,
}

// sourcePriority defines authority ordering: higher value = higher authority.
var sourcePriority = map[string]int{
	"frontmatter":   4,
	"extractor":     3,
	"derived":       2,
	"skill_advisory": 1,
}

// governanceKeys is the set of frontmatter keys that project into governance_metadata.
var governanceKeys = map[string]string{
	"status":           "status",
	"owner":            "owner",
	"approver":         "approver",
	"department":       "department",
	"effective_date":   "effective_date",
	"review_due":       "review_due",
	"supersedes":       "supersedes",
	"superseded_by":    "superseded_by",
	"sensitivity":      "sensitivity",
	"allowed_audience": "allowed_audience",
	"canonical_source": "canonical_source",
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
		value    string
		priority int
		updatedAt int64
	}
	now := time.Now().Unix()
	winners := make(map[string]winner, len(governanceKeys))

	for _, t := range tuples {
		col, ok := governanceKeys[t.Key]
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

// DeleteDocumentMetadataByFile removes all document_metadata (and via FK cascade,
// governance_metadata) rows for nodes belonging to the given file path.
// Called before re-indexing a file to ensure a clean slate.
func (s *Store) DeleteDocumentMetadataByFile(filePath string) error {
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

// GetMetadataStats returns counts used by docgraph_status.
func (s *Store) GetMetadataStats() (MetadataStats, error) {
	var stats MetadataStats
	err := s.db.QueryRow(`SELECT COUNT(DISTINCT id) FROM nodes WHERE kind = 'document'`).Scan(&stats.TotalDocs)
	if err != nil {
		return stats, err
	}
	err = s.db.QueryRow(`SELECT COUNT(DISTINCT node_id) FROM document_metadata`).Scan(&stats.DocsWithMetadata)
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

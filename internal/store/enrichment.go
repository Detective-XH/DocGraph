package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/Detective-XH/docgraph/internal/domainpacks"
)

const (
	maxAgentMetadataTuples = 50
	maxAgentValueBytes     = 2000
	maxAgentSummaryBytes   = 4000
)

// EnrichmentCandidate is a document that can be sent to an agent for inferred
// summary and metadata generation. It only includes documents that lack
// frontmatter, because human-authored metadata remains the authoritative path.
type EnrichmentCandidate struct {
	DocID          string
	FilePath       string
	Name           string
	StartLine      int
	EndLine        int
	BodyExcerpt    string
	ContentHash    string
	HasFrontmatter bool
}

// AISummary stores the agent-authored summary for a document and the content
// hash it was derived from.
type AISummary struct {
	NodeID      string
	Summary     string
	ModelHint   string
	ContentHash string
	UpdatedAt   int64
}

// AgentEnrichment is the write payload for agent-inferred metadata. Metadata
// sources are forced to agent_inferred so the audit trail is explicit.
type AgentEnrichment struct {
	DocID       string
	Summary     string
	ModelHint   string
	ContentHash string
	Metadata    []MetadataTuple
}

// EnrichmentStats reports the state of agent-driven metadata enrichment.
type EnrichmentStats struct {
	EligibleDocs int
	EnrichedDocs int
	StaleDocs    int
}

// GetPendingEnrichments returns frontmatter-less documents whose inferred
// summary is missing or stale for the current file content hash.
func (s *Store) GetPendingEnrichments(limit int) ([]EnrichmentCandidate, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT n.id, n.file_path, n.name, n.start_line, n.end_line, n.body_excerpt,
		       COALESCE(f.content_hash, ''), COALESCE(f.has_frontmatter, 0)
		FROM nodes n
		LEFT JOIN files f ON f.path = n.file_path
		LEFT JOIN ai_summaries a ON a.node_id = n.id
		WHERE n.kind = 'document'
		  AND COALESCE(f.has_frontmatter, 0) = 0
		  AND (a.node_id IS NULL OR a.content_hash != COALESCE(f.content_hash, ''))
		ORDER BY n.file_path
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EnrichmentCandidate
	for rows.Next() {
		var c EnrichmentCandidate
		var hasFrontmatter int
		if err := rows.Scan(&c.DocID, &c.FilePath, &c.Name, &c.StartLine, &c.EndLine,
			&c.BodyExcerpt, &c.ContentHash, &hasFrontmatter); err != nil {
			return nil, err
		}
		c.HasFrontmatter = hasFrontmatter != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpsertAgentEnrichment stores a summary and normalized metadata inferred by
// an external agent. content_hash is checked before every write so stale agent
// output cannot be applied after the source document changes.
func (s *Store) UpsertAgentEnrichment(e AgentEnrichment) error {
	if e.DocID == "" {
		return fmt.Errorf("doc_id is required")
	}
	if e.ContentHash == "" {
		return fmt.Errorf("content_hash is required")
	}
	if e.Summary == "" && len(e.Metadata) == 0 {
		return fmt.Errorf("summary or metadata is required")
	}
	if len(e.Summary) > maxAgentSummaryBytes {
		return fmt.Errorf("summary exceeds %d bytes", maxAgentSummaryBytes)
	}
	if len(e.Metadata) > maxAgentMetadataTuples {
		return fmt.Errorf("metadata tuple count %d exceeds cap of %d", len(e.Metadata), maxAgentMetadataTuples)
	}

	var currentHash string
	err := s.db.QueryRow(`
		SELECT COALESCE(f.content_hash, '')
		FROM nodes n
		LEFT JOIN files f ON f.path = n.file_path
		WHERE n.id = ? AND n.kind = 'document'`, e.DocID).Scan(&currentHash)
	if err == sql.ErrNoRows {
		return fmt.Errorf("document node not found: %s", e.DocID)
	}
	if err != nil {
		return err
	}
	if currentHash != e.ContentHash {
		return fmt.Errorf("content_hash mismatch for %s: current %q, payload %q", e.DocID, currentHash, e.ContentHash)
	}

	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if e.Summary != "" {
		if _, err := tx.Exec(`
			INSERT INTO ai_summaries(node_id, summary, model_hint, content_hash, updated_at)
			VALUES(?,?,?,?,?)
			ON CONFLICT(node_id) DO UPDATE SET
				summary      = excluded.summary,
				model_hint   = excluded.model_hint,
				content_hash = excluded.content_hash,
				updated_at   = excluded.updated_at`,
			e.DocID, e.Summary, e.ModelHint, e.ContentHash, now); err != nil {
			return fmt.Errorf("upsert ai summary: %w", err)
		}
	}

	if len(e.Metadata) > 0 {
		stmt, err := tx.Prepare(`
			INSERT INTO document_metadata(node_id, key, value, value_type, source, confidence, updated_at)
			VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(node_id, key, source) DO UPDATE SET
				value      = excluded.value,
				value_type = excluded.value_type,
				confidence = excluded.confidence,
				updated_at = excluded.updated_at`)
		if err != nil {
			return fmt.Errorf("prepare metadata insert: %w", err)
		}
		defer stmt.Close()

		for _, t := range e.Metadata {
			t.Source = "agent_inferred"
			if t.Key == "" {
				return fmt.Errorf("metadata key must not be empty")
			}
			if !validSources[t.Source] {
				return fmt.Errorf("invalid metadata source %q", t.Source)
			}
			if len(t.Value) > maxAgentValueBytes {
				t.Value = t.Value[:maxAgentValueBytes]
			}
			if _, err := stmt.Exec(e.DocID, t.Key, t.Value, t.ValueType, t.Source, t.Confidence, now); err != nil {
				return fmt.Errorf("insert metadata %q: %w", t.Key, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return s.RefreshMetadataProjections(e.DocID)
}

// GetAISummary returns an agent-authored summary for a document, when present.
func (s *Store) GetAISummary(nodeID string) (*AISummary, error) {
	row := s.db.QueryRow(`
		SELECT node_id, summary, model_hint, content_hash, updated_at
		FROM ai_summaries
		WHERE node_id = ?`, nodeID)
	var out AISummary
	if err := row.Scan(&out.NodeID, &out.Summary, &out.ModelHint, &out.ContentHash, &out.UpdatedAt); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &out, nil
}

// GetEnrichmentStats returns aggregate counts for docgraph_status.
func (s *Store) GetEnrichmentStats() (EnrichmentStats, error) {
	var stats EnrichmentStats
	if err := s.db.QueryRow(`
		SELECT COUNT(*)
		FROM nodes n
		LEFT JOIN files f ON f.path = n.file_path
		WHERE n.kind = 'document' AND COALESCE(f.has_frontmatter, 0) = 0`).Scan(&stats.EligibleDocs); err != nil {
		return stats, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ai_summaries`).Scan(&stats.EnrichedDocs); err != nil {
		return stats, err
	}
	if err := s.db.QueryRow(`
		SELECT COUNT(*)
		FROM ai_summaries a
		JOIN nodes n ON n.id = a.node_id
		LEFT JOIN files f ON f.path = n.file_path
		WHERE a.content_hash != COALESCE(f.content_hash, '')`).Scan(&stats.StaleDocs); err != nil {
		return stats, err
	}
	return stats, nil
}

// RefreshMetadataProjections recomputes typed metadata from all persisted
// document_metadata rows. This is required for agent writes because inferred
// metadata arrives after indexing and must not overwrite higher-authority rows.
func (s *Store) RefreshMetadataProjections(nodeID string) error {
	if err := s.refreshGovernanceProjection(nodeID); err != nil {
		return err
	}
	return s.refreshResearchProjection(nodeID)
}

type projectionWinner struct {
	value     string
	priority  int
	updatedAt int64
}

func (s *Store) metadataProjectionWinners(nodeID, packID string) (map[string]projectionWinner, error) {
	projectionKeys := domainpacks.FieldColumnMap(packID)
	winners := make(map[string]projectionWinner, len(projectionKeys))
	if len(projectionKeys) == 0 {
		return winners, nil
	}

	rows, err := s.db.Query(`
		SELECT key, value, source, updated_at
		FROM document_metadata
		WHERE node_id = ?`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var key, value, source string
		var updatedAt int64
		if err := rows.Scan(&key, &value, &source, &updatedAt); err != nil {
			return nil, err
		}
		col, ok := projectionKeys[key]
		if !ok {
			continue
		}
		prio := sourcePriority[source]
		if w, exists := winners[col]; exists {
			if prio < w.priority || (prio == w.priority && updatedAt <= w.updatedAt) {
				continue
			}
		}
		winners[col] = projectionWinner{value: value, priority: prio, updatedAt: updatedAt}
	}
	return winners, rows.Err()
}

func (s *Store) refreshGovernanceProjection(nodeID string) error {
	winners, err := s.metadataProjectionWinners(nodeID, domainpacks.PackGovernance)
	if err != nil {
		return err
	}
	if len(winners) == 0 {
		_, err := s.db.Exec(`DELETE FROM governance_metadata WHERE node_id = ?`, nodeID)
		return err
	}
	rec := &GovernanceRecord{NodeID: nodeID, UpdatedAt: time.Now().Unix()}
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
	_, err = s.db.Exec(`
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
			updated_at       = excluded.updated_at`,
		rec.NodeID, rec.Status, rec.Owner, rec.Approver, rec.Department,
		rec.EffectiveDate, rec.ReviewDue, rec.Supersedes, rec.SupersededBy,
		rec.Sensitivity, rec.AllowedAudience, rec.CanonicalSource, rec.UpdatedAt)
	return err
}

func (s *Store) refreshResearchProjection(nodeID string) error {
	winners, err := s.metadataProjectionWinners(nodeID, domainpacks.PackResearchProvenance)
	if err != nil {
		return err
	}
	if len(winners) == 0 {
		_, err := s.db.Exec(`DELETE FROM research_metadata WHERE node_id = ?`, nodeID)
		return err
	}
	rec := &ResearchRecord{NodeID: nodeID, UpdatedAt: time.Now().Unix()}
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
	_, err = s.db.Exec(`
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
			updated_at      = excluded.updated_at`,
		rec.NodeID, rec.ClaimID, rec.Evidence, rec.SourceType, rec.Confidence,
		rec.EventDate, rec.AssessmentDate, rec.LastVerified, rec.ValidUntil,
		rec.AnalystStatus, rec.Client, rec.DeliverableID, rec.UpdatedAt)
	return err
}

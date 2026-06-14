package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
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
	ModelID     string
	AgentID     string
	RunID       string
	ContentHash string
	UpdatedAt   int64
}

// AgentEnrichment is the write payload for agent-inferred metadata. Metadata
// sources are forced to agent_inferred so the audit trail is explicit.
type AgentEnrichment struct {
	DocID       string
	Summary     string
	Provider    string
	ModelID     string
	AgentID     string
	ContentHash string
	Metadata    []MetadataTuple
}

// AgentEnrichmentRun records one external model processing event. Runs are an
// append-only provenance ledger; the active summary/metadata remains a single
// current view to avoid mixing model outputs in normal retrieval.
type AgentEnrichmentRun struct {
	RunID        string
	NodeID       string
	Provider     string
	ModelID      string
	AgentID      string
	ContentHash  string
	SummaryHash  string
	MetadataHash string
	CreatedAt    int64
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
		LEFT JOIN agent_enrichment_current c ON c.node_id = n.id
		LEFT JOIN agent_enrichment_runs r ON r.run_id = c.run_id
		WHERE n.kind = 'document'
		  AND COALESCE(f.has_frontmatter, 0) = 0
		  AND (c.node_id IS NULL OR r.content_hash != COALESCE(f.content_hash, ''))
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
	// Trim ModelID in-place before validation so the trimmed value propagates to
	// all downstream uses (runID, DB insert). validateAgentEnrichment receives the
	// already-trimmed copy.
	e.ModelID = strings.TrimSpace(e.ModelID)
	if err := validateAgentEnrichment(e); err != nil {
		return err
	}
	if err := verifyContentHash(s.db, e.DocID, e.ContentHash); err != nil {
		return err
	}

	now := time.Now().Unix()
	summaryHash := digestString(e.Summary)
	metadataHash := digestMetadata(e.Metadata)
	runID := enrichmentRunID(e.DocID, e.Provider, e.ModelID, e.AgentID, e.ContentHash, summaryHash, metadataHash, time.Now().UnixNano())
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := insertEnrichmentRunInTx(tx, e, runID, summaryHash, metadataHash, now); err != nil {
		return err
	}
	if err := upsertCurrentEnrichmentInTx(tx, e.DocID, runID, now); err != nil {
		return err
	}
	if err := upsertSummaryInTx(tx, e, now); err != nil {
		return err
	}
	if err := upsertMetadataInTx(tx, e, runID, now); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return s.RefreshMetadataProjections(e.DocID)
}

// validateAgentEnrichment returns an error if any required field in e is missing
// or out of range. Callers must trim e.ModelID before calling.
func validateAgentEnrichment(e AgentEnrichment) error {
	if e.DocID == "" {
		return fmt.Errorf("doc_id is required")
	}
	if e.ContentHash == "" {
		return fmt.Errorf("content_hash is required")
	}
	if e.ModelID == "" {
		return fmt.Errorf("model_id is required")
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
	return nil
}

// verifyContentHash checks that the stored content_hash for docID matches
// providedHash. Returns a descriptive error on mismatch or a missing document.
func verifyContentHash(db *sql.DB, docID, providedHash string) error {
	var currentHash string
	err := db.QueryRow(`
		SELECT COALESCE(f.content_hash, '')
		FROM nodes n
		LEFT JOIN files f ON f.path = n.file_path
		WHERE n.id = ? AND n.kind = 'document'`, docID).Scan(&currentHash)
	if err == sql.ErrNoRows {
		return fmt.Errorf("document node not found: %s", docID)
	}
	if err != nil {
		return err
	}
	if currentHash != providedHash {
		return fmt.Errorf("content_hash mismatch for %s: current %q, payload %q", docID, currentHash, providedHash)
	}
	return nil
}

// insertEnrichmentRunInTx appends one row to the append-only agent_enrichment_runs ledger.
func insertEnrichmentRunInTx(tx *sql.Tx, e AgentEnrichment, runID, summaryHash, metadataHash string, now int64) error {
	if _, err := tx.Exec(`
		INSERT INTO agent_enrichment_runs(
			run_id, node_id, provider, model_id, agent_id,
			content_hash, summary_hash, metadata_hash, created_at
		) VALUES(?,?,?,?,?,?,?,?,?)`,
		runID, e.DocID, e.Provider, e.ModelID, e.AgentID,
		e.ContentHash, summaryHash, metadataHash, now); err != nil {
		return fmt.Errorf("insert enrichment run: %w", err)
	}
	return nil
}

// upsertCurrentEnrichmentInTx updates the single-row current-pointer for a node
// so retrieval always returns the latest run's output.
func upsertCurrentEnrichmentInTx(tx *sql.Tx, nodeID, runID string, now int64) error {
	if _, err := tx.Exec(`
		INSERT INTO agent_enrichment_current(node_id, run_id, updated_at)
		VALUES(?,?,?)
		ON CONFLICT(node_id) DO UPDATE SET
			run_id     = excluded.run_id,
			updated_at = excluded.updated_at`,
		nodeID, runID, now); err != nil {
		return fmt.Errorf("upsert current enrichment: %w", err)
	}
	return nil
}

// upsertSummaryInTx writes the agent-authored summary into ai_summaries when one
// is present. No-ops when e.Summary is empty.
func upsertSummaryInTx(tx *sql.Tx, e AgentEnrichment, now int64) error {
	if e.Summary == "" {
		return nil
	}
	if _, err := tx.Exec(`
		INSERT INTO ai_summaries(node_id, summary, model_hint, content_hash, updated_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(node_id) DO UPDATE SET
			summary      = excluded.summary,
			model_hint   = excluded.model_hint,
			content_hash = excluded.content_hash,
			updated_at   = excluded.updated_at`,
		e.DocID, e.Summary, e.ModelID, e.ContentHash, now); err != nil {
		return fmt.Errorf("upsert ai summary: %w", err)
	}
	return nil
}

// upsertMetadataInTx writes each validated metadata tuple and its provenance
// pointer into document_metadata and agent_metadata_provenance. No-ops when
// e.Metadata is empty.
func upsertMetadataInTx(tx *sql.Tx, e AgentEnrichment, runID string, now int64) error {
	if len(e.Metadata) == 0 {
		return nil
	}
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
		if _, err := tx.Exec(`
			INSERT INTO agent_metadata_provenance(node_id, key, run_id, updated_at)
			VALUES(?,?,?,?)
			ON CONFLICT(node_id, key) DO UPDATE SET
				run_id     = excluded.run_id,
				updated_at = excluded.updated_at`,
			e.DocID, t.Key, runID, now); err != nil {
			return fmt.Errorf("upsert metadata provenance %q: %w", t.Key, err)
		}
	}
	return nil
}

// GetAISummary returns an agent-authored summary for a document, when present.
func (s *Store) GetAISummary(nodeID string) (*AISummary, error) {
	row := s.db.QueryRow(`
		SELECT a.node_id, a.summary,
		       COALESCE(r.model_id, a.model_hint),
		       COALESCE(r.agent_id, ''),
		       COALESCE(c.run_id, ''),
		       a.content_hash, a.updated_at
		FROM ai_summaries a
		LEFT JOIN agent_enrichment_current c ON c.node_id = a.node_id
		LEFT JOIN agent_enrichment_runs r ON r.run_id = c.run_id
		WHERE a.node_id = ?`, nodeID)
	var out AISummary
	if err := row.Scan(&out.NodeID, &out.Summary, &out.ModelID, &out.AgentID, &out.RunID, &out.ContentHash, &out.UpdatedAt); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &out, nil
}

// GetAgentEnrichmentRuns returns the append-only processing history for a
// document. Normal retrieval uses only agent_enrichment_current; this method is
// for audit surfaces and tests that need model lineage.
func (s *Store) GetAgentEnrichmentRuns(nodeID string) ([]AgentEnrichmentRun, error) {
	rows, err := s.db.Query(`
		SELECT run_id, node_id, provider, model_id, agent_id,
		       content_hash, summary_hash, metadata_hash, created_at
		FROM agent_enrichment_runs
		WHERE node_id = ?
		ORDER BY created_at, run_id`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentEnrichmentRun
	for rows.Next() {
		var run AgentEnrichmentRun
		if err := rows.Scan(&run.RunID, &run.NodeID, &run.Provider, &run.ModelID, &run.AgentID,
			&run.ContentHash, &run.SummaryHash, &run.MetadataHash, &run.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
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
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_enrichment_current`).Scan(&stats.EnrichedDocs); err != nil {
		return stats, err
	}
	if err := s.db.QueryRow(`
		SELECT COUNT(*)
		FROM agent_enrichment_current c
		JOIN agent_enrichment_runs r ON r.run_id = c.run_id
		JOIN nodes n ON n.id = c.node_id
		LEFT JOIN files f ON f.path = n.file_path
		WHERE r.content_hash != COALESCE(f.content_hash, '')`).Scan(&stats.StaleDocs); err != nil {
		return stats, err
	}
	return stats, nil
}

func digestString(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func digestMetadata(tuples []MetadataTuple) string {
	if len(tuples) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tuples))
	for _, t := range tuples {
		parts = append(parts, strings.Join([]string{t.Key, t.ValueType, t.Value, t.Source}, "\x00"))
	}
	sort.Strings(parts)
	return digestString(strings.Join(parts, "\x01"))
}

func enrichmentRunID(nodeID, provider, modelID, agentID, contentHash, summaryHash, metadataHash string, nonce int64) string {
	raw := strings.Join([]string{
		nodeID,
		provider,
		modelID,
		agentID,
		contentHash,
		summaryHash,
		metadataHash,
		fmt.Sprint(nonce),
	}, "\n")
	return digestString(raw)
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
	return s.writeGovernanceProjection(nodeID, winners, time.Now().Unix())
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
	return s.writeResearchProjection(nodeID, winners, time.Now().Unix())
}

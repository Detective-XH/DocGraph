package store

import (
	"database/sql"
	"sort"
	"strings"
	"time"
)

// MetadataQualityIssue describes one advisory quality finding for a document.
// Codes are stable machine-readable anchors for future audit packs; messages
// are human-facing summaries and may evolve without changing the code.
type MetadataQualityIssue struct {
	Code     string
	Severity string
	Message  string
	Penalty  int
}

// MetadataQualityRecord is an advisory score derived from explicit metadata and
// graph structure. The score is not an authority decision; it highlights review
// gaps that governance or research audit packs can inspect later.
type MetadataQualityRecord struct {
	NodeID             string
	Score              int
	Level              string
	Issues             []MetadataQualityIssue
	IncomingReferences int
	OutgoingReferences int
	AsOf               string
}

// MetadataQualityStats summarizes advisory quality across document nodes.
type MetadataQualityStats struct {
	TotalDocs    int
	AverageScore float64
	GoodDocs     int
	WarningDocs  int
	PoorDocs     int
	IssueCounts  map[string]int
}

// GetMetadataQuality evaluates one document or section against metadata
// quality signals. Section IDs are resolved to their owning document so callers
// can use this from search, node, context, or future audit workflows.
func (s *Store) GetMetadataQuality(nodeID string, asOf time.Time) (*MetadataQualityRecord, error) {
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}
	asOf = dateOnly(asOf)

	node, err := s.GetNodeByID(nodeID)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, nil
	}
	docID := node.ID
	if node.Kind != "document" && node.FilePath != "" {
		docID = node.FilePath
		var resolvedDocID string
		err := s.db.QueryRow(`SELECT id FROM nodes WHERE kind = 'document' AND file_path = ? ORDER BY start_line LIMIT 1`, node.FilePath).Scan(&resolvedDocID)
		if err == nil && resolvedDocID != "" {
			docID = resolvedDocID
		} else if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
	}

	incoming, err := s.GetIncomingEdges(docID)
	if err != nil {
		return nil, err
	}
	outgoing, err := s.GetOutgoingEdges(docID)
	if err != nil {
		return nil, err
	}
	gov, err := s.GetGovernanceMetadata(docID)
	if err != nil {
		return nil, err
	}
	research, err := s.GetResearchMetadata(docID)
	if err != nil {
		return nil, err
	}

	rec := &MetadataQualityRecord{
		NodeID:             docID,
		Score:              100,
		IncomingReferences: len(incoming),
		OutgoingReferences: len(outgoing),
		AsOf:               asOf.Format("2006-01-02"),
	}
	addIssue := func(code, severity, message string, penalty int) {
		rec.Issues = append(rec.Issues, MetadataQualityIssue{
			Code:     code,
			Severity: severity,
			Message:  message,
			Penalty:  penalty,
		})
		rec.Score -= penalty
	}

	if gov == nil || strings.TrimSpace(gov.Status) == "" {
		addIssue("missing_status", "warning", "Governance status is missing. (Narrative body 'Status:' prose does not satisfy the frontmatter governance status field — set frontmatter status: to clear this.)", 12)
	}
	if gov == nil || strings.TrimSpace(gov.Owner) == "" {
		addIssue("missing_owner", "warning", "Document owner is missing.", 10)
	}
	if gov == nil || strings.TrimSpace(gov.ReviewDue) == "" {
		addIssue("missing_review_due", "warning", "Review due date is missing.", 8)
	} else if dateBefore(gov.ReviewDue, asOf) {
		addIssue("stale_review_due", "error", "Review due date is in the past.", 16)
	}
	if gov != nil && isNonCanonicalGovernance(gov) {
		addIssue("non_canonical", "error", "Document is marked non-canonical or superseded.", 18)
	}

	if !IsResearchEmpty(research) {
		if strings.TrimSpace(research.Evidence) == "" {
			addIssue("missing_evidence", "error", "Research evidence is missing.", 14)
		}
		if strings.TrimSpace(research.ValidUntil) != "" && dateBefore(research.ValidUntil, asOf) {
			addIssue("stale_research_validity", "error", "Research valid_until date is in the past.", 16)
		}
		if strings.TrimSpace(research.LastVerified) != "" && dateBefore(research.LastVerified, asOf.AddDate(-1, 0, 0)) {
			addIssue("stale_last_verified", "warning", "Research last_verified date is older than one year.", 6)
		}
		if weakResearchCitations(research, len(outgoing)) {
			addIssue("weak_citations", "warning", "Research citations are weak or not linked from the document.", 12)
		}
	}

	if len(incoming) == 0 && len(outgoing) == 0 {
		addIssue("isolated_document", "warning", "Document has no incoming or outgoing indexed references.", 10)
	}

	if rec.Score < 0 {
		rec.Score = 0
	}
	rec.Level = metadataQualityLevel(rec.Score)
	sort.SliceStable(rec.Issues, func(i, j int) bool {
		if rec.Issues[i].Severity == rec.Issues[j].Severity {
			return rec.Issues[i].Penalty > rec.Issues[j].Penalty
		}
		return severityRank(rec.Issues[i].Severity) > severityRank(rec.Issues[j].Severity)
	})
	return rec, nil
}

// GetMetadataQualityStats evaluates all document nodes for docgraph_status.
func (s *Store) GetMetadataQualityStats(asOf time.Time) (MetadataQualityStats, error) {
	ids, err := s.GetAllDocumentIDs()
	if err != nil {
		return MetadataQualityStats{}, err
	}
	stats := MetadataQualityStats{
		TotalDocs:   len(ids),
		IssueCounts: make(map[string]int),
	}
	if len(ids) == 0 {
		return stats, nil
	}

	totalScore := 0
	for _, id := range ids {
		q, err := s.GetMetadataQuality(id, asOf)
		if err != nil {
			return stats, err
		}
		if q == nil {
			continue
		}
		totalScore += q.Score
		switch q.Level {
		case "good":
			stats.GoodDocs++
		case "warning":
			stats.WarningDocs++
		default:
			stats.PoorDocs++
		}
		for _, issue := range q.Issues {
			stats.IssueCounts[issue.Code]++
		}
	}
	stats.AverageScore = float64(totalScore) / float64(len(ids))
	return stats, nil
}

func metadataQualityLevel(score int) string {
	switch {
	case score >= 85:
		return "good"
	case score >= 65:
		return "warning"
	default:
		return "poor"
	}
}

func severityRank(severity string) int {
	switch severity {
	case "error":
		return 3
	case "warning":
		return 2
	default:
		return 1
	}
}

func isNonCanonicalGovernance(gov *GovernanceRecord) bool {
	if gov == nil {
		return false
	}
	switch normalizedSignal(gov.CanonicalSource) {
	case "false", "no", "duplicate", "non_canonical", "non-canonical", "noncanonical":
		return true
	}
	switch normalizedSignal(gov.Status) {
	case "archived", "deprecated", "retired", "superseded":
		return true
	}
	return strings.TrimSpace(gov.SupersededBy) != ""
}

func weakResearchCitations(research *ResearchRecord, outgoingCount int) bool {
	if research == nil {
		return false
	}
	if outgoingCount > 0 {
		return false
	}
	evidenceItems := splitMetadataList(research.Evidence)
	return len(evidenceItems) < 2
}

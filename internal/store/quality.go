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
	Code        string
	Severity    string
	Message     string
	Penalty     int
	Remediation string // actionable fix; "" when none applies
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

// qualityInputs bundles the DB-loaded inputs needed to evaluate one document's quality.
type qualityInputs struct {
	node     *Node
	gov      *GovernanceRecord
	research *ResearchRecord
	incoming []Edge
	outgoing []Edge
}

// loadQualityInputs fetches all data needed to evaluate quality for docID.
func (s *Store) loadQualityInputs(docID string, node *Node) (qualityInputs, error) {
	incoming, err := s.GetIncomingEdges(docID)
	if err != nil {
		return qualityInputs{}, err
	}
	outgoing, err := s.GetOutgoingEdges(docID)
	if err != nil {
		return qualityInputs{}, err
	}
	gov, err := s.GetGovernanceMetadata(docID)
	if err != nil {
		return qualityInputs{}, err
	}
	research, err := s.GetResearchMetadata(docID)
	if err != nil {
		return qualityInputs{}, err
	}
	return qualityInputs{node: node, gov: gov, research: research, incoming: incoming, outgoing: outgoing}, nil
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
	docID, err := s.resolveToDocumentID(node)
	if err != nil {
		return nil, err
	}

	qi, err := s.loadQualityInputs(docID, node)
	if err != nil {
		return nil, err
	}

	rec := &MetadataQualityRecord{
		NodeID:             docID,
		Score:              100,
		IncomingReferences: len(qi.incoming),
		OutgoingReferences: len(qi.outgoing),
		AsOf:               asOf.Format("2006-01-02"),
	}
	addIssue := func(code, severity, message string, penalty int) {
		rec.Issues = append(rec.Issues, MetadataQualityIssue{
			Code:        code,
			Severity:    severity,
			Message:     message,
			Penalty:     penalty,
			Remediation: remediationFor(code, node.FilePath),
		})
		rec.Score -= penalty
	}

	evaluateFrontmatterIssues(node, qi.gov, asOf, addIssue)
	// non_canonical runs unconditionally (not gated on isFrontmatterCapable).
	evaluateNonCanonical(qi.gov, addIssue)
	evaluateResearchIssues(qi.research, len(qi.outgoing), asOf, addIssue)
	evaluateIsolated(qi.incoming, qi.outgoing, addIssue)

	if rec.Score < 0 {
		rec.Score = 0
	}
	rec.Level = metadataQualityLevel(rec.Score)
	sort.SliceStable(rec.Issues, qualityIssueLess(rec.Issues))
	return rec, nil
}

// evaluateNonCanonical appends non_canonical issue if gov is non-canonical/superseded.
func evaluateNonCanonical(gov *GovernanceRecord, addIssue func(code, severity, message string, penalty int)) {
	if gov != nil && isNonCanonicalGovernance(gov) {
		addIssue("non_canonical", "error", "Document is marked non-canonical or superseded.", 18)
	}
}

// evaluateIsolated appends isolated_document issue when the document has no
// incoming or outgoing indexed references.
func evaluateIsolated(incoming, outgoing []Edge, addIssue func(code, severity, message string, penalty int)) {
	if len(incoming) == 0 && len(outgoing) == 0 {
		addIssue("isolated_document", "warning", "Document has no incoming or outgoing indexed references.", 10)
	}
}

// resolveToDocumentID returns the canonical document node ID for node. If node
// is already a document its own ID is returned. For non-document nodes with a
// file_path the owning document row is looked up in the DB.
func (s *Store) resolveToDocumentID(node *Node) (string, error) {
	if node.Kind == "document" || node.FilePath == "" {
		return node.ID, nil
	}
	docID := node.FilePath
	var resolvedDocID string
	err := s.db.QueryRow(`SELECT id FROM nodes WHERE kind = 'document' AND file_path = ? ORDER BY start_line LIMIT 1`, node.FilePath).Scan(&resolvedDocID)
	if err == nil && resolvedDocID != "" {
		return resolvedDocID, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	return docID, nil
}

// evaluateFrontmatterIssues appends missing/stale frontmatter governance issues
// via addIssue. Issues are suppressed for formats that cannot carry frontmatter
// (PDF/DOCX) — flagging a binary for missing status/owner/review_due is a false
// positive. The non_canonical check is NOT included here; it runs unconditionally.
func evaluateFrontmatterIssues(node *Node, gov *GovernanceRecord, asOf time.Time, addIssue func(code, severity, message string, penalty int)) {
	if !isFrontmatterCapable(node.FilePath) {
		return
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
}

// evaluateResearchIssues appends research-specific quality issues via addIssue.
// No-ops when research is nil or empty.
func evaluateResearchIssues(research *ResearchRecord, outgoingCount int, asOf time.Time, addIssue func(code, severity, message string, penalty int)) {
	if IsResearchEmpty(research) {
		return
	}
	if strings.TrimSpace(research.Evidence) == "" {
		addIssue("missing_evidence", "error", "Research evidence is missing.", 14)
	}
	if strings.TrimSpace(research.ValidUntil) != "" && dateBefore(research.ValidUntil, asOf) {
		addIssue("stale_research_validity", "error", "Research valid_until date is in the past.", 16)
	}
	if strings.TrimSpace(research.LastVerified) != "" && dateBefore(research.LastVerified, asOf.AddDate(-1, 0, 0)) {
		addIssue("stale_last_verified", "warning", "Research last_verified date is older than one year.", 6)
	}
	if weakResearchCitations(research, outgoingCount) {
		addIssue("weak_citations", "warning", "Research citations are weak or not linked from the document.", 12)
	}
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

// qualityIssueLess returns a sort.SliceStable comparator that orders issues by
// descending severity rank then descending penalty — highest-impact error first.
func qualityIssueLess(issues []MetadataQualityIssue) func(i, j int) bool {
	return func(i, j int) bool {
		if issues[i].Severity == issues[j].Severity {
			return issues[i].Penalty > issues[j].Penalty
		}
		return severityRank(issues[i].Severity) > severityRank(issues[j].Severity)
	}
}

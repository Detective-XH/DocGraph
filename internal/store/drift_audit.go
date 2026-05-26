package store

import "time"

// DriftFinding is an advisory finding produced by a drift audit query.
// Code values use dotted namespace prefixes (e.g. "policy.*", "research.*")
// so future packs can extend the namespace without collision.
// Messages are human-facing and may change; Code values are stable API.
type DriftFinding struct {
	Code          string // stable dotted code, e.g. "policy.stale_review"
	NodeID        string
	FilePath      string
	RelatedNodeID string // "" when not applicable
	RelatedPath   string // "" when not applicable
	Severity      string // "error" | "warning" | "info"
	Message       string
	Evidence      string // brief supporting detail
}

// DriftAuditOpts configures a drift audit query. Zero value is safe to use;
// defaults are applied inside GetDriftFindings before any sub-query runs.
type DriftAuditOpts struct {
	// SimilarityMin is the minimum similar_to edge score for duplicate and
	// conflict detection. Default 0.75. Must be in (0, 1].
	SimilarityMin float64
	// Limit caps the total findings returned across all finding types.
	// Default 100.
	Limit int
	// AsOf is the reference date for stale-review and overdue checks.
	// Default time.Now().UTC().
	AsOf time.Time
	// UnverifiedAfterDays is the age threshold in days for the
	// research.unverified_evidence check. A research document whose
	// last_verified date is older than AsOf minus this many days is flagged.
	// Default 180.
	UnverifiedAfterDays int
}

// Policy/process drift finding codes.
const (
	CodePolicyStaleReview         = "policy.stale_review"
	CodePolicySupersedeReferenced = "policy.superseded_referenced"
	CodePolicyDuplicate           = "policy.duplicate"
	CodePolicyNonCanonical        = "policy.non_canonical"
	CodePolicyConflicting         = "policy.conflicting"
)

// Research/assessment drift finding codes.
const (
	CodeResearchStaleAssessment          = "research.stale_assessment"
	CodeResearchUnverifiedEvidence       = "research.unverified_evidence"
	CodeResearchCompetingInterpretations = "research.competing_interpretations"
	CodeResearchSupersededClaim          = "research.superseded_claim"
	CodeResearchImpactedDeliverable      = "research.impacted_deliverable"
)

// GetDriftFindings runs the policy/process drift audit and returns advisory
// findings. Computation is on-demand; no schema migration is required. The
// findings are not authoritative — they highlight candidates for human review.
func (s *Store) GetDriftFindings(opts DriftAuditOpts) ([]DriftFinding, error) {
	if opts.SimilarityMin <= 0 {
		opts.SimilarityMin = 0.75
	}
	if opts.Limit <= 0 {
		opts.Limit = 100
	}
	if opts.AsOf.IsZero() {
		opts.AsOf = time.Now().UTC()
	}
	if opts.UnverifiedAfterDays <= 0 {
		opts.UnverifiedAfterDays = 180
	}

	var all []DriftFinding

	stale, err := s.findStaleReview(opts)
	if err != nil {
		return nil, err
	}
	all = append(all, stale...)

	superseded, err := s.findSupersededReferenced(opts)
	if err != nil {
		return nil, err
	}
	all = append(all, superseded...)

	dups, err := s.findDuplicatePolicies(opts)
	if err != nil {
		return nil, err
	}
	all = append(all, dups...)

	nonCanon, err := s.findNonCanonicalCopies(opts)
	if err != nil {
		return nil, err
	}
	all = append(all, nonCanon...)

	conflicts, err := s.findConflictingPolicies(opts)
	if err != nil {
		return nil, err
	}
	all = append(all, conflicts...)

	staleAssess, err := s.findStaleAssessment(opts)
	if err != nil {
		return nil, err
	}
	all = append(all, staleAssess...)

	unverified, err := s.findUnverifiedEvidence(opts)
	if err != nil {
		return nil, err
	}
	all = append(all, unverified...)

	competing, err := s.findCompetingInterpretations(opts)
	if err != nil {
		return nil, err
	}
	all = append(all, competing...)

	supersededClaim, err := s.findResearchSupersededClaim(opts)
	if err != nil {
		return nil, err
	}
	all = append(all, supersededClaim...)

	impacted, err := s.findImpactedDeliverable(opts)
	if err != nil {
		return nil, err
	}
	all = append(all, impacted...)

	if len(all) > opts.Limit {
		all = all[:opts.Limit]
	}
	return all, nil
}

// DriftAuditStats summarizes findings by code for docgraph_status and drift_audit format.
type DriftAuditStats struct {
	TotalFindings int
	BySeverity    map[string]int
	ByCode        map[string]int
}

// SummarizeDriftFindings produces a summary for status and format output.
func SummarizeDriftFindings(findings []DriftFinding) DriftAuditStats {
	s := DriftAuditStats{
		TotalFindings: len(findings),
		BySeverity:    make(map[string]int),
		ByCode:        make(map[string]int),
	}
	for _, f := range findings {
		s.BySeverity[f.Severity]++
		s.ByCode[f.Code]++
	}
	return s
}

package store

import (
	"strings"
	"time"
)

func metadataMatchesRequest(req searchRequest, c *searchCandidate) bool {
	if !matchesGovernanceRequest(req, c) {
		return false
	}
	return matchesResearchRequest(req, c)
}

// matchesGovernanceRequest checks all governance field constraints in req against c.
func matchesGovernanceRequest(req searchRequest, c *searchCandidate) bool {
	gov := c.Governance
	if !govFieldMatch(req.Governance.Status, gov, func(g *GovernanceRecord) string { return g.Status }) {
		return false
	}
	if !govFieldMatch(req.Governance.Sensitivity, gov, func(g *GovernanceRecord) string { return g.Sensitivity }) {
		return false
	}
	if !govFieldMatch(req.Governance.CanonicalSource, gov, func(g *GovernanceRecord) string { return g.CanonicalSource }) {
		return false
	}
	if req.Governance.AllowedAudience != "" && !audienceAllowed(gov, req.Governance.AllowedAudience) {
		return false
	}
	if req.Governance.AsOfDate != "" && !matchesGovernanceAsOf(gov, c.Research, req.AsOf) {
		return false
	}
	return true
}

// govFieldMatch returns false only when filter is non-empty and the record's
// field (via fieldFn) does not match. A nil record fails when filter is set.
func govFieldMatch(filter string, gov *GovernanceRecord, fieldFn func(*GovernanceRecord) string) bool {
	if filter == "" {
		return true
	}
	if gov == nil {
		return false
	}
	return equalFold(fieldFn(gov), filter)
}

// matchesGovernanceAsOf checks temporal validity for a governance as-of-date
// filter: the doc's effective_date must not be future and any research
// valid_until must not be in the past.
func matchesGovernanceAsOf(gov *GovernanceRecord, research *ResearchRecord, asOf time.Time) bool {
	if gov != nil && dateAfter(gov.EffectiveDate, asOf) {
		return false
	}
	if research != nil && dateBefore(research.ValidUntil, asOf) {
		return false
	}
	return true
}

// matchesResearchRequest checks all research field constraints in req against c.
func matchesResearchRequest(req searchRequest, c *searchCandidate) bool {
	research := c.Research
	if req.Research.ClaimID != "" && (research == nil || !equalFold(research.ClaimID, req.Research.ClaimID)) {
		return false
	}
	if req.Research.SourceType != "" && (research == nil || !equalFold(research.SourceType, req.Research.SourceType)) {
		return false
	}
	if req.Research.Confidence != "" && (research == nil || !equalFold(research.Confidence, req.Research.Confidence)) {
		return false
	}
	if req.Research.AnalystStatus != "" && (research == nil || !equalFold(research.AnalystStatus, req.Research.AnalystStatus)) {
		return false
	}
	return true
}

func governanceRetrievalScore(gov *GovernanceRecord, audience string, asOf time.Time) float64 {
	score := governanceStatusScore(gov)
	score += governanceSensitivityScore(gov)
	score += governanceCanonicalScore(gov)
	if audience != "" {
		if audienceAllowed(gov, audience) {
			score += 4
		} else {
			score -= 16
		}
	}
	if dateAfter(gov.EffectiveDate, asOf) {
		score -= 12
	} else if strings.TrimSpace(gov.EffectiveDate) != "" {
		score += 3
	}
	if dateBefore(gov.ReviewDue, asOf) {
		score -= 8
	} else if strings.TrimSpace(gov.ReviewDue) != "" {
		score += 2
	}
	if strings.TrimSpace(gov.SupersededBy) != "" {
		score -= 16
	}
	return score
}

// governanceStatusScore returns the score contribution from the gov.Status field.
func governanceStatusScore(gov *GovernanceRecord) float64 {
	switch normalizedSignal(gov.Status) {
	case "approved", "accepted", "active", "current", "final", "ratified":
		return 12
	case "draft", "proposal", "provisional", "review":
		return -4
	case "archived", "deprecated", "rejected", "retired", "superseded":
		return -14
	}
	return 0
}

// governanceSensitivityScore returns the score contribution from the gov.Sensitivity field.
func governanceSensitivityScore(gov *GovernanceRecord) float64 {
	switch normalizedSignal(gov.Sensitivity) {
	case "", "public":
		return 3
	case "internal", "team":
		return 1
	case "confidential", "restricted", "secret":
		return -8
	}
	return 0
}

// governanceCanonicalScore returns the score contribution from the gov.CanonicalSource field.
func governanceCanonicalScore(gov *GovernanceRecord) float64 {
	switch normalizedSignal(gov.CanonicalSource) {
	case "true", "yes", "canonical", "official", "primary":
		return 8
	case "false", "no", "duplicate", "non-canonical", "noncanonical":
		return -10
	default:
		if strings.TrimSpace(gov.CanonicalSource) != "" {
			return 4
		}
	}
	return 0
}

func researchRetrievalScore(research *ResearchRecord, asOf time.Time) float64 {
	score := researchConfidenceScore(research)
	score += researchSourceTypeScore(research)
	score += researchAnalystScore(research)
	if dateBefore(research.ValidUntil, asOf) {
		score -= 12
	} else if strings.TrimSpace(research.ValidUntil) != "" {
		score += 3
	}
	if lastVerifiedFresh(research.LastVerified, asOf) {
		score += 2
	} else if dateBefore(research.LastVerified, asOf.AddDate(-1, 0, 0)) {
		score -= 3
	}
	return score
}

// researchConfidenceScore returns the score contribution from research.Confidence.
func researchConfidenceScore(research *ResearchRecord) float64 {
	switch normalizedSignal(research.Confidence) {
	case "very-high", "very_high", "high":
		return 10
	case "medium", "moderate":
		return 4
	case "low", "weak":
		return -8
	}
	return 0
}

// researchSourceTypeScore returns the score contribution from research.SourceType.
func researchSourceTypeScore(research *ResearchRecord) float64 {
	switch normalizedSignal(research.SourceType) {
	case "primary", "official":
		return 6
	case "internal", "expert", "verified":
		return 4
	case "secondary":
		return 1
	case "social", "rumor", "unverified":
		return -6
	}
	return 0
}

// researchAnalystScore returns the score contribution from research.AnalystStatus.
func researchAnalystScore(research *ResearchRecord) float64 {
	switch normalizedSignal(research.AnalystStatus) {
	case "approved", "verified", "peer-reviewed", "reviewed", "final":
		return 6
	case "draft", "open", "unverified":
		return -4
	case "rejected", "superseded", "withdrawn":
		return -10
	}
	return 0
}

func queryMatchesText(req searchRequest, text string) bool {
	text = strings.ToLower(text)
	if text == "" {
		return false
	}
	if strings.Contains(text, strings.ToLower(req.Query)) {
		return true
	}
	for _, term := range req.Terms {
		if term == "" {
			continue
		}
		if !strings.Contains(text, strings.ToLower(term)) {
			return false
		}
	}
	return len(req.Terms) > 0
}

func audienceAllowed(gov *GovernanceRecord, requested string) bool {
	requested = normalizedSignal(requested)
	if requested == "" {
		return true
	}
	if gov == nil {
		return false
	}
	if normalizedSignal(gov.Sensitivity) == "public" {
		return true
	}
	audience := strings.TrimSpace(gov.AllowedAudience)
	if audience == "" {
		return false
	}
	for _, part := range splitMetadataList(audience) {
		part = normalizedSignal(part)
		if part == requested || part == "all" || part == "*" || part == "public" {
			return true
		}
	}
	return false
}

func parseSearchDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if len(value) >= len("2006-01-02") {
		value = value[:len("2006-01-02")]
	}
	t, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, false
	}
	return dateOnly(t), true
}

func dateAfter(value string, ref time.Time) bool {
	t, ok := parseSearchDate(value)
	return ok && t.After(dateOnly(ref))
}

func lastVerifiedFresh(value string, ref time.Time) bool {
	t, ok := parseSearchDate(value)
	if !ok {
		return false
	}
	return !t.Before(dateOnly(ref).AddDate(-1, 0, 0)) && !t.After(dateOnly(ref))
}

func equalFold(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

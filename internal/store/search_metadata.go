package store

import (
	"strings"
	"time"
)

func metadataMatchesRequest(req searchRequest, c *searchCandidate) bool {
	gov := c.Governance
	research := c.Research
	if req.Governance.Status != "" && (gov == nil || !equalFold(gov.Status, req.Governance.Status)) {
		return false
	}
	if req.Governance.Sensitivity != "" && (gov == nil || !equalFold(gov.Sensitivity, req.Governance.Sensitivity)) {
		return false
	}
	if req.Governance.CanonicalSource != "" && (gov == nil || !equalFold(gov.CanonicalSource, req.Governance.CanonicalSource)) {
		return false
	}
	if req.Governance.AllowedAudience != "" && !audienceAllowed(gov, req.Governance.AllowedAudience) {
		return false
	}
	if req.Governance.AsOfDate != "" {
		if gov != nil && dateAfter(gov.EffectiveDate, req.AsOf) {
			return false
		}
		if research != nil && dateBefore(research.ValidUntil, req.AsOf) {
			return false
		}
	}
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
	score := 0.0
	switch normalizedSignal(gov.Status) {
	case "approved", "accepted", "active", "current", "final", "ratified":
		score += 12
	case "draft", "proposal", "provisional", "review":
		score -= 4
	case "archived", "deprecated", "rejected", "retired", "superseded":
		score -= 14
	}
	switch normalizedSignal(gov.Sensitivity) {
	case "", "public":
		score += 3
	case "internal", "team":
		score += 1
	case "confidential", "restricted", "secret":
		score -= 8
	}
	switch normalizedSignal(gov.CanonicalSource) {
	case "true", "yes", "canonical", "official", "primary":
		score += 8
	case "false", "no", "duplicate", "non-canonical", "noncanonical":
		score -= 10
	default:
		if strings.TrimSpace(gov.CanonicalSource) != "" {
			score += 4
		}
	}
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

func researchRetrievalScore(research *ResearchRecord, asOf time.Time) float64 {
	score := 0.0
	switch normalizedSignal(research.Confidence) {
	case "very-high", "very_high", "high":
		score += 10
	case "medium", "moderate":
		score += 4
	case "low", "weak":
		score -= 8
	}
	switch normalizedSignal(research.SourceType) {
	case "primary", "official":
		score += 6
	case "internal", "expert", "verified":
		score += 4
	case "secondary":
		score += 1
	case "social", "rumor", "unverified":
		score -= 6
	}
	switch normalizedSignal(research.AnalystStatus) {
	case "approved", "verified", "peer-reviewed", "reviewed", "final":
		score += 6
	case "draft", "open", "unverified":
		score -= 4
	case "rejected", "superseded", "withdrawn":
		score -= 10
	}
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

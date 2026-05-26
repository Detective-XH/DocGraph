package store

import (
	"testing"
	"time"
)

// resTestAsOf is a fixed reference date used across all research drift tests.
var resTestAsOf = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// resTuples is a convenience builder for research MetadataTuple slices.
func resTuples(kv ...string) []MetadataTuple {
	if len(kv)%2 != 0 {
		panic("resTuples: odd number of arguments")
	}
	out := make([]MetadataTuple, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		out = append(out, MetadataTuple{
			Key:       kv[i],
			Value:     kv[i+1],
			ValueType: "string",
			Source:    "frontmatter",
		})
	}
	return out
}

// ---- findStaleAssessment ----

// TestStaleAssessment_Positive: doc with valid_until in the past → one warning finding.
func TestStaleAssessment_Positive(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "research/expired.md", "research/expired.md")

	if err := st.UpsertResearchMetadata("research/expired.md", resTuples(
		"valid_until", "2020-06-01",
		"claim_id", "claim-001",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: resTestAsOf}
	findings, err := st.findStaleAssessment(opts)
	if err != nil {
		t.Fatalf("findStaleAssessment: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Code != CodeResearchStaleAssessment {
		t.Errorf("Code = %q, want %q", f.Code, CodeResearchStaleAssessment)
	}
	if f.Severity != "warning" {
		t.Errorf("Severity = %q, want warning", f.Severity)
	}
	if f.NodeID != "research/expired.md" {
		t.Errorf("NodeID = %q, want research/expired.md", f.NodeID)
	}
	if f.Message == "" {
		t.Error("Message must not be empty")
	}
	if f.Evidence == "" {
		t.Error("Evidence must not be empty")
	}
}

// TestStaleAssessment_Negative: doc with valid_until in the future → no finding.
func TestStaleAssessment_Negative(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "research/fresh.md", "research/fresh.md")

	if err := st.UpsertResearchMetadata("research/fresh.md", resTuples(
		"valid_until", "2030-01-01",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: resTestAsOf}
	findings, err := st.findStaleAssessment(opts)
	if err != nil {
		t.Fatalf("findStaleAssessment: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for future valid_until, got %d: %+v", len(findings), findings)
	}
}

// TestStaleAssessment_NoValidUntil: doc with empty valid_until → no finding.
func TestStaleAssessment_NoValidUntil(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "research/nodate.md", "research/nodate.md")

	if err := st.UpsertResearchMetadata("research/nodate.md", resTuples(
		"claim_id", "claim-nv",
		"confidence", "high",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: resTestAsOf}
	findings, err := st.findStaleAssessment(opts)
	if err != nil {
		t.Fatalf("findStaleAssessment: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for empty valid_until, got %d: %+v", len(findings), findings)
	}
}

// ---- findUnverifiedEvidence ----

// TestUnverifiedEvidence_Positive: doc last verified beyond threshold → one info finding.
func TestUnverifiedEvidence_Positive(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "research/old-evidence.md", "research/old-evidence.md")

	// resTestAsOf = 2026-01-01; threshold with UnverifiedAfterDays=180 → 2025-07-05.
	// last_verified = 2024-01-01 is well beyond the threshold.
	if err := st.UpsertResearchMetadata("research/old-evidence.md", resTuples(
		"last_verified", "2024-01-01",
		"claim_id", "claim-uv",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: resTestAsOf, UnverifiedAfterDays: 180}
	findings, err := st.findUnverifiedEvidence(opts)
	if err != nil {
		t.Fatalf("findUnverifiedEvidence: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Code != CodeResearchUnverifiedEvidence {
		t.Errorf("Code = %q, want %q", f.Code, CodeResearchUnverifiedEvidence)
	}
	if f.Severity != "info" {
		t.Errorf("Severity = %q, want info", f.Severity)
	}
	if f.Message == "" {
		t.Error("Message must not be empty")
	}
}

// TestUnverifiedEvidence_Negative: doc recently verified → no finding.
func TestUnverifiedEvidence_Negative(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "research/recent.md", "research/recent.md")

	// last_verified = 2025-12-01, threshold = 2025-07-05 → no finding.
	if err := st.UpsertResearchMetadata("research/recent.md", resTuples(
		"last_verified", "2025-12-01",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: resTestAsOf, UnverifiedAfterDays: 180}
	findings, err := st.findUnverifiedEvidence(opts)
	if err != nil {
		t.Fatalf("findUnverifiedEvidence: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for recently verified doc, got %d: %+v", len(findings), findings)
	}
}

// TestUnverifiedEvidence_NoLastVerified: doc with empty last_verified → no finding.
func TestUnverifiedEvidence_NoLastVerified(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "research/noverify.md", "research/noverify.md")

	if err := st.UpsertResearchMetadata("research/noverify.md", resTuples(
		"claim_id", "claim-nlv",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: resTestAsOf, UnverifiedAfterDays: 180}
	findings, err := st.findUnverifiedEvidence(opts)
	if err != nil {
		t.Fatalf("findUnverifiedEvidence: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for empty last_verified, got %d: %+v", len(findings), findings)
	}
}

// ---- findCompetingInterpretations ----

// TestCompetingInterpretations_DifferentConfidence: same claim_id, different
// confidence on two docs → one warning finding.
func TestCompetingInterpretations_DifferentConfidence(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "research/ci-a.md", "research/ci-a.md")
	insertTestNode(t, st, "research/ci-b.md", "research/ci-b.md")

	if err := st.UpsertResearchMetadata("research/ci-a.md", resTuples(
		"claim_id", "claim-ci",
		"confidence", "high",
		"analyst_status", "confirmed",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata ci-a: %v", err)
	}
	if err := st.UpsertResearchMetadata("research/ci-b.md", resTuples(
		"claim_id", "claim-ci",
		"confidence", "low",
		"analyst_status", "confirmed",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata ci-b: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: resTestAsOf}
	findings, err := st.findCompetingInterpretations(opts)
	if err != nil {
		t.Fatalf("findCompetingInterpretations: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Code != CodeResearchCompetingInterpretations {
		t.Errorf("Code = %q, want %q", f.Code, CodeResearchCompetingInterpretations)
	}
	if f.Severity != "warning" {
		t.Errorf("Severity = %q, want warning", f.Severity)
	}
	if f.Evidence == "" {
		t.Error("Evidence must not be empty")
	}
}

// TestCompetingInterpretations_SameConfidence: same claim_id, same confidence
// and analyst_status → no finding.
func TestCompetingInterpretations_SameConfidence(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "research/ci-same-a.md", "research/ci-same-a.md")
	insertTestNode(t, st, "research/ci-same-b.md", "research/ci-same-b.md")

	for _, id := range []string{"research/ci-same-a.md", "research/ci-same-b.md"} {
		if err := st.UpsertResearchMetadata(id, resTuples(
			"claim_id", "claim-same",
			"confidence", "medium",
			"analyst_status", "pending",
		)); err != nil {
			t.Fatalf("UpsertResearchMetadata %s: %v", id, err)
		}
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: resTestAsOf}
	findings, err := st.findCompetingInterpretations(opts)
	if err != nil {
		t.Fatalf("findCompetingInterpretations: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for same confidence/status, got %d: %+v", len(findings), findings)
	}
}

// TestCompetingInterpretations_DifferentStatus: same claim_id, different
// analyst_status only → one finding.
func TestCompetingInterpretations_DifferentStatus(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "research/ci-st-a.md", "research/ci-st-a.md")
	insertTestNode(t, st, "research/ci-st-b.md", "research/ci-st-b.md")

	if err := st.UpsertResearchMetadata("research/ci-st-a.md", resTuples(
		"claim_id", "claim-status",
		"confidence", "high",
		"analyst_status", "confirmed",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata ci-st-a: %v", err)
	}
	if err := st.UpsertResearchMetadata("research/ci-st-b.md", resTuples(
		"claim_id", "claim-status",
		"confidence", "high",
		"analyst_status", "disputed",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata ci-st-b: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: resTestAsOf}
	findings, err := st.findCompetingInterpretations(opts)
	if err != nil {
		t.Fatalf("findCompetingInterpretations: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding for different analyst_status, got %d", len(findings))
	}
}

// ---- findResearchSupersededClaim ----

// TestResearchSupersededClaim_Positive: research doc that is superseded_by,
// referenced by an active doc → one warning finding.
func TestResearchSupersededClaim_Positive(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "research/old-claim.md", "research/old-claim.md")
	insertTestNode(t, st, "research/active-doc.md", "research/active-doc.md")

	if err := st.UpsertGovernanceMetadata("research/old-claim.md", govTuples(
		"status", "superseded",
		"superseded_by", "research/new-claim.md",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata old-claim: %v", err)
	}
	if err := st.UpsertResearchMetadata("research/old-claim.md", resTuples(
		"claim_id", "claim-sc",
		"confidence", "high",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata old-claim: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("research/active-doc.md", govTuples(
		"status", "approved",
		"owner", "alice",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata active-doc: %v", err)
	}

	if err := st.InsertEdges([]Edge{
		{Source: "research/active-doc.md", Target: "research/old-claim.md", Kind: "references"},
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: resTestAsOf}
	findings, err := st.findResearchSupersededClaim(opts)
	if err != nil {
		t.Fatalf("findResearchSupersededClaim: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Code != CodeResearchSupersededClaim {
		t.Errorf("Code = %q, want %q", f.Code, CodeResearchSupersededClaim)
	}
	if f.Severity != "warning" {
		t.Errorf("Severity = %q, want warning", f.Severity)
	}
	if f.NodeID != "research/old-claim.md" {
		t.Errorf("NodeID = %q, want research/old-claim.md", f.NodeID)
	}
	if f.RelatedNodeID != "research/active-doc.md" {
		t.Errorf("RelatedNodeID = %q, want research/active-doc.md", f.RelatedNodeID)
	}
	if f.Evidence == "" {
		t.Error("Evidence must not be empty")
	}
}

// TestResearchSupersededClaim_Negative: superseded research doc with no
// incoming refs → no finding.
func TestResearchSupersededClaim_Negative(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "research/isolated.md", "research/isolated.md")

	if err := st.UpsertGovernanceMetadata("research/isolated.md", govTuples(
		"status", "superseded",
		"superseded_by", "research/new.md",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}
	if err := st.UpsertResearchMetadata("research/isolated.md", resTuples(
		"claim_id", "claim-iso",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: resTestAsOf}
	findings, err := st.findResearchSupersededClaim(opts)
	if err != nil {
		t.Fatalf("findResearchSupersededClaim: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for no incoming refs, got %d: %+v", len(findings), findings)
	}
}

// TestResearchSupersededClaim_NoResearchMetadata: superseded by governance but
// no research_metadata → no finding (JOIN excludes it).
func TestResearchSupersededClaim_NoResearchMetadata(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "research/gov-only.md", "research/gov-only.md")
	insertTestNode(t, st, "research/referencer.md", "research/referencer.md")

	if err := st.UpsertGovernanceMetadata("research/gov-only.md", govTuples(
		"status", "superseded",
		"superseded_by", "research/new-gov.md",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}
	// No UpsertResearchMetadata — the JOIN must exclude this.
	if err := st.InsertEdges([]Edge{
		{Source: "research/referencer.md", Target: "research/gov-only.md", Kind: "references"},
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: resTestAsOf}
	findings, err := st.findResearchSupersededClaim(opts)
	if err != nil {
		t.Fatalf("findResearchSupersededClaim: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings (no research_metadata), got %d: %+v", len(findings), findings)
	}
}

// TestResearchSupersededClaim_RefFromArchivedExcluded: superseded research doc
// referenced only by an archived doc → no finding.
func TestResearchSupersededClaim_RefFromArchivedExcluded(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "research/sup-r.md", "research/sup-r.md")
	insertTestNode(t, st, "research/archived-ref.md", "research/archived-ref.md")

	if err := st.UpsertGovernanceMetadata("research/sup-r.md", govTuples(
		"status", "superseded",
		"superseded_by", "research/sup-r-new.md",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata sup-r: %v", err)
	}
	if err := st.UpsertResearchMetadata("research/sup-r.md", resTuples(
		"claim_id", "claim-rfa",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("research/archived-ref.md", govTuples(
		"status", "archived",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata archived-ref: %v", err)
	}
	if err := st.InsertEdges([]Edge{
		{Source: "research/archived-ref.md", Target: "research/sup-r.md", Kind: "wikilinks_to"},
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: resTestAsOf}
	findings, err := st.findResearchSupersededClaim(opts)
	if err != nil {
		t.Fatalf("findResearchSupersededClaim: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings (ref from archived), got %d: %+v", len(findings), findings)
	}
}

// ---- findImpactedDeliverable ----

// TestImpactedDeliverable_Positive: deliverable doc linked (outgoing edge) to
// a stale assessment → one info finding.
func TestImpactedDeliverable_Positive(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "research/deliverable.md", "research/deliverable.md")
	insertTestNode(t, st, "research/stale-assessment.md", "research/stale-assessment.md")

	if err := st.UpsertResearchMetadata("research/deliverable.md", resTuples(
		"deliverable_id", "DELIV-001",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata deliverable: %v", err)
	}
	if err := st.UpsertResearchMetadata("research/stale-assessment.md", resTuples(
		"valid_until", "2020-01-01",
		"claim_id", "claim-stale",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata stale-assessment: %v", err)
	}

	if err := st.InsertEdges([]Edge{
		{Source: "research/deliverable.md", Target: "research/stale-assessment.md", Kind: "references"},
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: resTestAsOf}
	findings, err := st.findImpactedDeliverable(opts)
	if err != nil {
		t.Fatalf("findImpactedDeliverable: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Code != CodeResearchImpactedDeliverable {
		t.Errorf("Code = %q, want %q", f.Code, CodeResearchImpactedDeliverable)
	}
	if f.Severity != "info" {
		t.Errorf("Severity = %q, want info", f.Severity)
	}
	if f.NodeID != "research/deliverable.md" {
		t.Errorf("NodeID = %q, want research/deliverable.md", f.NodeID)
	}
	if f.RelatedNodeID != "research/stale-assessment.md" {
		t.Errorf("RelatedNodeID = %q, want research/stale-assessment.md", f.RelatedNodeID)
	}
	if f.Message == "" {
		t.Error("Message must not be empty")
	}
}

// TestImpactedDeliverable_Negative: deliverable linked to a current assessment → no finding.
func TestImpactedDeliverable_Negative(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "research/deliv-neg.md", "research/deliv-neg.md")
	insertTestNode(t, st, "research/fresh-assessment.md", "research/fresh-assessment.md")

	if err := st.UpsertResearchMetadata("research/deliv-neg.md", resTuples(
		"deliverable_id", "DELIV-NEG",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata deliverable: %v", err)
	}
	if err := st.UpsertResearchMetadata("research/fresh-assessment.md", resTuples(
		"valid_until", "2030-01-01",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata fresh: %v", err)
	}

	if err := st.InsertEdges([]Edge{
		{Source: "research/deliv-neg.md", Target: "research/fresh-assessment.md", Kind: "references"},
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: resTestAsOf}
	findings, err := st.findImpactedDeliverable(opts)
	if err != nil {
		t.Fatalf("findImpactedDeliverable: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for current assessment, got %d: %+v", len(findings), findings)
	}
}

// TestImpactedDeliverable_IncomingEdge: deliverable with an *incoming* edge
// from a stale assessment → one finding (bidirectional check).
func TestImpactedDeliverable_IncomingEdge(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "research/deliv-inc.md", "research/deliv-inc.md")
	insertTestNode(t, st, "research/stale-inc.md", "research/stale-inc.md")

	if err := st.UpsertResearchMetadata("research/deliv-inc.md", resTuples(
		"deliverable_id", "DELIV-INC",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata deliverable: %v", err)
	}
	if err := st.UpsertResearchMetadata("research/stale-inc.md", resTuples(
		"valid_until", "2019-06-01",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata stale: %v", err)
	}

	// Edge goes FROM stale TO deliverable (incoming for deliverable).
	if err := st.InsertEdges([]Edge{
		{Source: "research/stale-inc.md", Target: "research/deliv-inc.md", Kind: "related_to"},
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: resTestAsOf}
	findings, err := st.findImpactedDeliverable(opts)
	if err != nil {
		t.Fatalf("findImpactedDeliverable: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding (incoming edge), got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.NodeID != "research/deliv-inc.md" {
		t.Errorf("NodeID = %q, want research/deliv-inc.md", f.NodeID)
	}
	if f.RelatedNodeID != "research/stale-inc.md" {
		t.Errorf("RelatedNodeID = %q, want research/stale-inc.md", f.RelatedNodeID)
	}
}

// ---- GetDriftFindings integration smoke test for research codes ----

// TestGetDriftFindings_ResearchIntegration: smoke test that GetDriftFindings
// routes through all 5 research sub-finders without error.
func TestGetDriftFindings_ResearchIntegration(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "research/integ.md", "research/integ.md")

	if err := st.UpsertResearchMetadata("research/integ.md", resTuples(
		"valid_until", "2020-01-01",
		"claim_id", "claim-integ",
	)); err != nil {
		t.Fatalf("UpsertResearchMetadata: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: resTestAsOf, UnverifiedAfterDays: 180}
	findings, err := st.GetDriftFindings(opts)
	if err != nil {
		t.Fatalf("GetDriftFindings: %v", err)
	}

	var count int
	for _, f := range findings {
		if f.Code == CodeResearchStaleAssessment {
			count++
		}
	}
	if count < 1 {
		t.Errorf("expected at least 1 stale_assessment finding, got %d total findings", len(findings))
	}
}

// Suppress unused import warning for time package.
var _ = time.UTC

package store

import (
	"strings"
	"testing"
	"time"
)

// similarityEdgeMeta returns a JSON metadata string for a similar_to edge.
func similarityEdgeMeta(score float64) string {
	// Keep it minimal — only the score field is needed by the conflict queries.
	return strings.TrimSpace(`{"score":` + formatFloat(score) + `}`)
}

// formatFloat renders a float64 for JSON embedding without importing fmt.
func formatFloat(f float64) string {
	// strconv is not imported; use a helper that handles the small set of
	// values used in tests (0.50, 0.80, etc.).
	switch f {
	case 0.50:
		return "0.5"
	case 0.80:
		return "0.8"
	default:
		// Generic fallback: convert via fmt-free approach.
		// For test values this is always a small decimal.
		v := int(f * 100)
		whole := v / 100
		frac := v % 100
		if frac == 0 {
			return itoa(whole) + ".0"
		}
		if frac < 10 {
			return itoa(whole) + ".0" + itoa(frac)
		}
		return itoa(whole) + "." + itoa(frac)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// approvedOpts returns DriftAuditOpts with a 0.75 similarity threshold so tests
// calling findConflictingPolicies directly get predictable defaults.
func approvedOpts() DriftAuditOpts {
	return DriftAuditOpts{
		SimilarityMin: 0.75,
		Limit:         100,
		AsOf:          time.Now().UTC(),
	}
}

// insertSimilarityEdge inserts a similar_to edge with a given score as metadata.
func insertSimilarityEdge(t *testing.T, st *Store, src, tgt string, score float64) {
	t.Helper()
	meta := similarityEdgeMeta(score)
	if err := st.InsertEdges([]Edge{{Source: src, Target: tgt, Kind: "similar_to", Metadata: meta}}); err != nil {
		t.Fatalf("InsertEdges similar_to %s->%s: %v", src, tgt, err)
	}
}

// upsertApproved sets status=approved in governance_metadata for nodeID.
func upsertApproved(t *testing.T, st *Store, nodeID string) {
	t.Helper()
	if err := st.UpsertGovernanceMetadata(nodeID, []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata approved %s: %v", nodeID, err)
	}
}

// ---- Signal 1: conflictingByAuthority ----

// TestConflictSignal1Positive: two approved docs with a similar_to edge above
// the threshold → expect a policy.conflicting finding.
func TestConflictSignal1Positive(t *testing.T) {
	st := tempStore(t)
	insertTestNode(t, st, "pol-a.md", "pol-a.md")
	insertTestNode(t, st, "pol-b.md", "pol-b.md")
	upsertApproved(t, st, "pol-a.md")
	upsertApproved(t, st, "pol-b.md")
	// Edge direction must be source < target for the query to pick it up.
	insertSimilarityEdge(t, st, "pol-a.md", "pol-b.md", 0.80)

	findings, err := st.conflictingByAuthority(approvedOpts())
	if err != nil {
		t.Fatalf("conflictingByAuthority: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected at least one policy.conflicting finding, got none")
	}
	for _, f := range findings {
		if f.Code != CodePolicyConflicting {
			t.Errorf("expected code %q, got %q", CodePolicyConflicting, f.Code)
		}
		if f.Severity != "error" {
			t.Errorf("expected severity=error, got %q", f.Severity)
		}
	}
}

// TestConflictSignal1NegativeDraft: one approved + one draft → no conflict from
// conflictingByAuthority (draft docs are not competing authorities).
func TestConflictSignal1NegativeDraft(t *testing.T) {
	st := tempStore(t)
	insertTestNode(t, st, "pol-a.md", "pol-a.md")
	insertTestNode(t, st, "pol-b.md", "pol-b.md")
	upsertApproved(t, st, "pol-a.md")
	if err := st.UpsertGovernanceMetadata("pol-b.md", []MetadataTuple{
		{Key: "status", Value: "draft", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata draft: %v", err)
	}
	insertSimilarityEdge(t, st, "pol-a.md", "pol-b.md", 0.80)

	findings, err := st.conflictingByAuthority(approvedOpts())
	if err != nil {
		t.Fatalf("conflictingByAuthority: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings for approved+draft pair, got %d", len(findings))
	}
}

// TestConflictSignal1NegativeLowScore: both approved but similarity score
// below the threshold (0.50 < 0.75) → no conflict.
func TestConflictSignal1NegativeLowScore(t *testing.T) {
	st := tempStore(t)
	insertTestNode(t, st, "pol-a.md", "pol-a.md")
	insertTestNode(t, st, "pol-b.md", "pol-b.md")
	upsertApproved(t, st, "pol-a.md")
	upsertApproved(t, st, "pol-b.md")
	insertSimilarityEdge(t, st, "pol-a.md", "pol-b.md", 0.50)

	findings, err := st.conflictingByAuthority(approvedOpts())
	if err != nil {
		t.Fatalf("conflictingByAuthority: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings for low score pair, got %d", len(findings))
	}
}

// ---- Signal 2: conflictingByCanonicalSource ----

// TestConflictSignal2Positive: two approved docs with a similar_to edge above
// the threshold and different canonical_source values → expect a
// policy.conflicting finding with "canonical_source" in evidence.
func TestConflictSignal2Positive(t *testing.T) {
	st := tempStore(t)
	insertTestNode(t, st, "pol-a.md", "pol-a.md")
	insertTestNode(t, st, "pol-b.md", "pol-b.md")
	if err := st.UpsertGovernanceMetadata("pol-a.md", []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "canonical_source", Value: "policy-a", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata pol-a: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("pol-b.md", []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "canonical_source", Value: "policy-b", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata pol-b: %v", err)
	}
	insertSimilarityEdge(t, st, "pol-a.md", "pol-b.md", 0.80)

	findings, err := st.conflictingByCanonicalSource(approvedOpts())
	if err != nil {
		t.Fatalf("conflictingByCanonicalSource: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected at least one policy.conflicting finding, got none")
	}
	found := false
	for _, f := range findings {
		if f.Code == CodePolicyConflicting && strings.Contains(f.Evidence, "vs") {
			found = true
			if f.Severity != "error" {
				t.Errorf("expected severity=error, got %q", f.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected finding with canonical_source evidence, got %+v", findings)
	}
}

// TestConflictSignal2NegativeSameCanonical: both approved, high similarity, but
// same canonical_source value → conflictingByCanonicalSource must not fire.
// (Signal 1 may fire; we verify Signal 2 specifically.)
func TestConflictSignal2NegativeSameCanonical(t *testing.T) {
	st := tempStore(t)
	insertTestNode(t, st, "pol-a.md", "pol-a.md")
	insertTestNode(t, st, "pol-b.md", "pol-b.md")
	if err := st.UpsertGovernanceMetadata("pol-a.md", []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "canonical_source", Value: "policy-shared", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata pol-a: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("pol-b.md", []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "canonical_source", Value: "policy-shared", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata pol-b: %v", err)
	}
	insertSimilarityEdge(t, st, "pol-a.md", "pol-b.md", 0.80)

	// conflictingByCanonicalSource specifically must return zero findings when
	// the canonical_source values are identical.
	findings, err := st.conflictingByCanonicalSource(approvedOpts())
	if err != nil {
		t.Fatalf("conflictingByCanonicalSource: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no signal-2 findings for same canonical_source, got %d", len(findings))
	}
}

// ---- Signal 3: conflictingBySupersedes ----

// TestConflictSignal3Positive: two non-archived docs both with
// supersedes="old-policy-v1" → expect a policy.conflicting finding for each.
func TestConflictSignal3Positive(t *testing.T) {
	st := tempStore(t)
	insertTestNode(t, st, "new-a.md", "new-a.md")
	insertTestNode(t, st, "new-b.md", "new-b.md")
	if err := st.UpsertGovernanceMetadata("new-a.md", []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "supersedes", Value: "old-policy-v1", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata new-a: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("new-b.md", []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "supersedes", Value: "old-policy-v1", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata new-b: %v", err)
	}

	findings, err := st.conflictingBySupersedes(approvedOpts())
	if err != nil {
		t.Fatalf("conflictingBySupersedes: %v", err)
	}
	if len(findings) < 2 {
		t.Fatalf("expected 2 findings (one per node), got %d: %+v", len(findings), findings)
	}
	for _, f := range findings {
		if f.Code != CodePolicyConflicting {
			t.Errorf("expected code %q, got %q", CodePolicyConflicting, f.Code)
		}
		if f.Severity != "error" {
			t.Errorf("expected severity=error, got %q", f.Severity)
		}
		if !strings.Contains(f.Message, "old-policy-v1") {
			t.Errorf("expected message to contain supersedes target, got %q", f.Message)
		}
		if !strings.Contains(f.Evidence, "supersedes=") {
			t.Errorf("expected evidence to contain supersedes=, got %q", f.Evidence)
		}
	}
}

// TestConflictSignal3NegativeSingleSupersedes: only one active doc with a given
// supersedes value → no conflict.
func TestConflictSignal3NegativeSingleSupersedes(t *testing.T) {
	st := tempStore(t)
	insertTestNode(t, st, "new-a.md", "new-a.md")
	if err := st.UpsertGovernanceMetadata("new-a.md", []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "supersedes", Value: "old-policy-v1", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata new-a: %v", err)
	}

	findings, err := st.conflictingBySupersedes(approvedOpts())
	if err != nil {
		t.Fatalf("conflictingBySupersedes: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings for single supersedes claimant, got %d", len(findings))
	}
}

// ---- Negative case: archived document ----

// TestConflictNegativeArchived: two high-similarity docs where one is archived →
// no conflict from either authority signal, because archived docs are excluded.
func TestConflictNegativeArchived(t *testing.T) {
	st := tempStore(t)
	insertTestNode(t, st, "pol-a.md", "pol-a.md")
	insertTestNode(t, st, "pol-b.md", "pol-b.md")
	upsertApproved(t, st, "pol-a.md")
	if err := st.UpsertGovernanceMetadata("pol-b.md", []MetadataTuple{
		{Key: "status", Value: "archived", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata archived: %v", err)
	}
	insertSimilarityEdge(t, st, "pol-a.md", "pol-b.md", 0.80)

	sig1, err := st.conflictingByAuthority(approvedOpts())
	if err != nil {
		t.Fatalf("conflictingByAuthority: %v", err)
	}
	if len(sig1) != 0 {
		t.Fatalf("expected no signal-1 findings when one doc is archived, got %d", len(sig1))
	}

	sig2, err := st.conflictingByCanonicalSource(approvedOpts())
	if err != nil {
		t.Fatalf("conflictingByCanonicalSource: %v", err)
	}
	if len(sig2) != 0 {
		t.Fatalf("expected no signal-2 findings when one doc is archived, got %d", len(sig2))
	}
}

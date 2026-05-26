package store

import (
	"testing"
	"time"
)

func TestMetadataQualityDetectsGovernanceGaps(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "policy.md", "policy.md")

	if err := st.UpsertGovernanceMetadata("policy.md", []MetadataTuple{
		{Key: "status", Value: "superseded", ValueType: "string", Source: "frontmatter"},
		{Key: "review_due", Value: "2025-01-01", ValueType: "date", Source: "frontmatter"},
		{Key: "canonical_source", Value: "false", ValueType: "bool", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}

	asOf := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	quality, err := st.GetMetadataQuality("policy.md", asOf)
	if err != nil {
		t.Fatalf("GetMetadataQuality: %v", err)
	}
	if quality == nil {
		t.Fatal("expected quality record")
	}
	wantCodes := []string{"missing_owner", "stale_review_due", "non_canonical", "isolated_document"}
	for _, code := range wantCodes {
		if !qualityHasIssue(quality, code) {
			t.Fatalf("expected issue %q in %+v", code, quality.Issues)
		}
	}
	if quality.Score >= 85 {
		t.Fatalf("expected score below good threshold, got %d", quality.Score)
	}
}

func TestMetadataQualityResearchCitations(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "research.md", "research.md")
	insertTestNode(t, st, "source.md", "source.md")

	if err := st.UpsertGovernanceMetadata("research.md", []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "owner", Value: "analyst", ValueType: "string", Source: "frontmatter"},
		{Key: "review_due", Value: "2027-01-01", ValueType: "date", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}
	if err := st.UpsertResearchMetadata("research.md", []MetadataTuple{
		{Key: "claim_id", Value: "claim-1", ValueType: "string", Source: "frontmatter"},
		{Key: "evidence", Value: "single-source", ValueType: "string", Source: "frontmatter"},
		{Key: "last_verified", Value: "2024-01-01", ValueType: "date", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertResearchMetadata: %v", err)
	}

	asOf := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	quality, err := st.GetMetadataQuality("research.md", asOf)
	if err != nil {
		t.Fatalf("GetMetadataQuality: %v", err)
	}
	if !qualityHasIssue(quality, "weak_citations") {
		t.Fatalf("expected weak_citations without outgoing references, got %+v", quality.Issues)
	}
	if !qualityHasIssue(quality, "stale_last_verified") {
		t.Fatalf("expected stale_last_verified, got %+v", quality.Issues)
	}

	if err := st.InsertEdges([]Edge{{Source: "research.md", Target: "source.md", Kind: "references"}}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}
	quality, err = st.GetMetadataQuality("research.md", asOf)
	if err != nil {
		t.Fatalf("GetMetadataQuality after edge: %v", err)
	}
	if qualityHasIssue(quality, "weak_citations") {
		t.Fatalf("did not expect weak_citations after outgoing reference, got %+v", quality.Issues)
	}
}

func TestMetadataQualityStats(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "good.md", "good.md")
	insertTestNode(t, st, "poor.md", "poor.md")

	if err := st.UpsertGovernanceMetadata("good.md", []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "owner", Value: "owner", ValueType: "string", Source: "frontmatter"},
		{Key: "review_due", Value: "2027-01-01", ValueType: "date", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}
	if err := st.InsertEdges([]Edge{{Source: "good.md", Target: "poor.md", Kind: "references"}}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	stats, err := st.GetMetadataQualityStats(time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GetMetadataQualityStats: %v", err)
	}
	if stats.TotalDocs != 2 {
		t.Fatalf("TotalDocs = %d, want 2", stats.TotalDocs)
	}
	if stats.IssueCounts["missing_status"] != 1 {
		t.Fatalf("missing_status count = %d, want 1", stats.IssueCounts["missing_status"])
	}
	if stats.AverageScore <= 0 || stats.AverageScore > 100 {
		t.Fatalf("unexpected average score %.2f", stats.AverageScore)
	}
}

func qualityHasIssue(q *MetadataQualityRecord, code string) bool {
	if q == nil {
		return false
	}
	for _, issue := range q.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

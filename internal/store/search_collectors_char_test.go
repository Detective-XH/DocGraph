package store

import (
	"sort"
	"testing"
)

// TestGetNodesByRetrievalFilters_Characterization is a black-box characterization
// test for getNodesByRetrievalFilters. It seeds several document nodes with
// governance and research metadata and asserts that the returned node sets are
// identical before and after the refactor of that function.
//
// This test uses the unexported getNodesByRetrievalFilters directly (package
// store) so it exercises the exact code path that collectMetadataFilteredCandidates
// relies on.
func TestGetNodesByRetrievalFilters_Characterization(t *testing.T) {
	st := newTestStore(t)

	// Seed three documents with different governance/research states.
	insertTestNode(t, st, "char/approved.md", "char/approved.md")
	insertTestNode(t, st, "char/draft.md", "char/draft.md")
	insertTestNode(t, st, "char/research.md", "char/research.md")
	insertTestNode(t, st, "char/nometadata.md", "char/nometadata.md")

	// approved.md: approved, internal, canonical
	govApproved := []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "sensitivity", Value: "internal", ValueType: "string", Source: "frontmatter"},
		{Key: "canonical_source", Value: "true", ValueType: "string", Source: "frontmatter"},
		{Key: "allowed_audience", Value: "employees", ValueType: "string", Source: "frontmatter"},
		{Key: "effective_date", Value: "2025-01-01", ValueType: "date", Source: "frontmatter"},
	}
	if err := st.InsertDocumentMetadata("char/approved.md", govApproved); err != nil {
		t.Fatalf("InsertDocumentMetadata approved: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("char/approved.md", govApproved); err != nil {
		t.Fatalf("UpsertGovernanceMetadata approved: %v", err)
	}

	// draft.md: draft, public
	govDraft := []MetadataTuple{
		{Key: "status", Value: "draft", ValueType: "string", Source: "frontmatter"},
		{Key: "sensitivity", Value: "public", ValueType: "string", Source: "frontmatter"},
	}
	if err := st.InsertDocumentMetadata("char/draft.md", govDraft); err != nil {
		t.Fatalf("InsertDocumentMetadata draft: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("char/draft.md", govDraft); err != nil {
		t.Fatalf("UpsertGovernanceMetadata draft: %v", err)
	}

	// research.md: has research metadata
	researchTuples := []MetadataTuple{
		{Key: "claim_id", Value: "claim-char-001", ValueType: "string", Source: "frontmatter"},
		{Key: "source_type", Value: "primary", ValueType: "string", Source: "frontmatter"},
		{Key: "confidence", Value: "high", ValueType: "string", Source: "frontmatter"},
		{Key: "analyst_status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "valid_until", Value: "2027-01-01", ValueType: "date", Source: "frontmatter"},
		{Key: "effective_date", Value: "2025-01-01", ValueType: "date", Source: "frontmatter"},
	}
	if err := st.InsertDocumentMetadata("char/research.md", researchTuples); err != nil {
		t.Fatalf("InsertDocumentMetadata research: %v", err)
	}
	if err := st.UpsertResearchMetadata("char/research.md", researchTuples); err != nil {
		t.Fatalf("UpsertResearchMetadata research: %v", err)
	}

	se := st.Searcher

	nodeIDs := func(nodes []Node) []string {
		ids := make([]string, len(nodes))
		for i, n := range nodes {
			ids[i] = n.ID
		}
		sort.Strings(ids)
		return ids
	}

	assertFilter := func(t *testing.T, name string, req searchRequest, want []string) {
		t.Helper()
		nodes, err := se.getNodesByRetrievalFilters(req)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		got := nodeIDs(nodes)
		sort.Strings(want)
		if len(got) != len(want) {
			t.Errorf("%s: got %v, want %v", name, got, want)
			return
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s: got %v, want %v", name, got, want)
				return
			}
		}
	}

	// Filter by governance status=approved → only char/approved.md
	assertFilter(t, "status=approved", searchRequest{
		Governance: GovernanceSearchOptions{Status: "approved"},
	}, []string{"char/approved.md"})

	// Filter by governance status=draft → only char/draft.md
	assertFilter(t, "status=draft", searchRequest{
		Governance: GovernanceSearchOptions{Status: "draft"},
	}, []string{"char/draft.md"})

	// Filter by sensitivity=internal → only char/approved.md
	assertFilter(t, "sensitivity=internal", searchRequest{
		Governance: GovernanceSearchOptions{Sensitivity: "internal"},
	}, []string{"char/approved.md"})

	// Filter by canonical_source=true → only char/approved.md
	assertFilter(t, "canonical_source=true", searchRequest{
		Governance: GovernanceSearchOptions{CanonicalSource: "true"},
	}, []string{"char/approved.md"})

	// Filter by allowed_audience: triggers gm.node_id IS NOT NULL presence check only
	// (no SQL value clause in getNodesByRetrievalFilters) — returns all docs with a
	// governance row regardless of the actual AllowedAudience field value.
	// Post-filter audience matching is done in metadataMatchesRequest (in-Go).
	assertFilter(t, "allowed_audience=employees", searchRequest{
		Governance: GovernanceSearchOptions{AllowedAudience: "employees"},
	}, []string{"char/approved.md", "char/draft.md"})

	// Filter by research claim_id → only char/research.md
	assertFilter(t, "claim_id=claim-char-001", searchRequest{
		Research: ResearchSearchOptions{ClaimID: "claim-char-001"},
	}, []string{"char/research.md"})

	// Filter by research source_type=primary
	assertFilter(t, "source_type=primary", searchRequest{
		Research: ResearchSearchOptions{SourceType: "primary"},
	}, []string{"char/research.md"})

	// Filter by research confidence=high
	assertFilter(t, "confidence=high", searchRequest{
		Research: ResearchSearchOptions{Confidence: "high"},
	}, []string{"char/research.md"})

	// Filter by research analyst_status=approved
	assertFilter(t, "analyst_status=approved", searchRequest{
		Research: ResearchSearchOptions{AnalystStatus: "approved"},
	}, []string{"char/research.md"})

	// Filter by as_of_date: effective_date 2025-01-01 <= 2026-06-14 → approved.md, research.md
	// Note: no governance row for research.md, no research row for approved.md → gm/rm IS NULL passes
	assertFilter(t, "as_of_date=2026-06-14", searchRequest{
		Governance: GovernanceSearchOptions{AsOfDate: "2026-06-14"},
	}, []string{"char/approved.md", "char/draft.md", "char/nometadata.md", "char/research.md"})

	// Combined: governance status=approved AND research source_type=primary → no rows (different docs)
	assertFilter(t, "status=approved+source_type=primary", searchRequest{
		Governance: GovernanceSearchOptions{Status: "approved"},
		Research:   ResearchSearchOptions{SourceType: "primary"},
	}, []string{})

	// No filters: all 4 documents returned
	assertFilter(t, "no_filters", searchRequest{}, []string{
		"char/approved.md", "char/draft.md", "char/nometadata.md", "char/research.md",
	})
}

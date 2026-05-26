package store

import (
	"fmt"
	"testing"
	"time"
)

// helper: insert a document node and its governance_metadata in one call.
func insertNodeWithGov(t *testing.T, st *Store, id, filePath, status string) {
	t.Helper()
	node := Node{
		ID:            id,
		Kind:          "document",
		Name:          id,
		QualifiedName: id,
		FilePath:      filePath,
		StartLine:     1,
		EndLine:       10,
		UpdatedAt:     time.Now().Unix(),
	}
	if err := st.InsertNodes([]Node{node}); err != nil {
		t.Fatalf("InsertNodes(%q): %v", id, err)
	}
	if err := st.UpsertGovernanceMetadata(id, []MetadataTuple{
		{Key: "status", Value: status, ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata(%q, %q): %v", id, status, err)
	}
}

// helper: insert a similar_to edge with the given score.
func insertSimilarToEdge(t *testing.T, st *Store, src, tgt string, score float64) {
	t.Helper()
	// Normalise ordering as the similarity engine does (source < target).
	if src > tgt {
		src, tgt = tgt, src
	}
	meta := fmt.Sprintf(`{"score": %.2f}`, score)
	if err := st.InsertEdges([]Edge{
		{Source: src, Target: tgt, Kind: "similar_to", Metadata: meta},
	}); err != nil {
		t.Fatalf("InsertEdges similar_to (%q -> %q score=%.2f): %v", src, tgt, score, err)
	}
}

// findDuplicatePolicies helper that uses default opts.
func defaultDriftOpts() DriftAuditOpts {
	return DriftAuditOpts{
		SimilarityMin: 0.75,
		Limit:         100,
		AsOf:          time.Now().UTC(),
	}
}

// ---------------------------------------------------------------------------
// findDuplicatePolicies tests
// ---------------------------------------------------------------------------

func TestFindDuplicatePolicies_Positive(t *testing.T) {
	st := tempStore(t)

	insertNodeWithGov(t, st, "policy-a.md", "policy-a.md", "approved")
	insertNodeWithGov(t, st, "policy-b.md", "policy-b.md", "approved")
	insertSimilarToEdge(t, st, "policy-a.md", "policy-b.md", 0.80)

	findings, err := st.findDuplicatePolicies(defaultDriftOpts())
	if err != nil {
		t.Fatalf("findDuplicatePolicies: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 duplicate finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Code != CodePolicyDuplicate {
		t.Errorf("Code: got %q, want %q", f.Code, CodePolicyDuplicate)
	}
	if f.Severity != "warning" {
		t.Errorf("Severity: got %q, want \"warning\"", f.Severity)
	}
	// The pair should reference both nodes (either direction).
	gotIDs := map[string]bool{f.NodeID: true, f.RelatedNodeID: true}
	if !gotIDs["policy-a.md"] || !gotIDs["policy-b.md"] {
		t.Errorf("expected pair {policy-a.md, policy-b.md}, got NodeID=%q RelatedNodeID=%q",
			f.NodeID, f.RelatedNodeID)
	}
}

func TestFindDuplicatePolicies_LowScore_NegativeCase(t *testing.T) {
	st := tempStore(t)

	insertNodeWithGov(t, st, "policy-a.md", "policy-a.md", "approved")
	insertNodeWithGov(t, st, "policy-b.md", "policy-b.md", "approved")
	// Score 0.60 is below default SimilarityMin=0.75.
	insertSimilarToEdge(t, st, "policy-a.md", "policy-b.md", 0.60)

	findings, err := st.findDuplicatePolicies(defaultDriftOpts())
	if err != nil {
		t.Fatalf("findDuplicatePolicies: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for score=0.60 < 0.75, got %d: %+v", len(findings), findings)
	}
}

func TestFindDuplicatePolicies_OneNotApproved_NegativeCase(t *testing.T) {
	st := tempStore(t)

	insertNodeWithGov(t, st, "policy-a.md", "policy-a.md", "approved")
	insertNodeWithGov(t, st, "policy-b.md", "policy-b.md", "draft")
	insertSimilarToEdge(t, st, "policy-a.md", "policy-b.md", 0.80)

	findings, err := st.findDuplicatePolicies(defaultDriftOpts())
	if err != nil {
		t.Fatalf("findDuplicatePolicies: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings when one node is draft, got %d: %+v", len(findings), findings)
	}
}

// ---------------------------------------------------------------------------
// findNonCanonicalCopies tests
// ---------------------------------------------------------------------------

func TestFindNonCanonicalCopies_Positive(t *testing.T) {
	st := tempStore(t)

	// Two approved documents both claim the same canonical_source.
	for _, id := range []string{"policy-x.md", "policy-y.md"} {
		node := Node{
			ID:            id,
			Kind:          "document",
			Name:          id,
			QualifiedName: id,
			FilePath:      id,
			StartLine:     1,
			EndLine:       10,
			UpdatedAt:     time.Now().Unix(),
		}
		if err := st.InsertNodes([]Node{node}); err != nil {
			t.Fatalf("InsertNodes(%q): %v", id, err)
		}
		if err := st.UpsertGovernanceMetadata(id, []MetadataTuple{
			{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
			{Key: "canonical_source", Value: "policy-hr-leave", ValueType: "string", Source: "frontmatter"},
		}); err != nil {
			t.Fatalf("UpsertGovernanceMetadata(%q): %v", id, err)
		}
	}

	findings, err := st.findNonCanonicalCopies(defaultDriftOpts())
	if err != nil {
		t.Fatalf("findNonCanonicalCopies: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 non_canonical findings, got %d: %+v", len(findings), findings)
	}
	for _, f := range findings {
		if f.Code != CodePolicyNonCanonical {
			t.Errorf("Code: got %q, want %q", f.Code, CodePolicyNonCanonical)
		}
		if f.Severity != "warning" {
			t.Errorf("Severity: got %q, want \"warning\"", f.Severity)
		}
	}
	// Both node IDs should appear.
	nodeIDs := map[string]bool{}
	for _, f := range findings {
		nodeIDs[f.NodeID] = true
	}
	if !nodeIDs["policy-x.md"] || !nodeIDs["policy-y.md"] {
		t.Errorf("expected both policy-x.md and policy-y.md in findings, got %v", nodeIDs)
	}
}

func TestFindNonCanonicalCopies_SingleDocument_NegativeCase(t *testing.T) {
	st := tempStore(t)

	// Only ONE node with this canonical_source — no conflict.
	node := Node{
		ID:            "policy-z.md",
		Kind:          "document",
		Name:          "policy-z.md",
		QualifiedName: "policy-z.md",
		FilePath:      "policy-z.md",
		StartLine:     1,
		EndLine:       10,
		UpdatedAt:     time.Now().Unix(),
	}
	if err := st.InsertNodes([]Node{node}); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("policy-z.md", []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "canonical_source", Value: "policy-hr-leave", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}

	findings, err := st.findNonCanonicalCopies(defaultDriftOpts())
	if err != nil {
		t.Fatalf("findNonCanonicalCopies: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for single canonical_source document, got %d: %+v", len(findings), findings)
	}
}

func TestFindNonCanonicalCopies_ArchivedExcluded_NegativeCase(t *testing.T) {
	st := tempStore(t)

	// Two nodes share the same canonical_source, but one is archived.
	// Only one non-archived → count = 1 → no conflict.
	ids := []string{"policy-live.md", "policy-archived.md"}
	statuses := []string{"approved", "archived"}
	for i, id := range ids {
		node := Node{
			ID:            id,
			Kind:          "document",
			Name:          id,
			QualifiedName: id,
			FilePath:      id,
			StartLine:     1,
			EndLine:       10,
			UpdatedAt:     time.Now().Unix(),
		}
		if err := st.InsertNodes([]Node{node}); err != nil {
			t.Fatalf("InsertNodes(%q): %v", id, err)
		}
		if err := st.UpsertGovernanceMetadata(id, []MetadataTuple{
			{Key: "status", Value: statuses[i], ValueType: "string", Source: "frontmatter"},
			{Key: "canonical_source", Value: "policy-hr-leave", ValueType: "string", Source: "frontmatter"},
		}); err != nil {
			t.Fatalf("UpsertGovernanceMetadata(%q): %v", id, err)
		}
	}

	findings, err := st.findNonCanonicalCopies(defaultDriftOpts())
	if err != nil {
		t.Fatalf("findNonCanonicalCopies: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings when one node is archived, got %d: %+v", len(findings), findings)
	}
}

package store

import (
	"strings"
	"testing"
	"time"
)

func upsertTestFile(t *testing.T, st *Store, path, hash string, hasFrontmatter bool) {
	t.Helper()
	if err := st.UpsertFile(FileInfo{
		Path:           path,
		ContentHash:    hash,
		Size:           100,
		ModifiedAt:     time.Now().Unix(),
		IndexedAt:      time.Now().Unix(),
		NodeCount:      1,
		HasFrontmatter: hasFrontmatter,
	}); err != nil {
		t.Fatalf("UpsertFile(%q): %v", path, err)
	}
}

func TestGetPendingEnrichments_OnlyFrontmatterlessAndStale(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "doc/a.pdf", "doc/a.pdf")
	insertTestNode(t, st, "doc/b.md", "doc/b.md")
	insertTestNode(t, st, "doc/c.docx", "doc/c.docx")
	upsertTestFile(t, st, "doc/a.pdf", "hash-a", false)
	upsertTestFile(t, st, "doc/b.md", "hash-b", true)
	upsertTestFile(t, st, "doc/c.docx", "hash-c", false)

	if err := st.UpsertAgentEnrichment(AgentEnrichment{
		DocID:       "doc/c.docx",
		Summary:     "Current summary.",
		ContentHash: "hash-c",
	}); err != nil {
		t.Fatalf("UpsertAgentEnrichment: %v", err)
	}

	pending, err := st.GetPendingEnrichments(10)
	if err != nil {
		t.Fatalf("GetPendingEnrichments: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending doc, got %d: %+v", len(pending), pending)
	}
	if pending[0].DocID != "doc/a.pdf" {
		t.Fatalf("expected doc/a.pdf pending, got %s", pending[0].DocID)
	}

	upsertTestFile(t, st, "doc/c.docx", "hash-c2", false)
	pending, err = st.GetPendingEnrichments(10)
	if err != nil {
		t.Fatalf("GetPendingEnrichments after stale hash: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected stale doc to become pending, got %d: %+v", len(pending), pending)
	}
}

func TestUpsertAgentEnrichment_RejectsStaleContentHash(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "doc/a.pdf", "doc/a.pdf")
	upsertTestFile(t, st, "doc/a.pdf", "current", false)

	err := st.UpsertAgentEnrichment(AgentEnrichment{
		DocID:       "doc/a.pdf",
		Summary:     "Stale summary.",
		ContentHash: "old",
	})
	if err == nil {
		t.Fatal("expected stale content_hash error")
	}
	if !strings.Contains(err.Error(), "content_hash mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpsertAgentEnrichment_AgentInferredDoesNotOverrideFrontmatter(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "doc/policy.md", "doc/policy.md")
	upsertTestFile(t, st, "doc/policy.md", "hash-policy", true)

	frontmatter := []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "owner", Value: "human-owner", ValueType: "string", Source: "frontmatter"},
	}
	if err := st.InsertDocumentMetadata("doc/policy.md", frontmatter); err != nil {
		t.Fatalf("InsertDocumentMetadata: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("doc/policy.md", frontmatter); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}

	confidence := 0.42
	err := st.UpsertAgentEnrichment(AgentEnrichment{
		DocID:       "doc/policy.md",
		Summary:     "Agent summary.",
		ModelHint:   "test-model",
		ContentHash: "hash-policy",
		Metadata: []MetadataTuple{
			{Key: "status", Value: "draft", ValueType: "string", Source: "agent_inferred", Confidence: &confidence},
			{Key: "owner", Value: "agent-owner", ValueType: "string", Source: "agent_inferred", Confidence: &confidence},
		},
	})
	if err != nil {
		t.Fatalf("UpsertAgentEnrichment: %v", err)
	}

	gov, err := st.GetGovernanceMetadata("doc/policy.md")
	if err != nil {
		t.Fatalf("GetGovernanceMetadata: %v", err)
	}
	if gov == nil {
		t.Fatal("expected governance projection")
	}
	if gov.Status != "approved" || gov.Owner != "human-owner" {
		t.Fatalf("frontmatter should win, got status=%q owner=%q", gov.Status, gov.Owner)
	}

	summary, err := st.GetAISummary("doc/policy.md")
	if err != nil {
		t.Fatalf("GetAISummary: %v", err)
	}
	if summary == nil || summary.Summary != "Agent summary." || summary.ModelHint != "test-model" {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	tuples, err := st.GetDocumentMetadata("doc/policy.md")
	if err != nil {
		t.Fatalf("GetDocumentMetadata: %v", err)
	}
	sources := map[string]bool{}
	for _, tuple := range tuples {
		if tuple.Key == "status" {
			sources[tuple.Source] = true
		}
	}
	if !sources["frontmatter"] || !sources["agent_inferred"] {
		t.Fatalf("expected frontmatter and agent_inferred audit rows, got %+v", sources)
	}
}

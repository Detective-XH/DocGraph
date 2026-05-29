package store

import (
	"strings"
	"testing"
	"time"
)

// TestRefreshMetadataProjection_EmptyWinnersDeletesStaleRow guards the empty-case
// branch of the refresh path: when no document_metadata rows resolve to a projected
// column, the stale typed-projection row must be DELETEd (whereas the Upsert path
// returns nil and keeps it). The shared writeGovernanceProjection helper never runs
// in this case, so the delete must stay in refreshGovernanceProjection itself.
func TestRefreshMetadataProjection_EmptyWinnersDeletesStaleRow(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "doc/policy.md", "doc/policy.md")

	// Seed a governance projection directly, with no backing document_metadata rows.
	if err := st.UpsertGovernanceMetadata("doc/policy.md", []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}
	if gov, err := st.GetGovernanceMetadata("doc/policy.md"); err != nil || gov == nil {
		t.Fatalf("expected governance row before refresh; err=%v gov=%v", err, gov)
	}

	// With zero document_metadata rows, refresh resolves no winners and must delete
	// the stale projection rather than leave it in place.
	if err := st.RefreshMetadataProjections("doc/policy.md"); err != nil {
		t.Fatalf("RefreshMetadataProjections: %v", err)
	}
	gov, err := st.GetGovernanceMetadata("doc/policy.md")
	if err != nil {
		t.Fatalf("GetGovernanceMetadata: %v", err)
	}
	if gov != nil {
		t.Fatalf("expected stale governance row deleted by empty-winners refresh, got %+v", gov)
	}
}

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
		ModelID:     "test-model",
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
		ModelID:     "test-model",
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
		ModelID:     "test-model",
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
	if summary == nil || summary.Summary != "Agent summary." || summary.ModelID != "test-model" {
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

func TestUpsertAgentEnrichment_ModelSwitchKeepsRunHistory(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "doc/a.md", "doc/a.md")
	upsertTestFile(t, st, "doc/a.md", "hash-a", false)

	if err := st.UpsertAgentEnrichment(AgentEnrichment{
		DocID:       "doc/a.md",
		Summary:     "Processed by older model.",
		Provider:    "openai",
		ModelID:     "gpt-5.4",
		AgentID:     "codex",
		ContentHash: "hash-a",
	}); err != nil {
		t.Fatalf("first UpsertAgentEnrichment: %v", err)
	}
	if err := st.UpsertAgentEnrichment(AgentEnrichment{
		DocID:       "doc/a.md",
		Summary:     "Processed by newer model.",
		Provider:    "openai",
		ModelID:     "gpt-5.5",
		AgentID:     "codex",
		ContentHash: "hash-a",
	}); err != nil {
		t.Fatalf("second UpsertAgentEnrichment: %v", err)
	}

	summary, err := st.GetAISummary("doc/a.md")
	if err != nil {
		t.Fatalf("GetAISummary: %v", err)
	}
	if summary == nil || summary.Summary != "Processed by newer model." || summary.ModelID != "gpt-5.5" {
		t.Fatalf("current summary should use the latest model output, got %+v", summary)
	}

	runs, err := st.GetAgentEnrichmentRuns("doc/a.md")
	if err != nil {
		t.Fatalf("GetAgentEnrichmentRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d: %+v", len(runs), runs)
	}
	seen := map[string]bool{}
	for _, run := range runs {
		seen[run.ModelID] = true
	}
	if !seen["gpt-5.4"] || !seen["gpt-5.5"] {
		t.Fatalf("expected both model IDs in run history, got %+v", runs)
	}
}

func TestUpsertAgentEnrichment_RequiresModelID(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "doc/a.md", "doc/a.md")
	upsertTestFile(t, st, "doc/a.md", "hash-a", false)

	err := st.UpsertAgentEnrichment(AgentEnrichment{
		DocID:       "doc/a.md",
		Summary:     "No lineage.",
		ContentHash: "hash-a",
	})
	if err == nil || !strings.Contains(err.Error(), "model_id is required") {
		t.Fatalf("expected model_id error, got %v", err)
	}
}

package tools

import (
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

// ---------------------------------------------------------------------------
// handleSearch — governance filter: reindex pending note
// ---------------------------------------------------------------------------

// TestHandleSearchGovernanceFilter_ReindexPending verifies that when the
// governance_metadata table is empty and reindex_required="true" (written by
// migration 005 on every fresh store), a search with a status filter returns
// the "metadata reindex pending" advisory note.
func TestHandleSearchGovernanceFilter_ReindexPending(t *testing.T) {
	h, _ := newTestHandler(t)
	// newTestHandler calls store.Open which runs all migrations including 005,
	// so reindex_required="true" is already set. No additional setup needed.

	res, err := callTool(h, h.handleSearch, map[string]interface{}{
		"query":  "anything",
		"status": "approved",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	text := extractText(res)
	if !strings.Contains(text, "metadata reindex pending") {
		t.Errorf("expected 'metadata reindex pending' in output when reindex_required=true and governance table is empty, got:\n%s", text)
	}
}

// ---------------------------------------------------------------------------
// handleSearch — governance filter: empty result (no error, 0 matches)
// ---------------------------------------------------------------------------

// TestHandleSearchGovernanceFilter_Empty verifies that when governance_metadata
// has rows with status="draft" and a search uses status="approved", the result
// is 0 matches and no error. The "metadata reindex pending" note must NOT appear
// because we explicitly clear reindex_required.
func TestHandleSearchGovernanceFilter_Empty(t *testing.T) {
	h, st := newTestHandler(t)

	// Clear reindex_required so the pending note is not emitted.
	if err := st.DeleteProjectMeta(store.MetaKeyReindexRequired, store.MetaKeyReindexScope, store.MetaKeyReindexReason); err != nil {
		t.Fatalf("DeleteProjectMeta: %v", err)
	}

	// Insert a document node.
	node := store.Node{
		ID:            "draft-doc.md",
		Kind:          "document",
		Name:          "Draft Doc",
		QualifiedName: "draft-doc.md",
		FilePath:      "draft-doc.md",
		StartLine:     1,
		EndLine:       10,
		BodyExcerpt:   "draft content",
		UpdatedAt:     1,
	}
	if err := st.InsertNodes([]store.Node{node}); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}

	// Upsert governance metadata with status="draft".
	tuples := []store.MetadataTuple{
		{Key: "status", Value: "draft", ValueType: "string", Source: "frontmatter"},
	}
	if err := st.UpsertGovernanceMetadata("draft-doc.md", tuples); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}

	// Search for status="approved" — should match 0 docs, no error.
	res, err := callTool(h, h.handleSearch, map[string]interface{}{
		"query":  "draft",
		"status": "approved",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	text := extractText(res)
	if !strings.Contains(text, "Found 0 results") {
		t.Errorf("expected 'Found 0 results', got:\n%s", text)
	}
	// Reindex note must not appear since we cleared the flag.
	if strings.Contains(text, "metadata reindex pending") {
		t.Errorf("unexpected 'metadata reindex pending' note after clearing reindex_required, got:\n%s", text)
	}
}

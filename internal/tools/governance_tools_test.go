package tools

import (
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

// ---------------------------------------------------------------------------
// handleSearch — governance filter: reindex pending note
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// handleSearch — governance filter: empty result (no error, 0 matches)
// ---------------------------------------------------------------------------

// TestHandleSearchGovernanceFilter_Empty verifies that when governance_metadata
// has rows with status="draft" and a search uses status="approved", the result
// is 0 matches and no error.
func TestHandleSearchGovernanceFilter_Empty(t *testing.T) {
	h, st := newTestHandler(t)

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
	res, err := callTool(h, h.handleSearch, map[string]any{
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

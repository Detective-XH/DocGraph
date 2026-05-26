package tools

import (
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

func TestHandleSearchResearchFilter(t *testing.T) {
	h, st := newTestHandler(t)

	node := store.Node{
		ID:            "research-alpha.md",
		Kind:          "document",
		Name:          "Research Alpha",
		QualifiedName: "research-alpha.md",
		FilePath:      "research-alpha.md",
		StartLine:     1,
		EndLine:       10,
		BodyExcerpt:   "alpha research evidence",
		UpdatedAt:     1,
	}
	if err := st.InsertNodes([]store.Node{node}); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	tuples := []store.MetadataTuple{
		{Key: "claim_id", Value: "claim-alpha-001", ValueType: "string", Source: "frontmatter"},
		{Key: "source_type", Value: "primary", ValueType: "string", Source: "frontmatter"},
		{Key: "confidence", Value: "high", ValueType: "string", Source: "frontmatter"},
	}
	if err := st.UpsertResearchMetadata("research-alpha.md", tuples); err != nil {
		t.Fatalf("UpsertResearchMetadata: %v", err)
	}

	res, err := callTool(h, h.handleSearch, map[string]interface{}{
		"query":       "alpha",
		"source_type": "primary",
		"confidence":  "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	text := extractText(res)
	if !strings.Contains(text, "Research Alpha") {
		t.Errorf("expected research result in output, got:\n%s", text)
	}
	if !strings.Contains(text, "source_type") || !strings.Contains(text, "confidence") {
		t.Errorf("expected research filters in output, got:\n%s", text)
	}
}

func TestHandleSearchResearchFilterAppliesLimitAfterFTS(t *testing.T) {
	h, st := newTestHandler(t)

	nodes := []store.Node{
		{
			ID:            "aaa-nonmatch.md",
			Kind:          "document",
			Name:          "AAA Nonmatch",
			QualifiedName: "aaa-nonmatch.md",
			FilePath:      "aaa-nonmatch.md",
			StartLine:     1,
			EndLine:       10,
			BodyExcerpt:   "unrelated body",
			UpdatedAt:     1,
		},
		{
			ID:            "zzz-match.md",
			Kind:          "document",
			Name:          "ZZZ Match",
			QualifiedName: "zzz-match.md",
			FilePath:      "zzz-match.md",
			StartLine:     1,
			EndLine:       10,
			BodyExcerpt:   "alpha body",
			UpdatedAt:     1,
		},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	for _, id := range []string{"aaa-nonmatch.md", "zzz-match.md"} {
		if err := st.UpsertResearchMetadata(id, []store.MetadataTuple{
			{Key: "source_type", Value: "primary", ValueType: "string", Source: "frontmatter"},
			{Key: "confidence", Value: "high", ValueType: "string", Source: "frontmatter"},
		}); err != nil {
			t.Fatalf("UpsertResearchMetadata %s: %v", id, err)
		}
	}

	res, err := callTool(h, h.handleSearch, map[string]interface{}{
		"query":       "alpha",
		"source_type": "primary",
		"confidence":  "high",
		"limit":       float64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	text := extractText(res)
	if !strings.Contains(text, "ZZZ Match") {
		t.Errorf("expected FTS-matching research result after applying limit, got:\n%s", text)
	}
	if strings.Contains(text, "AAA Nonmatch") {
		t.Errorf("did not expect non-FTS result, got:\n%s", text)
	}
}

func TestHandleNodeResearchSection(t *testing.T) {
	h, st := newTestHandler(t)

	node := store.Node{
		ID:            "research-node.md",
		Kind:          "document",
		Name:          "Research Node",
		QualifiedName: "research-node.md",
		FilePath:      "research-node.md",
		StartLine:     1,
		EndLine:       10,
		BodyExcerpt:   "research node body",
		UpdatedAt:     1,
	}
	if err := st.InsertNodes([]store.Node{node}); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	if err := st.UpsertResearchMetadata("research-node.md", []store.MetadataTuple{
		{Key: "claim_id", Value: "claim-node-001", ValueType: "string", Source: "frontmatter"},
		{Key: "confidence", Value: "medium", ValueType: "string", Source: "frontmatter"},
		{Key: "analyst_status", Value: "peer-reviewed", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertResearchMetadata: %v", err)
	}

	res, err := callTool(h, h.handleNode, map[string]interface{}{
		"document": "research-node.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	text := extractText(res)
	for _, want := range []string{"### Research Provenance", "claim-node-001", "medium", "peer-reviewed"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in node output, got:\n%s", want, text)
		}
	}
}

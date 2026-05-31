package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

// newLinksTestHandler builds a fixture with a hub document that links to one
// other document (twice — a duplicate target), to a heading within itself (a
// same-document reference), and to an external URL. This is the shape that
// confused the AX-probe runner agents: raw rows mixing duplicates, self-refs,
// and external links with no derived count.
func newLinksTestHandler(t *testing.T) *handler {
	t.Helper()
	h, st := newTestHandler(t)
	nodes := []store.Node{
		{ID: "hub.md", Kind: "document", Name: "Hub", QualifiedName: "hub.md", FilePath: "hub.md", UpdatedAt: 1},
		{ID: "other.md", Kind: "document", Name: "Other", QualifiedName: "other.md", FilePath: "other.md", UpdatedAt: 1},
		{ID: "hub.md#sec", Kind: "heading", Name: "Section", QualifiedName: "hub.md#sec", FilePath: "hub.md", Level: 1, UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	edges := []store.Edge{
		{Source: "hub.md", Target: "other.md", Kind: "references", Line: 1},
		{Source: "hub.md", Target: "other.md", Kind: "references", Line: 2}, // duplicate target document
		{Source: "hub.md", Target: "hub.md#sec", Kind: "wikilinks_to", Line: 3}, // same-document reference
		{Source: "hub.md", Target: "hub.md", Kind: "links_external", Metadata: `{"url":"https://x.test"}`, Line: 4},
	}
	if err := st.InsertEdges(edges); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestOutgoingLinksSummaryClassifiesAndDedups(t *testing.T) {
	h := newLinksTestHandler(t)

	res, err := h.renderOutgoingLinks("hub.md", 10)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(res)

	// Derived counts computed server-side: the duplicate other.md edges collapse
	// to 1 distinct document, the heading link is a same-document anchor, and the
	// URL is external — so the agent never has to dedup/classify by hand.
	if !strings.Contains(text, "1 distinct other documents") {
		t.Errorf("expected distinct-other-document count of 1, got:\n%s", text)
	}
	if !strings.Contains(text, "1 same-document references") {
		t.Errorf("expected same-document reference count of 1, got:\n%s", text)
	}
	if !strings.Contains(text, "1 external URLs") {
		t.Errorf("expected external count of 1, got:\n%s", text)
	}
	if !strings.Contains(text, "same-document reference (not a link to another document)") {
		t.Errorf("expected the self-reference annotation on the anchor edge, got:\n%s", text)
	}
}

func TestOutgoingLinksTruncationHonesty(t *testing.T) {
	h := newLinksTestHandler(t)

	// 4 total edges, limit 1 → the per-edge list is truncated but the summary
	// must report the true total so the agent does not assume it saw everything.
	res, err := h.renderOutgoingLinks("hub.md", 1)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(res)

	if !strings.Contains(text, "Showing the first 1 of 4 edges") {
		t.Errorf("expected truncation-honesty line 'Showing the first 1 of 4 edges', got:\n%s", text)
	}
	// The full-set counts must survive truncation (distinct other doc still 1).
	if !strings.Contains(text, "1 distinct other documents") {
		t.Errorf("expected counts computed over the full set despite limit=1, got:\n%s", text)
	}
}

func TestIncomingLinksCountsDistinctSourceDocs(t *testing.T) {
	h := newLinksTestHandler(t)

	// other.md is referenced by two edges, both from hub.md → 1 distinct other
	// document, 0 same-document references.
	res, err := h.renderIncomingLinks("other.md", 10)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(res)

	if !strings.Contains(text, "1 distinct other documents") {
		t.Errorf("expected 1 distinct referencing document, got:\n%s", text)
	}
	if !strings.Contains(text, "0 same-document references") {
		t.Errorf("expected 0 same-document references for other.md, got:\n%s", text)
	}
}

// callHandleNode is a helper that invokes handleNode with just a document= arg.
func callHandleNode(t *testing.T, h *handler, document string) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"document": document}
	res, err := h.handleNode(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return extractText(res)
}

// extractSummaryLine extracts the single line from text that contains needle.
// Returns the trimmed line or "" if not found.
func extractSummaryLine(text, needle string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// TestNodeOutgoingLinksSummaryParity asserts that handleNode (docgraph_node)
// emits the identical outgoing summary line as the docgraph_graph facade
// (renderOutgoingLinks) for the same document.
// Fixture: hub.md has 4 edges → 1 distinct other doc, 1 same-doc reference, 1 external URL.
func TestNodeOutgoingLinksSummaryParity(t *testing.T) {
	h := newLinksTestHandler(t)

	// Get the facade's summary line (the ground truth).
	facadeRes, err := h.renderOutgoingLinks("hub.md", 10)
	if err != nil {
		t.Fatal(err)
	}
	facadeText := extractText(facadeRes)
	// The outgoing summary line always contains "outgoing edges →".
	facadeLine := extractSummaryLine(facadeText, "outgoing edges →")
	if facadeLine == "" {
		t.Fatalf("facade did not produce an outgoing summary line; got:\n%s", facadeText)
	}

	// Assert handleNode emits the identical summary line.
	nodeText := callHandleNode(t, h, "hub.md")
	if !strings.Contains(nodeText, facadeLine) {
		t.Errorf("handleNode outgoing summary mismatch.\nexpected to contain: %q\ngot:\n%s", facadeLine, nodeText)
	}
}

// TestNodeIncomingLinksSummaryParity asserts that handleNode (docgraph_node)
// emits the identical incoming summary line as the docgraph_graph facade
// (renderIncomingLinks) for the same document.
// Fixture: other.md has 2 incoming edges from hub.md → 1 distinct other doc, 0 same-doc refs.
func TestNodeIncomingLinksSummaryParity(t *testing.T) {
	h := newLinksTestHandler(t)

	// Get the facade's summary line (the ground truth).
	facadeRes, err := h.renderIncomingLinks("other.md", 10)
	if err != nil {
		t.Fatal(err)
	}
	facadeText := extractText(facadeRes)
	// The incoming summary line always contains "incoming edges ←".
	facadeLine := extractSummaryLine(facadeText, "incoming edges ←")
	if facadeLine == "" {
		t.Fatalf("facade did not produce an incoming summary line; got:\n%s", facadeText)
	}

	// Assert handleNode emits the identical summary line.
	nodeText := callHandleNode(t, h, "other.md")
	if !strings.Contains(nodeText, facadeLine) {
		t.Errorf("handleNode incoming summary mismatch.\nexpected to contain: %q\ngot:\n%s", facadeLine, nodeText)
	}
}

package tools

import (
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

func TestGraphFacadeIncomingReturnsReferences(t *testing.T) {
	h, _ := newGraphFacadeTestHandler(t)

	res, err := callTool(h, h.handleGraphFacade, map[string]any{"operation": "incoming", "document": "b.md", "limit": float64(10)})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", extractText(res))
	}
	text := extractText(res)
	if !strings.Contains(text, "a.md") {
		t.Fatalf("expected incoming output to list referrer a.md, got:\n%s", text)
	}
}

func TestGraphFacadeOutgoingReturnsLinks(t *testing.T) {
	h, _ := newGraphFacadeTestHandler(t)

	res, err := callTool(h, h.handleGraphFacade, map[string]any{"operation": "outgoing", "document": "b.md", "limit": float64(10)})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", extractText(res))
	}
	text := extractText(res)
	if !strings.Contains(text, "d.md") {
		t.Fatalf("expected outgoing output to list target d.md, got:\n%s", text)
	}
}

func TestGraphFacadeImpactReturnsAffectedDocs(t *testing.T) {
	h, _ := newGraphFacadeTestHandler(t)

	res, err := callTool(h, h.handleGraphFacade, map[string]any{"operation": "impact", "document": "b.md", "depth": float64(2)})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", extractText(res))
	}
	text := extractText(res)
	if !strings.Contains(text, "a.md") || !strings.Contains(text, "c.md") {
		t.Fatalf("expected impact output to list transitively-impacted a.md and c.md, got:\n%s", text)
	}
}

func TestGraphFacadeTraceFindsPath(t *testing.T) {
	h, _ := newGraphFacadeTestHandler(t)

	res, err := callTool(h, h.handleGraphFacade, map[string]any{"operation": "trace", "from": "c.md", "to": "b.md"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", extractText(res))
	}
	text := extractText(res)
	if !strings.Contains(text, "Path found") {
		t.Fatalf("expected trace output to report a path, got:\n%s", text)
	}
}

func TestTraceNoPathMessage(t *testing.T) {
	h, _ := newGraphFacadeTestHandler(t)

	// d.md has no outgoing edges, so d.md → c.md has no path.
	res, err := callTool(h, h.handleGraphFacade, map[string]any{"operation": "trace", "from": "d.md", "to": "c.md"})
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(res)
	if !strings.Contains(text, "No wikilink path found within 10 hops") {
		t.Errorf("expected no-path message with wikilink caveat, got: %s", text)
	}
	if !strings.Contains(text, "does NOT mean the documents are unrelated") {
		t.Errorf("expected 'not unrelated' caveat in no-path output, got: %s", text)
	}
}

func TestGraphFacadeRejectsUnknownOperation(t *testing.T) {
	h, _ := newGraphFacadeTestHandler(t)

	res, err := callTool(h, h.handleGraphFacade, map[string]any{"operation": "sideways", "document": "b.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected error for unknown operation, got: %s", extractText(res))
	}
	if text := extractText(res); !strings.Contains(text, "incoming, outgoing, impact, trace") {
		t.Fatalf("expected valid operation list, got: %s", text)
	}
}

func TestGraphFacadeValidatesRequiredInputs(t *testing.T) {
	h, _ := newGraphFacadeTestHandler(t)

	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{name: "incoming document", args: map[string]any{"operation": "incoming"}, want: "document parameter is required"},
		{name: "outgoing document", args: map[string]any{"operation": "outgoing"}, want: "document parameter is required"},
		{name: "impact document", args: map[string]any{"operation": "impact"}, want: "document parameter is required"},
		{name: "trace endpoints", args: map[string]any{"operation": "trace", "from": "a.md"}, want: "both 'from' and 'to' parameters are required"},
		{name: "incoming trace args", args: map[string]any{"operation": "incoming", "document": "b.md", "from": "a.md"}, want: "from and to parameters are only valid for operation=trace"},
		{name: "trace document arg", args: map[string]any{"operation": "trace", "document": "b.md", "from": "a.md", "to": "b.md"}, want: "document parameter is only valid"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := callTool(h, h.handleGraphFacade, tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if !res.IsError {
				t.Fatalf("expected error, got: %s", extractText(res))
			}
			if text := extractText(res); !strings.Contains(text, tc.want) {
				t.Fatalf("expected %q, got: %s", tc.want, text)
			}
		})
	}
}

func newGraphFacadeTestHandler(t *testing.T) (*handler, *store.Store) {
	t.Helper()
	h, st := newTestHandler(t)
	nodes := []store.Node{
		{ID: "a.md", Kind: "document", Name: "Alpha", QualifiedName: "a.md", FilePath: "a.md", UpdatedAt: 1},
		{ID: "b.md", Kind: "document", Name: "Beta", QualifiedName: "b.md", FilePath: "b.md", UpdatedAt: 1},
		{ID: "c.md", Kind: "document", Name: "Gamma", QualifiedName: "c.md", FilePath: "c.md", UpdatedAt: 1},
		{ID: "d.md", Kind: "document", Name: "Delta", QualifiedName: "d.md", FilePath: "d.md", UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	edges := []store.Edge{
		{Source: "a.md", Target: "b.md", Kind: "references", Line: 4},
		{Source: "c.md", Target: "a.md", Kind: "wikilinks_to", Line: 8},
		{Source: "b.md", Target: "d.md", Kind: "related_to", Line: 12},
		{Source: "b.md", Target: "b.md", Kind: "links_external", Metadata: `{"url":"https://example.test"}`, Line: 16},
	}
	if err := st.InsertEdges(edges); err != nil {
		t.Fatal(err)
	}
	return h, st
}

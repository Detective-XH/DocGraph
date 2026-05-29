package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

// TestHandleSimilar_OrderedByScore verifies docgraph_similar returns results
// sorted by similarity score (descending) and that `limit` truncates the
// least-similar tail — not a random subset. The dedup step in handleSimilar
// iterates a map (random order), so before the Go-side sort both the displayed
// order AND which results survived `limit` were nondeterministic.
func TestHandleSimilar_OrderedByScore(t *testing.T) {
	h, st := newTestHandler(t)
	nodes := []store.Node{
		{ID: "a.md", Kind: "document", Name: "Anchor", QualifiedName: "a.md", FilePath: "a.md", StartLine: 1, EndLine: 5, BodyExcerpt: "x", UpdatedAt: 1},
	}
	// Distinct scores, inserted OUT of score order so a correct result cannot
	// come from insertion order.
	targets := []struct {
		id, name string
		score    float64
	}{
		{"t1.md", "T1", 0.20},
		{"t2.md", "T2", 0.95},
		{"t3.md", "T3", 0.50},
		{"t4.md", "T4", 0.80},
		{"t5.md", "T5", 0.35},
		{"t6.md", "T6", 0.65},
	}
	var edges []store.Edge
	for _, tt := range targets {
		nodes = append(nodes, store.Node{ID: tt.id, Kind: "document", Name: tt.name, QualifiedName: tt.id, FilePath: tt.id, StartLine: 1, EndLine: 5, BodyExcerpt: "x", UpdatedAt: 1})
		// canonical order: "a.md" < "tN.md"
		edges = append(edges, store.Edge{Source: "a.md", Target: tt.id, Kind: "similar_to", Metadata: fmt.Sprintf(`{"score":%.2f,"engine":"tfidf"}`, tt.score)})
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertEdges(edges); err != nil {
		t.Fatal(err)
	}

	res, err := callTool(h, h.handleSimilar, map[string]any{"document": "a.md"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)

	// Expected descending-score order: T2(.95) T4(.80) T6(.65) T3(.50) T5(.35) T1(.20).
	want := []string{"T2", "T4", "T6", "T3", "T5", "T1"}
	pos := make([]int, len(want))
	for i, name := range want {
		idx := strings.Index(text, "**"+name+"**")
		if idx < 0 {
			t.Fatalf("result %q missing from output:\n%s", name, text)
		}
		pos[i] = idx
	}
	for i := 1; i < len(pos); i++ {
		if pos[i] < pos[i-1] {
			t.Fatalf("results not in descending-score order: %q appears before %q\n%s", want[i], want[i-1], text)
		}
	}

	// limit truncates the least-similar tail, not a random subset.
	res2, err := callTool(h, h.handleSimilar, map[string]any{"document": "a.md", "limit": float64(2)})
	if err != nil {
		t.Fatal(err)
	}
	text2 := extractText(res2)
	for _, name := range []string{"T2", "T4"} { // top-2 by score must survive
		if !strings.Contains(text2, "**"+name+"**") {
			t.Errorf("limit=2 should keep the top-2 by score; missing %q:\n%s", name, text2)
		}
	}
	for _, name := range []string{"T6", "T3", "T5", "T1"} { // the rest must be dropped
		if strings.Contains(text2, "**"+name+"**") {
			t.Errorf("limit=2 should drop the least-similar tail; unexpectedly kept %q:\n%s", name, text2)
		}
	}
}

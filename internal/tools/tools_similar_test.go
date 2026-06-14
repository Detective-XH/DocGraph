package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

func simEdge(src, tgt, engine string, score float64) store.Edge {
	return store.Edge{Source: src, Target: tgt, Kind: "similar_to",
		Metadata: fmt.Sprintf(`{"score":%.2f,"engine":%q}`, score, engine)}
}

// TestRankSimilarEdges exercises the pure dedup/filter/order/limit contract that
// handleSimilar delegates to — directly on []store.Edge, no store needed.
// tfidfEdge builds a similar_to edge with the real scorePair metadata shape
// (score + tfidf + refs + tags; no engine key).
func tfidfEdge(src, tgt string, score, tfidf, refs, tags float64) store.Edge {
	return store.Edge{Source: src, Target: tgt, Kind: "similar_to",
		Metadata: fmt.Sprintf(`{"score":%.2f,"tfidf":%.2f,"refs":%.2f,"tags":%.2f}`, score, tfidf, refs, tags)}
}

// neuralEdge builds a similar_to edge with the neural similarity metadata shape
// (engine + model_id + score; no tfidf/refs/tags keys).
func neuralEdge(src, tgt, modelID string, score float64) store.Edge {
	return store.Edge{Source: src, Target: tgt, Kind: "similar_to",
		Metadata: fmt.Sprintf(`{"engine":"neural","model_id":%q,"score":%.4f}`, modelID, score)}
}

// assertContains asserts that text contains want, using ctx as description in
// the failure message. Uses t.Helper so failures point to the call site.
func assertContains(t *testing.T, text, want, ctx string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Errorf("expected %q in %s, got:\n%s", want, ctx, text)
	}
}

// assertNotContains asserts that text does not contain want. Uses t.Helper so
// failures point to the call site.
func assertNotContains(t *testing.T, text, want, ctx string) {
	t.Helper()
	if strings.Contains(text, want) {
		t.Errorf("%q should not appear in %s, got:\n%s", want, ctx, text)
	}
}

// TestHandleSimilar_TFIDFShowsBlendAndSignals verifies that a TF-IDF edge (which
// carries tfidf/refs/tags in metadata) renders the blend marker and signal
// components, while a neural edge (which carries engine/model_id/score only)
// renders unchanged without the blend or component text.
func TestHandleSimilar_TFIDFShowsBlendAndSignals(t *testing.T) {
	t.Run("tfidf edge shows blend marker and signal components", func(t *testing.T) {
		h, st := newTestHandler(t)
		nodes := []store.Node{
			{ID: "anchor.md", Kind: "document", Name: "Anchor", QualifiedName: "anchor.md", FilePath: "anchor.md", StartLine: 1, EndLine: 5, BodyExcerpt: "x", UpdatedAt: 1},
			{ID: "peer.md", Kind: "document", Name: "Peer", QualifiedName: "peer.md", FilePath: "peer.md", StartLine: 1, EndLine: 5, BodyExcerpt: "y", UpdatedAt: 1},
		}
		edges := []store.Edge{tfidfEdge("anchor.md", "peer.md", 0.40, 0.50, 0.30, 0.20)}
		if err := st.InsertNodes(nodes); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertEdges(edges); err != nil {
			t.Fatal(err)
		}

		res, err := callTool(h, h.handleSimilar, map[string]any{"document": "anchor.md"})
		if err != nil {
			t.Fatal(err)
		}
		text := extractText(res)

		assertContains(t, text, "0-1 weighted blend", "TF-IDF result")
		assertContains(t, text, "tfidf-cosine 0.50", "TF-IDF result")
		assertContains(t, text, "shared-refs 0.30", "TF-IDF result")
		assertContains(t, text, "shared-tags 0.20", "TF-IDF result")
	})

	t.Run("neural edge does not show blend marker or signal components", func(t *testing.T) {
		h, st := newTestHandler(t)
		nodes := []store.Node{
			{ID: "anchor2.md", Kind: "document", Name: "Anchor2", QualifiedName: "anchor2.md", FilePath: "anchor2.md", StartLine: 1, EndLine: 5, BodyExcerpt: "x", UpdatedAt: 1},
			{ID: "peer2.md", Kind: "document", Name: "Peer2", QualifiedName: "peer2.md", FilePath: "peer2.md", StartLine: 1, EndLine: 5, BodyExcerpt: "y", UpdatedAt: 1},
		}
		edges := []store.Edge{neuralEdge("anchor2.md", "peer2.md", "text-embedding-3-small", 0.90)}
		if err := st.InsertNodes(nodes); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertEdges(edges); err != nil {
			t.Fatal(err)
		}

		// Neural similarity requires --enable-embeddings; bypass by querying
		// with engine=auto (which returns all edges regardless of type) so
		// handleSimilar can reach the neural edge render path.
		res, err := callTool(h, h.handleSimilar, map[string]any{"document": "anchor2.md", "engine": "auto"})
		if err != nil {
			t.Fatal(err)
		}
		text := extractText(res)

		assertNotContains(t, text, "0-1 weighted blend", "neural edge result")
		assertNotContains(t, text, "tfidf-cosine", "neural edge result")
		assertNotContains(t, text, "shared-refs", "neural edge result")
		// Neural edge should show engine and score.
		assertContains(t, text, "engine: neural", "neural edge result")
		assertContains(t, text, "score: 0.90", "neural edge result")
	})
}

// TestHandleSimilar_ZeroResultContainsKeywordCaveat verifies the new sentence
// in the 0-result branch that clarifies keyword-/link-based alternatives are
// not similarity-ranked.
func TestHandleSimilar_ZeroResultContainsKeywordCaveat(t *testing.T) {
	h, st := newTestHandler(t)
	if err := st.InsertNodes([]store.Node{
		{ID: "solo.md", Kind: "document", Name: "Solo", QualifiedName: "solo.md", FilePath: "solo.md", BodyExcerpt: "x", UpdatedAt: 1},
	}); err != nil {
		t.Fatal(err)
	}

	res, err := callTool(h, h.handleSimilar, map[string]any{"document": "solo.md"})
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(res)

	if !strings.Contains(text, "keyword- and link-based") {
		t.Errorf("expected 'keyword- and link-based' in 0-result caveat, got:\n%s", text)
	}
	if !strings.Contains(text, "not a similarity score") {
		t.Errorf("expected 'not a similarity score' in 0-result caveat, got:\n%s", text)
	}
	if !strings.Contains(text, "not ranked by topical overlap") {
		t.Errorf("expected 'not ranked by topical overlap' in 0-result caveat, got:\n%s", text)
	}
}

func TestRankSimilarEdges(t *testing.T) {
	t.Run("orders by score desc and limit drops the tail", func(t *testing.T) {
		edges := []store.Edge{
			simEdge("a.md", "t1.md", "tfidf", 0.20),
			simEdge("a.md", "t2.md", "tfidf", 0.95),
			simEdge("a.md", "t3.md", "tfidf", 0.50),
			simEdge("a.md", "t4.md", "tfidf", 0.80),
		}
		got := rankSimilarEdges(edges, "auto", 2)
		if len(got) != 2 {
			t.Fatalf("limit=2 should keep 2, got %d", len(got))
		}
		if got[0].Target != "t2.md" || got[1].Target != "t4.md" {
			t.Fatalf("expected top-2 by score [t2,t4], got [%s,%s]", got[0].Target, got[1].Target)
		}
	})

	t.Run("dedup prefers neural for the same pair", func(t *testing.T) {
		edges := []store.Edge{
			simEdge("a.md", "b.md", "tfidf", 0.40),
			simEdge("b.md", "a.md", "neural", 0.90), // same canonical pair, reversed
		}
		got := rankSimilarEdges(edges, "auto", 0)
		if len(got) != 1 {
			t.Fatalf("same pair should dedup to 1, got %d", len(got))
		}
		if edgeEngine(got[0]) != "neural" {
			t.Fatalf("dedup should prefer neural, got engine %q", edgeEngine(got[0]))
		}
	})

	t.Run("engine=tfidf filters out neural", func(t *testing.T) {
		edges := []store.Edge{
			simEdge("a.md", "b.md", "neural", 0.90),
			simEdge("a.md", "c.md", "tfidf", 0.50),
		}
		got := rankSimilarEdges(edges, "tfidf", 0)
		if len(got) != 1 || got[0].Target != "c.md" {
			t.Fatalf("engine=tfidf should keep only the tfidf edge, got %+v", got)
		}
	})

	t.Run("engine=neural keeps only neural", func(t *testing.T) {
		edges := []store.Edge{
			simEdge("a.md", "b.md", "neural", 0.90),
			simEdge("a.md", "c.md", "tfidf", 0.50),
		}
		got := rankSimilarEdges(edges, "neural", 0)
		if len(got) != 1 || got[0].Target != "b.md" {
			t.Fatalf("engine=neural should keep only the neural edge, got %+v", got)
		}
	})
}

// TestHandleSimilar_OrderedByScore verifies docgraph_similar returns results
// sorted by similarity score (descending) and that `limit` truncates the
// least-similar tail — not a random subset. The dedup step in handleSimilar
// iterates a map (random order), so before the Go-side sort both the displayed
// order AND which results survived `limit` were nondeterministic.
func TestHandleSimilar_ZeroResultCaveatNamesSearchAndDisclaimsEngineOff(t *testing.T) {
	h, st := newTestHandler(t)
	// A document with no similar_to edges — the topically-unique case.
	if err := st.InsertNodes([]store.Node{
		{ID: "lonely.md", Kind: "document", Name: "Lonely", QualifiedName: "lonely.md", FilePath: "lonely.md", BodyExcerpt: "x", UpdatedAt: 1},
	}); err != nil {
		t.Fatal(err)
	}

	res, err := callTool(h, h.handleSimilar, map[string]any{"document": "lonely.md"})
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(res)

	if !strings.Contains(text, "Found 0 similar documents") {
		t.Fatalf("expected a 0-result, got:\n%s", text)
	}
	// The caveat must name docgraph_search (the actual workhorse the probe runs
	// found, which the old caveat omitted) and must disclaim the wrong causal
	// model that 0 results means the engine/embeddings are off.
	if !strings.Contains(text, "docgraph_search") {
		t.Errorf("expected the 0-result caveat to name docgraph_search, got:\n%s", text)
	}
	if !strings.Contains(text, "does NOT mean the similarity engine is disabled") {
		t.Errorf("expected the caveat to disclaim 'engine disabled', got:\n%s", text)
	}
}

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
		assertContains(t, text2, "**"+name+"**", "limit=2 result (top-2 by score must survive)")
	}
	for _, name := range []string{"T6", "T3", "T5", "T1"} { // the rest must be dropped
		assertNotContains(t, text2, "**"+name+"**", "limit=2 result (least-similar tail must be dropped)")
	}
}

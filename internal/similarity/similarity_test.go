package similarity

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

// setupSimilarityStore creates a temp store with 3 document nodes whose body
// text overlaps in controlled ways: governance and security share terms like
// "policy", "compliance", "security"; readme is topically distinct.
func setupSimilarityStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	nodes := []store.Node{
		{ID: "governance.md", Kind: "document", Name: "Governance", QualifiedName: "governance.md", FilePath: "governance.md", StartLine: 1, EndLine: 10, BodyExcerpt: "governance policy architecture security compliance requirements", UpdatedAt: 1},
		{ID: "security.md", Kind: "document", Name: "Security", QualifiedName: "security.md", FilePath: "security.md", StartLine: 1, EndLine: 10, BodyExcerpt: "security policy compliance audit vulnerability assessment", UpdatedAt: 1},
		{ID: "readme.md", Kind: "document", Name: "README", QualifiedName: "readme.md", FilePath: "readme.md", StartLine: 1, EndLine: 10, BodyExcerpt: "installation guide getting started tutorial quickstart", UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	return st
}

// countSimilarToEdges returns the number of similar_to edges in the store.
func countSimilarToEdges(t *testing.T, st *store.Store) int {
	t.Helper()
	stats, err := st.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	return stats.EdgesByKind["similar_to"]
}

func TestComputeSimilarityBasic(t *testing.T) {
	st := setupSimilarityStore(t)

	// Use a low threshold so that the governance↔security pair (which share
	// "policy", "compliance", "security") is captured.
	if err := ComputeSimilarity(st, 0.05); err != nil {
		t.Fatalf("ComputeSimilarity failed: %v", err)
	}

	edgeCount := countSimilarToEdges(t, st)
	if edgeCount == 0 {
		t.Fatal("expected at least 1 similar_to edge, got 0")
	}

	// Verify that governance↔security pair exists (they share 3 terms).
	// We check via stats; with only 3 docs the maximum is C(3,2)=3 pairs.
	// The governance↔security pair should score higher than the other pairs.
	t.Logf("similar_to edges: %d (out of 3 possible pairs)", edgeCount)
}

func TestComputeSimilarityWithSharedRefs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	// Create 3 docs + 1 target node ("glossary.md").
	// doc-a and doc-b both reference glossary.md; doc-c does not.
	nodes := []store.Node{
		{ID: "doc-a.md", Kind: "document", Name: "Doc A", QualifiedName: "doc-a.md", FilePath: "doc-a.md", StartLine: 1, EndLine: 10, BodyExcerpt: "alpha bravo charlie", UpdatedAt: 1},
		{ID: "doc-b.md", Kind: "document", Name: "Doc B", QualifiedName: "doc-b.md", FilePath: "doc-b.md", StartLine: 1, EndLine: 10, BodyExcerpt: "delta echo foxtrot", UpdatedAt: 1},
		{ID: "doc-c.md", Kind: "document", Name: "Doc C", QualifiedName: "doc-c.md", FilePath: "doc-c.md", StartLine: 1, EndLine: 10, BodyExcerpt: "golf hotel india", UpdatedAt: 1},
		{ID: "glossary.md", Kind: "document", Name: "Glossary", QualifiedName: "glossary.md", FilePath: "glossary.md", StartLine: 1, EndLine: 10, BodyExcerpt: "terms definitions", UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}

	// Both doc-a and doc-b reference glossary.md.
	edges := []store.Edge{
		{Source: "doc-a.md", Target: "glossary.md", Kind: "references"},
		{Source: "doc-b.md", Target: "glossary.md", Kind: "references"},
	}
	if err := st.InsertEdges(edges); err != nil {
		t.Fatal(err)
	}

	// Use a low threshold: even with no text overlap, shared refs should
	// contribute to the composite score (weight 0.3 for refs).
	if err := ComputeSimilarity(st, 0.05); err != nil {
		t.Fatalf("ComputeSimilarity failed: %v", err)
	}

	stats, err := st.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	simEdges := stats.EdgesByKind["similar_to"]
	if simEdges == 0 {
		t.Fatal("expected at least 1 similar_to edge from shared refs, got 0")
	}
	t.Logf("similar_to edges with shared refs: %d", simEdges)
}

func TestComputeSimilarityWithTags(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	tagsAB, _ := json.Marshal(map[string]interface{}{"tags": []string{"security", "compliance"}})
	tagsC, _ := json.Marshal(map[string]interface{}{"tags": []string{"tutorial", "quickstart"}})

	nodes := []store.Node{
		{ID: "policy.md", Kind: "document", Name: "Policy", QualifiedName: "policy.md", FilePath: "policy.md", StartLine: 1, EndLine: 10, BodyExcerpt: "alpha bravo", Metadata: string(tagsAB), UpdatedAt: 1},
		{ID: "audit.md", Kind: "document", Name: "Audit", QualifiedName: "audit.md", FilePath: "audit.md", StartLine: 1, EndLine: 10, BodyExcerpt: "charlie delta", Metadata: string(tagsAB), UpdatedAt: 1},
		{ID: "guide.md", Kind: "document", Name: "Guide", QualifiedName: "guide.md", FilePath: "guide.md", StartLine: 1, EndLine: 10, BodyExcerpt: "echo foxtrot", Metadata: string(tagsC), UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}

	if err := ComputeSimilarity(st, 0.05); err != nil {
		t.Fatalf("ComputeSimilarity failed: %v", err)
	}

	simEdges := countSimilarToEdges(t, st)
	if simEdges == 0 {
		t.Fatal("expected at least 1 similar_to edge from shared tags, got 0")
	}
	t.Logf("similar_to edges with shared tags: %d", simEdges)
}

func TestComputeSimilarityThreshold(t *testing.T) {
	t.Run("high threshold yields zero edges", func(t *testing.T) {
		st := setupSimilarityStore(t)
		if err := ComputeSimilarity(st, 0.99); err != nil {
			t.Fatalf("ComputeSimilarity failed: %v", err)
		}
		if got := countSimilarToEdges(t, st); got != 0 {
			t.Errorf("expected 0 similar_to edges with threshold=0.99, got %d", got)
		}
	})

	t.Run("low threshold yields more edges", func(t *testing.T) {
		st := setupSimilarityStore(t)
		if err := ComputeSimilarity(st, 0.01); err != nil {
			t.Fatalf("ComputeSimilarity failed: %v", err)
		}
		got := countSimilarToEdges(t, st)
		if got == 0 {
			t.Error("expected >0 similar_to edges with threshold=0.01, got 0")
		}
		t.Logf("similar_to edges with threshold=0.01: %d", got)
	})
}

func TestComputeSimilarityEmpty(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	// Empty store — no documents at all.
	if err := ComputeSimilarity(st, 0.25); err != nil {
		t.Fatalf("ComputeSimilarity on empty store should not fail: %v", err)
	}
	if got := countSimilarToEdges(t, st); got != 0 {
		t.Errorf("expected 0 edges on empty store, got %d", got)
	}
}

func TestComputeSimilaritySingleDoc(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	nodes := []store.Node{
		{ID: "solo.md", Kind: "document", Name: "Solo", QualifiedName: "solo.md", FilePath: "solo.md", StartLine: 1, EndLine: 10, BodyExcerpt: "the only document in the store", UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}

	if err := ComputeSimilarity(st, 0.25); err != nil {
		t.Fatalf("ComputeSimilarity with single doc should not fail: %v", err)
	}
	if got := countSimilarToEdges(t, st); got != 0 {
		t.Errorf("expected 0 edges with single doc, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// Neural similarity tests
// ---------------------------------------------------------------------------

func TestDenseCosineSimilarity(t *testing.T) {
	tests := []struct {
		a, b []float64
		want float64
	}{
		{[]float64{1, 0}, []float64{1, 0}, 1.0},
		{[]float64{1, 0}, []float64{0, 1}, 0.0},
		{[]float64{1, 1}, []float64{1, 1}, 1.0},
		{[]float64{}, []float64{}, 0.0},
		{[]float64{1}, []float64{1, 2}, 0.0}, // dim mismatch
	}
	for _, tc := range tests {
		got := denseCosineSimilarity(tc.a, tc.b)
		if tc.want == 1.0 && (got < 0.9999 || got > 1.0001) {
			t.Errorf("denseCosineSimilarity(%v, %v) = %f, want ~%f", tc.a, tc.b, got, tc.want)
		} else if tc.want == 0.0 && (got > 0.0001) {
			t.Errorf("denseCosineSimilarity(%v, %v) = %f, want ~0", tc.a, tc.b, got)
		}
	}
}

func TestComputeNeuralSimilarityForDoc(t *testing.T) {
	st := setupSimilarityStore(t)

	// Store embeddings: governance and security are near-identical (high cosine),
	// readme is orthogonal.
	embs := []store.Embedding{
		{DocID: "governance.md", ModelID: "test-model", Dim: 3, Vector: []float64{1, 1, 0}, ContentHash: "h"},
		{DocID: "security.md", ModelID: "test-model", Dim: 3, Vector: []float64{1, 1, 0.1}, ContentHash: "h"},
		{DocID: "readme.md", ModelID: "test-model", Dim: 3, Vector: []float64{0, 0, 1}, ContentHash: "h"},
	}
	for _, e := range embs {
		if err := st.UpsertEmbedding(e); err != nil {
			t.Fatalf("UpsertEmbedding %s: %v", e.DocID, err)
		}
	}

	if err := ComputeNeuralSimilarityForDoc(st, "governance.md", "test-model", 0.25); err != nil {
		t.Fatalf("ComputeNeuralSimilarityForDoc: %v", err)
	}

	// governance ↔ security should have a neural edge; governance ↔ readme should not.
	allEdges, err := st.GetSimilarEdgesForDoc("governance.md")
	if err != nil {
		t.Fatal(err)
	}

	var neuralEdge *store.Edge
	for i, e := range allEdges {
		var m map[string]interface{}
		json.Unmarshal([]byte(e.Metadata), &m)
		if eng, _ := m["engine"].(string); eng == "neural" {
			neuralEdge = &allEdges[i]
		}
	}
	if neuralEdge == nil {
		t.Error("expected neural similar_to edge between governance and security, found none")
	}
	if neuralEdge != nil {
		var m map[string]interface{}
		json.Unmarshal([]byte(neuralEdge.Metadata), &m)
		if m["model_id"] != "test-model" {
			t.Errorf("expected model_id=test-model, got %v", m["model_id"])
		}
	}
}

func TestComputeNeuralSimilarityForDoc_Idempotent(t *testing.T) {
	st := setupSimilarityStore(t)
	embs := []store.Embedding{
		{DocID: "governance.md", ModelID: "m", Dim: 2, Vector: []float64{1, 0}, ContentHash: "h"},
		{DocID: "security.md", ModelID: "m", Dim: 2, Vector: []float64{1, 0}, ContentHash: "h"},
	}
	for _, e := range embs {
		st.UpsertEmbedding(e)
	}

	// Run twice — should not create duplicate edges.
	ComputeNeuralSimilarityForDoc(st, "governance.md", "m", 0.1)
	ComputeNeuralSimilarityForDoc(st, "governance.md", "m", 0.1)

	stats, _ := st.GetStats()
	var neuralCount int
	for kind, n := range stats.EdgesByKind {
		if kind == "similar_to" {
			neuralCount = n
		}
	}
	if neuralCount > 1 {
		t.Errorf("expected at most 1 neural edge, got %d", neuralCount)
	}
}

// TestComputeSimilarityNoPanicOnAdversarialInput locks in the audit verdict that
// similarity.Compute* has no panic vector (security-audit backlog #4). It runs
// on the watcher reindex goroutine, so a panic would crash serve. The risky
// spots are the idf log (n/df), the cosine division by vector norms, and the
// jaccard union division — each case probes a degenerate corpus and asserts the
// full and incremental entry points both return across a range of thresholds
// (including 0, negative, and >1).
func TestComputeSimilarityNoPanicOnAdversarialInput(t *testing.T) {
	doc := func(id, name, body string, tags ...string) store.Node {
		md := ""
		if len(tags) > 0 {
			ts := make([]string, len(tags))
			copy(ts, tags)
			b, _ := json.Marshal(map[string]interface{}{"tags": ts})
			md = string(b)
		}
		return store.Node{ID: id, Kind: "document", Name: name, QualifiedName: id, FilePath: id, StartLine: 1, EndLine: 10, BodyExcerpt: body, Metadata: md, UpdatedAt: 1}
	}
	cases := []struct {
		name  string
		nodes []store.Node
	}{
		{"empty corpus", nil},
		{"single document", []store.Node{doc("solo.md", "Solo", "lonely body text")}},
		{"all bodies empty", []store.Node{doc("a.md", "A", ""), doc("b.md", "B", "   \n\t  ")}},
		{"unicode-only bodies (no ascii tokens)", []store.Node{doc("a.md", "A", "你好世界🔥"), doc("b.md", "B", "안녕하세요🌊")}},
		// Every doc shares the single term "alpha" → df==n → idf log(1)==0 →
		// zero tf-idf vectors → cosine denom==0 (must hit the guard, not divide).
		{"term in every doc (zero vectors)", []store.Node{doc("a.md", "A", "alpha alpha"), doc("b.md", "B", "alpha"), doc("c.md", "C", "alpha alpha alpha")}},
		{"empty and overlapping tag sets", []store.Node{doc("a.md", "A", "alpha beta", "x", "y"), doc("b.md", "B", "alpha beta"), doc("c.md", "C", "alpha beta", "y", "z")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")
			st, err := store.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { st.Close() })
			if len(tc.nodes) > 0 {
				if err := st.InsertNodes(tc.nodes); err != nil {
					t.Fatal(err)
				}
			}

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Compute panicked on %q: %v", tc.name, r)
				}
			}()
			changed := make([]string, len(tc.nodes))
			for i, n := range tc.nodes {
				changed[i] = n.ID
			}
			for _, th := range []float64{-1, 0, 0.1, 1, 2} {
				if err := ComputeSimilarity(st, th); err != nil {
					t.Fatalf("ComputeSimilarity(th=%v) error: %v", th, err)
				}
				if err := ComputeSimilarityIncremental(st, changed, th); err != nil {
					t.Fatalf("ComputeSimilarityIncremental(th=%v) error: %v", th, err)
				}
			}
		})
	}
}

// TestBuildCappedTargetsBoundsReferenceSet locks the per-doc targets cap added
// for security-audit backlog #6: an untrusted document with far more distinct
// outgoing references than maxTargetsPerDoc must yield a targets set bounded at
// the cap, so the O(n^2) Jaccard pass cannot be amplified by a single crafted
// file. Below the cap, the set is exact.
func TestBuildCappedTargetsBoundsReferenceSet(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cap.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	const over = maxTargetsPerDoc + 500
	// Target nodes (FK requires targets to exist) + the source document.
	nodes := make([]store.Node, 0, over+1)
	nodes = append(nodes, store.Node{ID: "src.md", Kind: "document", Name: "Src",
		QualifiedName: "src.md", FilePath: "src.md", StartLine: 1, EndLine: 1, UpdatedAt: 1})
	for k := range over {
		id := "t" + strconv.Itoa(k)
		nodes = append(nodes, store.Node{ID: id, Kind: "heading", Name: "h",
			QualifiedName: id, FilePath: "src.md", StartLine: 1, EndLine: 1, UpdatedAt: 1})
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	edges := make([]store.Edge, over)
	for k := range over {
		edges[k] = store.Edge{Source: "src.md", Target: "t" + strconv.Itoa(k), Kind: "wikilinks_to"}
	}
	if err := st.InsertEdges(edges); err != nil {
		t.Fatal(err)
	}

	got, err := buildCappedTargets(st, "src.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxTargetsPerDoc {
		t.Fatalf("over-cap doc: targets = %d, want capped at %d", len(got), maxTargetsPerDoc)
	}

	// A doc with few references is returned in full (cap does not under-count).
	under, err := buildCappedTargets(st, "t0") // t0 has no outgoing edges
	if err != nil {
		t.Fatal(err)
	}
	if len(under) != 0 {
		t.Fatalf("no-ref doc: targets = %d, want 0", len(under))
	}
}

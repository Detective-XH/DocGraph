package similarity

import (
	"encoding/json"
	"path/filepath"
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

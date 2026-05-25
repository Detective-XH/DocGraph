package similarity

import (
	"path/filepath"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

// setupIncrementalStore creates a 4-doc store: two topically similar pairs.
// governance↔security share policy/compliance terms; install↔quickstart share
// install/setup terms. readme is topically distinct from all.
func setupIncrementalStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "incr.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	nodes := []store.Node{
		{ID: "governance.md", Kind: "document", Name: "Governance", QualifiedName: "governance.md", FilePath: "governance.md", StartLine: 1, EndLine: 10, BodyExcerpt: "governance policy architecture security compliance requirements", UpdatedAt: 1},
		{ID: "security.md", Kind: "document", Name: "Security", QualifiedName: "security.md", FilePath: "security.md", StartLine: 1, EndLine: 10, BodyExcerpt: "security policy compliance audit vulnerability assessment", UpdatedAt: 1},
		{ID: "install.md", Kind: "document", Name: "Install", QualifiedName: "install.md", FilePath: "install.md", StartLine: 1, EndLine: 10, BodyExcerpt: "installation setup binary download prerequisites", UpdatedAt: 1},
		{ID: "quickstart.md", Kind: "document", Name: "Quickstart", QualifiedName: "quickstart.md", FilePath: "quickstart.md", StartLine: 1, EndLine: 10, BodyExcerpt: "quickstart setup install guide binary prerequisites", UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	return st
}

// TestIncrementalNoOp verifies that an empty changedDocIDs list is a no-op.
func TestIncrementalNoOp(t *testing.T) {
	st := setupIncrementalStore(t)
	if err := ComputeSimilarityIncremental(st, nil, 0.05); err != nil {
		t.Fatalf("incremental with nil changedDocIDs: %v", err)
	}
	if n := countSimilarToEdges(t, st); n != 0 {
		t.Errorf("expected 0 edges for no-op, got %d", n)
	}
}

// TestIncrementalAddNewDoc simulates adding a new doc to an existing corpus.
// Full similarity runs first; then a new doc is added and incremental runs.
// The new doc's edges should appear without touching existing pairs.
func TestIncrementalAddNewDoc(t *testing.T) {
	st := setupIncrementalStore(t)

	// Full run first to populate existing edges.
	if err := ComputeSimilarity(st, 0.05); err != nil {
		t.Fatalf("full ComputeSimilarity: %v", err)
	}
	edgesAfterFull := countSimilarToEdges(t, st)
	if edgesAfterFull == 0 {
		t.Fatal("expected at least 1 similar_to edge after full run")
	}

	// Add a new doc related to governance.
	newDoc := store.Node{
		ID: "compliance.md", Kind: "document", Name: "Compliance",
		QualifiedName: "compliance.md", FilePath: "compliance.md",
		StartLine: 1, EndLine: 10,
		BodyExcerpt: "compliance policy requirements governance security audit",
		UpdatedAt:   2,
	}
	if err := st.InsertNodes([]store.Node{newDoc}); err != nil {
		t.Fatal(err)
	}

	// Incremental: only compliance.md changed.
	if err := ComputeSimilarityIncremental(st, []string{"compliance.md"}, 0.05); err != nil {
		t.Fatalf("incremental: %v", err)
	}

	edgesAfterIncr := countSimilarToEdges(t, st)
	if edgesAfterIncr <= edgesAfterFull {
		t.Errorf("expected more edges after adding related doc (had %d, got %d)", edgesAfterFull, edgesAfterIncr)
	}
}

// TestIncrementalPreservesUnchangedPairs verifies that pairs not involving
// changed docs keep their edges intact after an incremental run.
func TestIncrementalPreservesUnchangedPairs(t *testing.T) {
	st := setupIncrementalStore(t)

	// Full run.
	if err := ComputeSimilarity(st, 0.05); err != nil {
		t.Fatalf("full ComputeSimilarity: %v", err)
	}

	// Check governance↔security pair exists.
	govEdges, err := st.GetOutgoingEdges("governance.md")
	if err != nil {
		t.Fatal(err)
	}
	var govSecEdge bool
	for _, e := range govEdges {
		if e.Kind == "similar_to" && (e.Target == "security.md" || e.Source == "security.md") {
			govSecEdge = true
		}
	}
	if !govSecEdge {
		// Also check incoming direction
		secEdges, _ := st.GetOutgoingEdges("security.md")
		for _, e := range secEdges {
			if e.Kind == "similar_to" && (e.Target == "governance.md" || e.Source == "governance.md") {
				govSecEdge = true
			}
		}
	}
	if !govSecEdge {
		t.Skip("governance↔security edge not created at this threshold — skipping preservation check")
	}

	// Run incremental for install.md only (unrelated to governance/security).
	if err := ComputeSimilarityIncremental(st, []string{"install.md"}, 0.05); err != nil {
		t.Fatalf("incremental: %v", err)
	}

	// governance↔security edge should still exist.
	govEdges2, err := st.GetOutgoingEdges("governance.md")
	if err != nil {
		t.Fatal(err)
	}
	var stillExists bool
	for _, e := range govEdges2 {
		if e.Kind == "similar_to" && (e.Target == "security.md" || e.Source == "security.md") {
			stillExists = true
		}
	}
	if !stillExists {
		secEdges2, _ := st.GetOutgoingEdges("security.md")
		for _, e := range secEdges2 {
			if e.Kind == "similar_to" && (e.Target == "governance.md" || e.Source == "governance.md") {
				stillExists = true
			}
		}
	}
	if !stillExists {
		t.Error("governance↔security similar_to edge was deleted by incremental run that only changed install.md")
	}
}

// TestIncrementalThresholdChangeFallback verifies that changing the threshold
// triggers a full rebuild (all existing edges recomputed).
func TestIncrementalThresholdChangeFallback(t *testing.T) {
	st := setupIncrementalStore(t)

	// First run at low threshold — should produce several edges.
	if err := ComputeSimilarityIncremental(st, []string{"governance.md", "security.md", "install.md", "quickstart.md"}, 0.01); err != nil {
		t.Fatalf("first incremental: %v", err)
	}
	edgesLow := countSimilarToEdges(t, st)

	// Second run at very high threshold — should produce fewer edges.
	// Even though changedDocIDs is just one file, threshold change triggers full rebuild.
	if err := ComputeSimilarityIncremental(st, []string{"governance.md"}, 0.99); err != nil {
		t.Fatalf("second incremental (high threshold): %v", err)
	}
	edgesHigh := countSimilarToEdges(t, st)

	if edgesHigh >= edgesLow {
		t.Errorf("expected fewer edges at threshold 0.99 than 0.01 (low=%d, high=%d)", edgesLow, edgesHigh)
	}
}

// TestIncrementalBailoutHalfCorpus verifies that when k > n/2 docs changed,
// ComputeSimilarityIncremental delegates to the full rebuild path.
// We verify this indirectly: full rebuild always clears and recomputes all edges.
func TestIncrementalBailoutHalfCorpus(t *testing.T) {
	st := setupIncrementalStore(t)

	// All 4 docs "changed" — k(4) > n/2(2) → full rebuild.
	changedAll := []string{"governance.md", "security.md", "install.md", "quickstart.md"}
	if err := ComputeSimilarityIncremental(st, changedAll, 0.05); err != nil {
		t.Fatalf("incremental (k>n/2): %v", err)
	}

	// Should have same result as full ComputeSimilarity.
	edgesIncr := countSimilarToEdges(t, st)

	st2dbPath := filepath.Join(t.TempDir(), "full.db")
	st2, err := store.Open(st2dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st2.Close() })
	nodes := []store.Node{
		{ID: "governance.md", Kind: "document", Name: "Governance", QualifiedName: "governance.md", FilePath: "governance.md", StartLine: 1, EndLine: 10, BodyExcerpt: "governance policy architecture security compliance requirements", UpdatedAt: 1},
		{ID: "security.md", Kind: "document", Name: "Security", QualifiedName: "security.md", FilePath: "security.md", StartLine: 1, EndLine: 10, BodyExcerpt: "security policy compliance audit vulnerability assessment", UpdatedAt: 1},
		{ID: "install.md", Kind: "document", Name: "Install", QualifiedName: "install.md", FilePath: "install.md", StartLine: 1, EndLine: 10, BodyExcerpt: "installation setup binary download prerequisites", UpdatedAt: 1},
		{ID: "quickstart.md", Kind: "document", Name: "Quickstart", QualifiedName: "quickstart.md", FilePath: "quickstart.md", StartLine: 1, EndLine: 10, BodyExcerpt: "quickstart setup install guide binary prerequisites", UpdatedAt: 1},
	}
	if err := st2.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	if err := ComputeSimilarity(st2, 0.05); err != nil {
		t.Fatalf("full ComputeSimilarity for reference: %v", err)
	}
	edgesFull := countSimilarToEdges(t, st2)

	if edgesIncr != edgesFull {
		t.Errorf("k>n/2 incremental should equal full rebuild: incremental=%d, full=%d", edgesIncr, edgesFull)
	}
}

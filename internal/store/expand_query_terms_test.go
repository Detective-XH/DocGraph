package store

import (
	"fmt"
	"strings"
	"testing"
)

// TestExpandQueryTermsCalibration is the quality gate for any refactor.
// Seed: heading "REST API Guide", definition "api-gateway", tag "api".
// Query "api" via LIKE '%api%' must find the heading and definition names
// and return their sub-terms. If a refactor loses these expansions, it
// has degraded recall and must not ship.
func TestExpandQueryTermsCalibration(t *testing.T) {
	st := tempStore(t)
	nodes := []Node{
		{ID: "guide.md#h1", Kind: "heading",
			Name: "REST API Guide", QualifiedName: "guide.md#h1",
			FilePath: "guide.md", StartLine: 1, EndLine: 5, Level: 1, UpdatedAt: 1},
		{ID: "guide.md#def0", Kind: "definition",
			Name: "api-gateway", QualifiedName: "guide.md#def0",
			FilePath: "guide.md", StartLine: 10, EndLine: 11, UpdatedAt: 1},
		{ID: "tag:api", Kind: "tag", Name: "api", QualifiedName: "tag:api",
			FilePath: "", StartLine: 0, EndLine: 0, UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}

	req := searchRequest{Terms: []string{"api"}}
	expanded := st.expandQueryTerms(req)

	got := make(map[string]bool, len(expanded))
	for _, term := range expanded {
		got[term] = true
	}

	// "api" must NOT appear — deduplicated against Terms.
	if got["api"] {
		t.Error(`returned "api" — must be deduplicated against Terms`)
	}
	// Minimum baseline — measured 2026-05-31 against main@1fa1a9b.
	// ["api-gateway", "rest", "guide"]. A refactored implementation may
	// return additional terms (⊇ is fine); a missing term is a recall
	// regression and must not ship.
	for _, want := range []string{"rest", "guide", "api-gateway"} {
		if !got[want] {
			t.Errorf("missing expected expansion %q; got %v", want, expanded)
		}
	}
	t.Logf("expanded: %v", expanded)
}

// TestExpandQueryTermsEmptyInput guards the nil fast-path.
func TestExpandQueryTermsEmptyInput(t *testing.T) {
	st := tempStore(t)
	if got := st.expandQueryTerms(searchRequest{}); got != nil {
		t.Errorf("expected nil for empty Terms, got %v", got)
	}
}

// TestExpandQueryTermsNoMatch asserts nil/empty when no node name matches.
func TestExpandQueryTermsNoMatch(t *testing.T) {
	st := tempStore(t)
	nodes := []Node{
		{ID: "a.md#h1", Kind: "heading", Name: "Completely Unrelated Heading",
			QualifiedName: "a.md#h1", FilePath: "a.md",
			StartLine: 1, EndLine: 5, Level: 1, UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	got := st.expandQueryTerms(searchRequest{Terms: []string{"xyzzy"}})
	if len(got) != 0 {
		t.Errorf("expected no expansions, got %v", got)
	}
}

// TestExpandQueryTermsLimitTruncation is the differential gate for the LIMIT+ORDER BY
// boundary. It seeds >12 heading nodes whose names all match "service", then asserts
// that Fix A's expansion ⊇ the LIKE-based expansion. If Fix A uses ORDER BY rank
// instead of ORDER BY length/name and drops a name LIKE would have kept, this test
// catches it.
//
// Phase 1 (before Fix A): logs the LIKE baseline and asserts LIMIT fires correctly.
// Phase 2 (after Fix A):  uncomment the ftsExpanded block to activate the ⊇ assertion.
func TestExpandQueryTermsLimitTruncation(t *testing.T) {
	st := tempStore(t)
	// Seed 20 headings all containing "service" with varying name lengths,
	// ensuring LIMIT 12 will truncate. Shorter names sort first by length(name).
	var nodes []Node
	for i := 0; i < 20; i++ {
		suffix := strings.Repeat(string(rune('a'+i)), i+1) // "a", "bb", "ccc", ...
		nodes = append(nodes, Node{
			ID:            fmt.Sprintf("svc%02d.md#h0", i),
			Kind:          "heading",
			Name:          "Service " + suffix, // length varies: 10, 11, 12, ...
			QualifiedName: fmt.Sprintf("svc%02d.md#h0", i),
			FilePath:      fmt.Sprintf("svc%02d.md", i),
			StartLine:     1, EndLine: 5, Level: 1, UpdatedAt: 1,
		})
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}

	req := searchRequest{Terms: []string{"service"}}

	// LIKE baseline: what does the current implementation return?
	likeExpanded := st.expandQueryTermsLike(req)

	// ⊇ assertion: Fix A's FTS expansion must be a superset of LIKE expansion.
	ftsExpanded := st.expandQueryTerms(req)
	ftsSet := make(map[string]bool)
	for _, term := range ftsExpanded {
		ftsSet[term] = true
	}
	for _, want := range likeExpanded {
		if !ftsSet[want] {
			t.Errorf("FTS expansion missing LIKE term %q; fts=%v like=%v", want, ftsExpanded, likeExpanded)
		}
	}
	t.Logf("FTS expansion (>12 matching corpus): %v", ftsExpanded)

	t.Logf("LIKE baseline (>12 matching corpus): %v", likeExpanded)
	t.Logf("Note: LIMIT fires (>12 nodes match); ORDER BY length/name selects the shortest 12 names")
}

package store

import (
	"math"
	"reflect"
	"testing"
)

// TestSearchBatchEquivalence is the correctness contract for the batched ranking
// loaders: every value the SearchWithOptions hot path now reads from a batch
// helper must equal what the per-candidate reference (GetGovernanceMetadata /
// GetResearchMetadata / GetFileHistory / graphSignals) returns for the same
// input. The ship case ("same scores, far fewer queries") rests entirely on this
// — a coarse end-to-end ordering test can miss a subtle count mismatch, so this
// asserts the per-candidate equality directly.
//
// The corpus deliberately exercises every keying path the batch must replicate:
//   - document candidates (key on Node.ID, which equals FilePath)
//   - heading candidates (key on FilePath; in/out by Node.ID)
//   - a document and one of its headings sharing a tag source (a.md): the
//     collision the batch fans a single GROUP BY row out to both
//   - present and absent governance/research/history rows (nil parity)
//   - tagged edges matching a Terms entry AND an ExpandedTerms entry
func TestSearchBatchEquivalence(t *testing.T) {
	st := tempStore(t)

	nodes := []Node{
		{ID: "a.md", Kind: "document", Name: "Alpha Doc", QualifiedName: "a.md", FilePath: "a.md", StartLine: 1, EndLine: 9, BodyExcerpt: "alpha", UpdatedAt: 1},
		{ID: "a.md#h1", Kind: "heading", Name: "Section One", QualifiedName: "a.md#h1", FilePath: "a.md", StartLine: 2, EndLine: 5, Level: 1, BodyExcerpt: "alpha", UpdatedAt: 1},
		{ID: "b.md", Kind: "document", Name: "Beta Doc", QualifiedName: "b.md", FilePath: "b.md", StartLine: 1, EndLine: 4, BodyExcerpt: "beta", UpdatedAt: 1},
		{ID: "c.md", Kind: "document", Name: "Gamma Doc", QualifiedName: "c.md", FilePath: "c.md", StartLine: 1, EndLine: 4, BodyExcerpt: "gamma", UpdatedAt: 1},
		{ID: "d.md", Kind: "document", Name: "Delta Doc", QualifiedName: "d.md", FilePath: "d.md", StartLine: 1, EndLine: 4, BodyExcerpt: "delta", UpdatedAt: 1},
		// A definition candidate (kind=definition) exercises the non-document
		// keying branch for a kind other than heading; FilePath "a.md" makes it a
		// THIRD candidate colliding on tag source "a.md" (with a.md + a.md#h1).
		{ID: "a.md#def1", Kind: "definition", Name: "Term One", QualifiedName: "a.md#def1", FilePath: "a.md", StartLine: 6, EndLine: 7, BodyExcerpt: "alpha", UpdatedAt: 1},
		// Tag nodes (targets of 'tagged' edges).
		{ID: "tag:alpha", Kind: "tag", Name: "alpha", QualifiedName: "tag:alpha", FilePath: "", StartLine: 0, EndLine: 0, UpdatedAt: 1},
		{ID: "tag:beta", Kind: "tag", Name: "beta", QualifiedName: "tag:beta", FilePath: "", StartLine: 0, EndLine: 0, UpdatedAt: 1},
		{ID: "tag:gamma", Kind: "tag", Name: "gamma", QualifiedName: "tag:gamma", FilePath: "", StartLine: 0, EndLine: 0, UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}

	edges := []Edge{
		// Reference graph: a.md gets incoming from b.md + c.md; a.md → b.md outgoing.
		{Source: "a.md", Target: "b.md", Kind: "references", Line: 1},
		{Source: "b.md", Target: "a.md", Kind: "references", Line: 1},
		{Source: "c.md", Target: "a.md", Kind: "wikilinks_to", Line: 1},
		{Source: "c.md", Target: "b.md", Kind: "embeds", Line: 2},
		// Heading-level edge (non-document keying): a.md#h1 has one incoming.
		{Source: "b.md", Target: "a.md#h1", Kind: "references", Line: 3},
		// Definition-level outgoing edge (non-document keying by ID): a.md#def1
		// has one outgoing. Targets b.md so it does not perturb a.md's incoming.
		{Source: "a.md#def1", Target: "b.md", Kind: "references", Line: 6},
		// Tagged edges. a.md is tagged alpha + beta (one in Terms, one in
		// ExpandedTerms). d.md is tagged gamma. b.md/c.md untagged.
		{Source: "a.md", Target: "tag:alpha", Kind: "tagged", Line: 0},
		{Source: "a.md", Target: "tag:beta", Kind: "tagged", Line: 0},
		{Source: "d.md", Target: "tag:gamma", Kind: "tagged", Line: 0},
	}
	if err := st.InsertEdges(edges); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// Metadata present on some nodes, absent on others (nil parity).
	if err := st.UpsertGovernanceMetadata("a.md", []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "sensitivity", Value: "public", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata a.md: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("c.md", []MetadataTuple{
		{Key: "status", Value: "draft", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata c.md: %v", err)
	}
	if err := st.UpsertResearchMetadata("a.md", []MetadataTuple{
		{Key: "confidence", Value: "high", ValueType: "string", Source: "frontmatter"},
		{Key: "source_type", Value: "primary", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertResearchMetadata a.md: %v", err)
	}
	if err := st.UpsertFileHistory(FileHistory{Path: "a.md", CommitCount: 12, FirstCommitAt: 100, LastCommitAt: 200, AuthorCount: 3, LastAuthor: "x", LastSubject: "s"}); err != nil {
		t.Fatalf("UpsertFileHistory a.md: %v", err)
	}
	if err := st.UpsertFileHistory(FileHistory{Path: "d.md", CommitCount: 1, FirstCommitAt: 50, LastCommitAt: 50, AuthorCount: 1, LastAuthor: "y", LastSubject: "t"}); err != nil {
		t.Fatalf("UpsertFileHistory d.md: %v", err)
	}

	// "alpha" in Terms, "beta"/"gamma" in ExpandedTerms — disjoint, as
	// expandQueryTerms guarantees in production (it dedups expansions against
	// Terms). "delta" is an unrelated term that matches no tag.
	req := searchRequest{
		Query:         "alpha",
		Terms:         []string{"alpha"},
		ExpandedTerms: []string{"beta", "gamma", "delta"},
	}

	cands := make([]*searchCandidate, 0, 6)
	for _, n := range nodes[:6] { // doc/heading/definition candidates (not the tag nodes)
		nn := n
		cands = append(cands, &searchCandidate{Node: nn})
	}

	// retrievalDocID oracle: assert the metadata key derivation INDEPENDENTLY of
	// retrievalDocID itself (the equivalence loop below feeds the same function to
	// both sides, so a wrong derivation would corrupt both equally and pass).
	wantDocID := map[string]string{
		"a.md": "a.md", "a.md#h1": "a.md", "b.md": "b.md",
		"c.md": "c.md", "d.md": "d.md", "a.md#def1": "a.md",
	}
	for _, c := range cands {
		if got := retrievalDocID(c.Node); got != wantDocID[c.Node.ID] {
			t.Errorf("retrievalDocID(%s) = %q, want %q", c.Node.ID, got, wantDocID[c.Node.ID])
		}
	}

	// Golden lock on the graph-score formula. The equivalence checks below verify
	// the COUNTS fed to applyGraphScore match the per-candidate path, but not the
	// formula constants applyGraphScore applies; this hard-codes the expected
	// score for fixed counts so a constant change (cap, weight, log base) fails
	// loudly. incoming=2 → min(ln3*3,12); outgoing=3 → min(ln4*1.25,5); tag=1 → 8.
	var gc searchCandidate
	applyGraphScore(&gc, 2, 3, 1)
	const wantGraphScore = 13.028704817404193
	if math.Abs(gc.Score-wantGraphScore) > 1e-9 {
		t.Errorf("applyGraphScore(2,3,1) = %v, want %v (graph-rerank formula changed)", gc.Score, wantGraphScore)
	}

	// --- guard: the corpus must produce non-zero signals, else "equivalence"
	// would be trivially true on an all-empty corpus. ---
	wIn, _, wTag, err := st.Searcher.graphSignals(req, nodes[0]) // a.md
	if err != nil {
		t.Fatalf("graphSignals a.md: %v", err)
	}
	// Document-level incoming joins on the TARGET node's file_path, so it counts
	// edges pointing at the doc AND at any heading in the same file: b.md→a.md,
	// c.md→a.md, and b.md→a.md#h1 all resolve to file_path a.md ⇒ 3. (This exact
	// doc-vs-heading keying difference is what the batch must replicate.)
	if wIn != 3 {
		t.Fatalf("corpus guard: a.md incoming want 3 (b.md→a.md, c.md→a.md, b.md→a.md#h1), got %d", wIn)
	}
	if wTag != 2 {
		t.Fatalf("corpus guard: a.md tagMatches want 2 (alpha+beta), got %d", wTag)
	}

	// --- metadata equivalence ---
	metaIDSet := map[string]struct{}{}
	pathSet := map[string]struct{}{}
	for _, c := range cands {
		metaIDSet[retrievalDocID(c.Node)] = struct{}{}
		pathSet[c.Node.FilePath] = struct{}{}
	}
	govBatch, err := st.Searcher.getGovernanceMetadataBatch(setKeys(metaIDSet))
	if err != nil {
		t.Fatalf("getGovernanceMetadataBatch: %v", err)
	}
	resBatch, err := st.Searcher.getResearchMetadataBatch(setKeys(metaIDSet))
	if err != nil {
		t.Fatalf("getResearchMetadataBatch: %v", err)
	}
	histBatch, err := st.Searcher.getFileHistoryBatch(setKeys(pathSet))
	if err != nil {
		t.Fatalf("getFileHistoryBatch: %v", err)
	}
	graphBatch, err := st.Searcher.graphSignalsBatch(req, cands)
	if err != nil {
		t.Fatalf("graphSignalsBatch: %v", err)
	}

	for _, c := range cands {
		id := retrievalDocID(c.Node)

		wantGov, err := st.GetGovernanceMetadata(id)
		if err != nil {
			t.Fatalf("GetGovernanceMetadata(%s): %v", id, err)
		}
		if !reflect.DeepEqual(wantGov, govBatch[id]) {
			t.Errorf("governance mismatch for %s (id=%s):\n  per-candidate: %+v\n  batch:         %+v", c.Node.ID, id, wantGov, govBatch[id])
		}

		wantRes, err := st.GetResearchMetadata(id)
		if err != nil {
			t.Fatalf("GetResearchMetadata(%s): %v", id, err)
		}
		if !reflect.DeepEqual(wantRes, resBatch[id]) {
			t.Errorf("research mismatch for %s (id=%s):\n  per-candidate: %+v\n  batch:         %+v", c.Node.ID, id, wantRes, resBatch[id])
		}

		wantHist, err := st.GetFileHistory(c.Node.FilePath)
		if err != nil {
			t.Fatalf("GetFileHistory(%s): %v", c.Node.FilePath, err)
		}
		if !reflect.DeepEqual(wantHist, histBatch[c.Node.FilePath]) {
			t.Errorf("history mismatch for %s (path=%s):\n  per-candidate: %+v\n  batch:         %+v", c.Node.ID, c.Node.FilePath, wantHist, histBatch[c.Node.FilePath])
		}

		gotSig := graphBatch[c.Node.ID]
		inc, out, tag, err := st.Searcher.graphSignals(req, c.Node)
		if err != nil {
			t.Fatalf("graphSignals(%s): %v", c.Node.ID, err)
		}
		if gotSig.incoming != inc || gotSig.outgoing != out || gotSig.tagMatches != tag {
			t.Errorf("graph signal mismatch for %s:\n  per-candidate: in=%d out=%d tag=%d\n  batch:         in=%d out=%d tag=%d",
				c.Node.ID, inc, out, tag, gotSig.incoming, gotSig.outgoing, gotSig.tagMatches)
		}
	}

	// --- collision case made explicit: a.md (document, tag source = its ID) and
	// a.md#h1 (heading, tag source = its FilePath "a.md") share one tag source, so
	// both must carry a.md's tagMatches=2. ---
	if graphBatch["a.md"].tagMatches != 2 || graphBatch["a.md#h1"].tagMatches != 2 {
		t.Errorf("shared tag-source collision: a.md=%d a.md#h1=%d, both want 2",
			graphBatch["a.md"].tagMatches, graphBatch["a.md#h1"].tagMatches)
	}
}

package store

import "testing"

// TestGraphRerankActivatesOnLinkDensity verifies Item 4(b): the graph-aware
// reranking (applyGraphReranking → graphSignals) is wired but contributes
// nothing on a link-sparse corpus, and proves it DOES activate — flipping
// result order — once a corpus is link-dense.
//
// Construction isolates the graph signal as the sole cause:
//   - aaa.md and zzz.md are identical in every field the text ranker reads
//     (Name, BodyExcerpt, section text) AND their IDs are equal-length (6 chars
//     → equal trigram count), so even the bm25 doc-length normalization over the
//     qualified_name column matches → a genuine tie. (Unequal-length IDs like
//     alpha.md/zeta.md shift bm25 by ~0.01 and would defeat the isolation.)
//   - On a tie, search.go sorts by FilePath ascending, so aaa.md precedes zzz.md
//     with no edges (baseline).
//   - 50 linker docs are inserted UP FRONT (present in both searches), so the
//     bm25 corpus is unchanged between baseline and treatment. The ONLY thing
//     that changes is the wikilinks_to edges pointed at zzz.md. graphSignals is
//     the sole scorer that reads edges, so any order change is attributable to
//     it alone. The linkers' content is disjoint from the query, so they never
//     surface as candidates.
//
// If 50 incoming refs flip zzz.md above aaa.md, the feature is verified. A
// flip at incoming=50 (boost ≈ min(log1p(50)*3, 12) ≈ 11.8, near the cap) is an
// honest link-dense corpus, not a manufactured one.
func TestGraphRerankActivatesOnLinkDensity(t *testing.T) {
	st := tempStore(t)

	// Two query-matching docs, identical in every text-ranked field. IDs/paths
	// differ but contain no query term, so they add nothing to the text score.
	const body = "telemetry metrics overview dashboard"
	twins := []Node{
		{ID: "aaa.md", Kind: "document", Name: "Telemetry Guide", QualifiedName: "aaa.md",
			FilePath: "aaa.md", StartLine: 1, EndLine: 5, BodyExcerpt: body, UpdatedAt: 1},
		{ID: "zzz.md", Kind: "document", Name: "Telemetry Guide", QualifiedName: "zzz.md",
			FilePath: "zzz.md", StartLine: 1, EndLine: 5, BodyExcerpt: body, UpdatedAt: 1},
	}
	if err := st.InsertNodes(twins); err != nil {
		t.Fatalf("InsertNodes twins: %v", err)
	}
	if err := st.UpsertSectionChunks([]SectionChunk{
		sectionChunk("aaa.md", "aaa.md", "h1", "doc", "", body, 1, 5),
		sectionChunk("zzz.md", "zzz.md", "h1", "doc", "", body, 1, 5),
	}); err != nil {
		t.Fatalf("UpsertSectionChunks: %v", err)
	}

	// 50 linker docs, content disjoint from the query. Inserted up front so the
	// corpus is identical across both searches; only their edges are added later.
	const linkers = 50
	linkerNodes := make([]Node, linkers)
	for i := range linkerNodes {
		id := linkerName(i)
		linkerNodes[i] = Node{ID: id, Kind: "document", Name: "Orchard Note",
			QualifiedName: id, FilePath: id, StartLine: 1, EndLine: 3,
			BodyExcerpt: "unrelated orchard banana filler content", UpdatedAt: 1}
	}
	if err := st.InsertNodes(linkerNodes); err != nil {
		t.Fatalf("InsertNodes linkers: %v", err)
	}

	// Baseline: no edges. Tie → FilePath tie-break → aaa.md before zzz.md.
	base, err := st.Search("telemetry", "", 10)
	if err != nil {
		t.Fatalf("baseline Search: %v", err)
	}
	ia, iz := indexOfDoc(base, "aaa.md"), indexOfDoc(base, "zzz.md")
	if ia < 0 || iz < 0 {
		t.Fatalf("baseline: both docs must be present, got sparse=%d dense=%d", ia, iz)
	}
	// The isolation depends on the twins scoring identically with no edges. Assert
	// the tie directly (equal rank, not a magic value) so a future scoring change
	// that breaks the premise fails loudly here rather than silently.
	if base[ia].Rank != base[iz].Rank {
		t.Fatalf("baseline: twins must tie to isolate the graph signal, got aaa=%.6f zzz=%.6f",
			base[ia].Rank, base[iz].Rank)
	}
	if ia >= iz {
		t.Fatalf("baseline: expected aaa.md before zzz.md on a tie (sparse=%d, dense=%d)", ia, iz)
	}

	// Treatment: 50 docs now wikilinks_to zzz.md. Only edges changed.
	edges := make([]Edge, linkers)
	for i := range edges {
		edges[i] = Edge{Source: linkerName(i), Target: "zzz.md", Kind: "wikilinks_to"}
	}
	if err := st.InsertEdges(edges); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	treat, err := st.Search("telemetry", "", 10)
	if err != nil {
		t.Fatalf("treatment Search: %v", err)
	}
	ja, jz := indexOfDoc(treat, "aaa.md"), indexOfDoc(treat, "zzz.md")
	if ja < 0 || jz < 0 {
		t.Fatalf("treatment: both docs must be present, got sparse=%d dense=%d", ja, jz)
	}
	if jz >= ja {
		t.Fatalf("graph rerank did NOT activate: zzz.md has %d incoming wikilinks_to but did "+
			"not overtake aaa.md (sparse=%d, dense=%d). The feature is wired but ineffective.",
			linkers, ja, jz)
	}
	t.Logf("graph rerank verified: %d incoming refs flipped zzz.md from #%d→#%d (sparse #%d→#%d)",
		linkers, iz, jz, ia, ja)
}

func linkerName(i int) string {
	return "link" + itoa2(i) + ".md"
}

// itoa2 renders i as a zero-padded 2-digit string so linker IDs sort stably and
// stay clear of the twin docs' names.
func itoa2(i int) string {
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

func indexOfDoc(results []SearchResult, id string) int {
	for i, r := range results {
		if r.Node.ID == id {
			return i
		}
	}
	return -1
}

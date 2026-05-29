package similarity

import (
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/Detective-XH/docgraph/internal/store"
)

// Backlog #6 (security audit): quantify the O(n^2) pairwise cost of
// ComputeSimilarity (the full-rebuild path taken on first index, --force, or
// >half-changed). Skipped in normal runs; opt in with:
//
//	DOCGRAPH_SCALING=1 go test -run TestSimilarityScaling -timeout 600s ./internal/similarity/
//
// It measures wall time, peak heap delta, and similar_to edge count for three
// adversarial shapes:
//
//	A sparse  — distinct vocab per doc, default threshold → few edges (pure n^2 CPU)
//	B dense   — heavy vocab overlap → ~all C(n,2) pairs match → O(n^2) edges in RAM + DB
//	C targets — few docs, each with a huge outgoing-edge (targets) set → jaccard lever
func TestSimilarityScaling(t *testing.T) {
	if os.Getenv("DOCGRAPH_SCALING") == "" {
		t.Skip("set DOCGRAPH_SCALING=1 to run the O(n^2) scaling measurement")
	}

	// A 500-byte excerpt is the hard cap across all formats (parser/extractor),
	// so per-doc token vectors are bounded. We fill to that cap.
	const excerptCap = 500

	// Shared vocabulary pool. Dense scenario draws all docs from a small pool so
	// DF is high and cosine scores clear the threshold for most pairs.
	denseVocab := []string{"policy", "security", "compliance", "audit", "governance", "control", "risk", "review", "process", "standard"}
	// Sparse scenario gives each doc its own block of unique terms.
	sparseTerm := func(doc, k int) string { return fmt.Sprintf("u%dx%d", doc, k) }

	fillExcerpt := func(words func(k int) string) string {
		s := ""
		for k := 0; len(s) < excerptCap; k++ {
			w := words(k)
			if len(s)+len(w)+1 > excerptCap {
				break
			}
			if s != "" {
				s += " "
			}
			s += w
		}
		return s
	}

	measure := func(name string, build func(st *store.Store) int, threshold float64) {
		dbPath := t.TempDir() + "/scale.db"
		st, err := store.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		n := build(st)

		runtime.GC()
		var m0 runtime.MemStats
		runtime.ReadMemStats(&m0)

		// Sample live resident heap (HeapInuse) during the call and keep the
		// peak — this is the honest memory cost, unlike TotalAlloc which counts
		// freed garbage too. Poll fast enough to catch the pre-InsertEdges spike.
		var peakHeap uint64
		done := make(chan struct{})
		go func() {
			var ms runtime.MemStats
			for {
				select {
				case <-done:
					return
				default:
					runtime.ReadMemStats(&ms)
					if ms.HeapInuse > peakHeap {
						peakHeap = ms.HeapInuse
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
		}()

		start := time.Now()
		if err := ComputeSimilarity(st, threshold); err != nil {
			t.Fatalf("%s: ComputeSimilarity: %v", name, err)
		}
		elapsed := time.Since(start)
		close(done)
		var m1 runtime.MemStats
		runtime.ReadMemStats(&m1)

		stats, _ := st.GetStats()
		edges := stats.EdgesByKind["similar_to"]
		churnMB := float64(m1.TotalAlloc-m0.TotalAlloc) / (1 << 20) // cumulative, incl. freed
		peakMB := float64(peakHeap) / (1 << 20)                     // resident peak (honest)
		t.Logf("%-22s n=%-7d edges=%-10d wall=%-12s peakHeap=%8.1fMB churn=%9.1fMB",
			name, n, edges, elapsed, peakMB, churnMB)
	}

	_ = denseVocab

	// Scenario A: sparse. Each doc has unique vocab → near-zero cosine → few edges.
	// Isolates the raw n^2 comparison cost from edge-accumulation memory.
	for _, n := range []int{500, 1000, 2000, 4000} {
		nn := n
		measure("A.sparse", func(st *store.Store) int {
			nodes := make([]store.Node, nn)
			for i := range nn {
				ex := fillExcerpt(func(k int) string { return sparseTerm(i, k) })
				nodes[i] = store.Node{ID: fmt.Sprintf("d%d.md", i), Kind: "document",
					Name: fmt.Sprintf("Doc %d", i), QualifiedName: fmt.Sprintf("d%d.md", i),
					FilePath: fmt.Sprintf("d%d.md", i), StartLine: 1, EndLine: 10,
					BodyExcerpt: ex, UpdatedAt: 1}
			}
			if err := st.InsertNodes(nodes); err != nil {
				t.Fatal(err)
			}
			return nn
		}, 0.25)
	}

	// Scenario B: dense worst case. Docs are split into 2 groups; within a group
	// all excerpts are identical (cosine = 1.0), across groups disjoint. A term's
	// DF = group size = n/2 < n, so idf = log(2) > 0 (avoids the "term in every
	// doc → idf 0 → zero vector" degeneracy). Result: ~2·C(n/2,2) ≈ n²/4 edges
	// all held in RAM, then bulk-written to DB. This is the memory blow-up shape.
	for _, n := range []int{500, 1000, 2000, 4000} {
		nn := n
		measure("B.dense", func(st *store.Store) int {
			nodes := make([]store.Node, nn)
			for i := range nn {
				g := i % 2
				ex := fillExcerpt(func(k int) string { return fmt.Sprintf("g%dw%d", g, k) })
				nodes[i] = store.Node{ID: fmt.Sprintf("d%d.md", i), Kind: "document",
					Name: "Doc", QualifiedName: fmt.Sprintf("d%d.md", i),
					FilePath: fmt.Sprintf("d%d.md", i), StartLine: 1, EndLine: 10,
					BodyExcerpt: ex, UpdatedAt: 1}
			}
			if err := st.InsertNodes(nodes); err != nil {
				t.Fatal(err)
			}
			return nn
		}, 0.25)
	}

	// Scenario C: pathological targets. Few docs, but each carries a large
	// outgoing-edge set. NOTE: similarity's targets come from GetEdgesBySource,
	// which filters to references/wikilinks_to/related_to/embeds (NOT contains),
	// so the lever is a doc with hundreds of thousands of [[wikilinks]], not
	// headings. jaccard iterates set `a` with no swap-to-smaller, so each of the
	// C(n,2) pairs pays O(|targets|). All docs point at the SAME target nodes so
	// both the intersection work and the resulting score (rs=1.0) are maximal.
	for _, tcount := range []int{50000, 200000} {
		tc := tcount
		const docs = 30
		measure("C.targets", func(st *store.Store) int {
			// Shared target nodes (FK requires targets to exist as nodes;
			// in the real pipeline these are heading nodes).
			tnodes := make([]store.Node, tc)
			for k := range tc {
				tnodes[k] = store.Node{ID: fmt.Sprintf("t%d", k), Kind: "heading",
					Name: "h", QualifiedName: fmt.Sprintf("t%d", k),
					FilePath: "shared.md", StartLine: 1, EndLine: 1, UpdatedAt: 1}
			}
			if err := st.InsertNodes(tnodes); err != nil {
				t.Fatal(err)
			}
			nodes := make([]store.Node, docs)
			for i := range docs {
				nodes[i] = store.Node{ID: fmt.Sprintf("d%d.md", i), Kind: "document",
					Name: "Doc", QualifiedName: fmt.Sprintf("d%d.md", i),
					FilePath: fmt.Sprintf("d%d.md", i), StartLine: 1, EndLine: 10,
					BodyExcerpt: "shared term", UpdatedAt: 1}
			}
			if err := st.InsertNodes(nodes); err != nil {
				t.Fatal(err)
			}
			for i := range docs {
				edges := make([]store.Edge, tc)
				for k := range tc {
					edges[k] = store.Edge{Source: fmt.Sprintf("d%d.md", i),
						Target: fmt.Sprintf("t%d", k), Kind: "wikilinks_to"}
				}
				if err := st.InsertEdges(edges); err != nil {
					t.Fatal(err)
				}
			}
			return docs
		}, 0.25)
	}
}

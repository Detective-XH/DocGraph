package similarity

import (
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/Detective-XH/docgraph/internal/store"
)

// pollPeakHeap spawns a goroutine that samples HeapInuse every 20ms until the
// returned stop channel is closed, then returns a pointer to the observed peak.
// It captures the live resident heap (HeapInuse) rather than TotalAlloc so the
// measurement reflects the honest high-water mark, not cumulative churn.
func pollPeakHeap(stop <-chan struct{}) *uint64 {
	var peak uint64
	go func() {
		var ms runtime.MemStats
		for {
			select {
			case <-stop:
				return
			default:
				runtime.ReadMemStats(&ms)
				if ms.HeapInuse > peak {
					peak = ms.HeapInuse
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
	}()
	return &peak
}

// measureScenario opens a fresh DB, calls build to populate it, then runs
// ComputeSimilarity at the given threshold while sampling heap and wall time.
// Results are logged to t.
func measureScenario(t *testing.T, name string, build func(st *store.Store) int, threshold float64) {
	t.Helper()
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

	done := make(chan struct{})
	peakHeap := pollPeakHeap(done)

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
	peakMB := float64(*peakHeap) / (1 << 20)                    // resident peak (honest)
	t.Logf("%-22s n=%-7d edges=%-10d wall=%-12s peakHeap=%8.1fMB churn=%9.1fMB",
		name, n, edges, elapsed, peakMB, churnMB)
}

// scalingFillExcerpt builds an excerpt by appending word(k) tokens until the
// 500-byte cap (the hard parser limit) is reached, then returns the result.
func scalingFillExcerpt(word func(k int) string) string {
	const excerptCap = 500
	s := ""
	for k := 0; len(s) < excerptCap; k++ {
		w := word(k)
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

// buildSparseCorpus inserts nn documents each with a unique vocabulary block
// (no shared terms → near-zero cosine → few edges). Returns nn.
func buildSparseCorpus(t *testing.T, st *store.Store, nn int) int {
	t.Helper()
	nodes := make([]store.Node, nn)
	for i := range nn {
		ex := scalingFillExcerpt(func(k int) string { return fmt.Sprintf("u%dx%d", i, k) })
		nodes[i] = store.Node{ID: fmt.Sprintf("d%d.md", i), Kind: "document",
			Name: fmt.Sprintf("Doc %d", i), QualifiedName: fmt.Sprintf("d%d.md", i),
			FilePath: fmt.Sprintf("d%d.md", i), StartLine: 1, EndLine: 10,
			BodyExcerpt: ex, UpdatedAt: 1}
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	return nn
}

// buildDenseCorpus inserts nn documents split into 2 groups with identical
// vocabulary per group and disjoint across groups (cosine 1.0 within, 0 across).
// Returns nn.
func buildDenseCorpus(t *testing.T, st *store.Store, nn int) int {
	t.Helper()
	nodes := make([]store.Node, nn)
	for i := range nn {
		g := i % 2
		ex := scalingFillExcerpt(func(k int) string { return fmt.Sprintf("g%dw%d", g, k) })
		nodes[i] = store.Node{ID: fmt.Sprintf("d%d.md", i), Kind: "document",
			Name: "Doc", QualifiedName: fmt.Sprintf("d%d.md", i),
			FilePath: fmt.Sprintf("d%d.md", i), StartLine: 1, EndLine: 10,
			BodyExcerpt: ex, UpdatedAt: 1}
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	return nn
}

// buildTargetsCorpus inserts 30 docs each pointing at tc shared target nodes
// via wikilinks_to, producing an adversarial Jaccard load. Returns 30.
func buildTargetsCorpus(t *testing.T, st *store.Store, tc int) int {
	t.Helper()
	const docs = 30
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
}

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

	// Scenario A: sparse. Each doc has unique vocab → near-zero cosine → few edges.
	// Isolates the raw n^2 comparison cost from edge-accumulation memory. The
	// n=8000 point exists to show P2(a) inverted-index pruning's asymptotic win:
	// unique vocab → every posting has length 1 → zero candidates → near-constant
	// time, while the full scan keeps growing ~4×/doubling.
	for _, n := range []int{500, 1000, 2000, 4000, 8000} {
		nn := n
		measureScenario(t, "A.sparse", func(st *store.Store) int { return buildSparseCorpus(t, st, nn) }, 0.25)
	}

	// Scenario B: dense worst case. Docs are split into 2 groups; within a group
	// all excerpts are identical (cosine = 1.0), across groups disjoint. A term's
	// DF = group size = n/2 < n, so idf = log(2) > 0 (avoids the "term in every
	// doc → idf 0 → zero vector" degeneracy). Result: ~2·C(n/2,2) ≈ n²/4 edges
	// all held in RAM, then bulk-written to DB. This is the memory blow-up shape.
	for _, n := range []int{500, 1000, 2000, 4000} {
		nn := n
		measureScenario(t, "B.dense", func(st *store.Store) int { return buildDenseCorpus(t, st, nn) }, 0.25)
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
		measureScenario(t, "C.targets", func(st *store.Store) int { return buildTargetsCorpus(t, st, tc) }, 0.25)
	}
}

package similarity

import (
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

// BenchmarkIncrementalRepeated models the serve watcher steady state: a large
// corpus held open in one *Store, reindexed every tick with a small change.
// Each iteration mutates k changed docs' content and calls
// ComputeSimilarityIncremental, which re-tokenizes ALL docs to rebuild the
// global IDF every run.
//
// Reject-evidence for P2(d) item 1 (the "cache tokenized features" idea): a CPU
// profile of this path attributes tokenize at <0.5% of cum time — it does not
// register on the profile at all. The dominant per-tick costs are
// buildCappedTargets' per-doc DB query and the pairwise cosine scoring, neither
// of which a token cache touches. A perfect cache could save <0.5%, far under
// the ~15% keep gate, so the cache was measured-and-rejected (and would need an
// unbounded package-level map + LRU to be safe — added complexity, not a
// simplification). Reproduce:
//
//	go test -run x -bench BenchmarkIncrementalRepeated -benchtime 40x \
//	  -cpuprofile /tmp/incr.prof ./internal/similarity/
//	go tool pprof -top -cum /tmp/incr.prof   # tokenize absent; DB+scoring dominate
//
// Corpus size via DG_BENCH_N (default 2000); changed docs via DG_BENCH_K (1).
func BenchmarkIncrementalRepeated(b *testing.B) {
	n := envInt("DG_BENCH_N", 2000)
	k := envInt("DG_BENCH_K", 1)

	st, err := store.Open(b.TempDir() + "/bench.db")
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()

	// Moderate vocab overlap: a shared pool so DF is non-trivial and some
	// edges form, but each doc draws a distinct window so it isn't all-dense.
	const poolSize = 400
	pool := make([]string, poolSize)
	for i := range pool {
		pool[i] = fmt.Sprintf("term%d", i)
	}
	excerpt := func(seed int) string {
		s := ""
		for w := range 60 {
			if s != "" {
				s += " "
			}
			s += pool[(seed*7+w*13)%poolSize]
		}
		return s
	}

	nodes := make([]store.Node, n)
	for i := range nodes {
		nodes[i] = store.Node{ID: fmt.Sprintf("d%d.md", i), Kind: "document",
			Name: fmt.Sprintf("Doc %d", i), QualifiedName: fmt.Sprintf("d%d.md", i),
			FilePath: fmt.Sprintf("d%d.md", i), StartLine: 1, EndLine: 10,
			BodyExcerpt: excerpt(i), UpdatedAt: 1}
	}
	if err := st.InsertNodes(nodes); err != nil {
		b.Fatal(err)
	}
	// Prime: one full compute so steady-state incrementals have edges to update.
	if err := ComputeSimilarity(st, 0.25); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for it := range b.N {
		changed := make([]string, k)
		upd := make([]store.Node, k)
		for j := range k {
			id := fmt.Sprintf("d%d.md", (it*k+j)%n)
			changed[j] = id
			// Mutate content so a token cache sees a miss for this doc.
			upd[j] = store.Node{ID: id, Kind: "document", Name: id,
				QualifiedName: id, FilePath: id, StartLine: 1, EndLine: 10,
				BodyExcerpt: excerpt(it*1000 + j), UpdatedAt: int64(2 + it)}
		}
		if err := st.InsertNodes(upd); err != nil {
			b.Fatal(err)
		}
		if err := ComputeSimilarityIncremental(st, changed, 0.25); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkJaccard measures the swap-to-smaller guard for jaccard. The guard
// only does work when |a| != |b|; the skewed sub-bench is where any win shows,
// the symmetric one is the no-op control. maxTargetsPerDoc caps real sets at
// 10000 and real docs carry tens-to-hundreds of targets, so the absolute win
// is bounded by construction — this quantifies it honestly.
func BenchmarkJaccard(b *testing.B) {
	mkset := func(size, offset int) map[string]bool {
		m := make(map[string]bool, size)
		for i := range size {
			m[fmt.Sprintf("t%d", offset+i)] = true
		}
		return m
	}
	cases := []struct {
		name string
		a, b map[string]bool
	}{
		// Skewed: large `a`, small `b`, ~half overlap. Iterating `a` (current)
		// pays O(|a|); iterating `b` (guard) pays O(|b|).
		{"skewed_10000x50", mkset(10000, 0), mkset(50, 5)},
		{"skewed_1000x20", mkset(1000, 0), mkset(20, 5)},
		{"skewed_200x100", mkset(200, 0), mkset(100, 5)},
		// Symmetric control: guard is a no-op here.
		{"symmetric_500x500", mkset(500, 0), mkset(500, 250)},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			var sink float64
			for b.Loop() {
				sink += jaccard(c.a, c.b)
			}
			_ = sink
		})
	}
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

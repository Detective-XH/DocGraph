package workspace

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Detective-XH/docgraph/internal/store"
)

// ---------------------------------------------------------------------------
// Workspace query-serving (read-path) benchmark.
//
// BenchmarkQueryWorkspaceSearch measures the SERIAL fan-out + merge-sort cost
// of (*Workspace).SearchWithOptions: it loops every project's store serially,
// runs each project's SearchWithOptions at a per-project cap of limit*2,
// annotates nodes, concatenates, and re-sorts the union by Rank. This on-CPU
// cost is profile-visible and is the reason the workspace path is benched
// separately from the single-store path.
//
// Gated behind DG_QUERY_BENCH (same gate as internal/tools/query_bench_test.go)
// so the normal suite skips it at zero CI cost. Run WITHOUT -race.
//
// Saturation: w.SearchWithOptions sets each project's Limit to limit*2 (=40 for
// a top-level Limit of 20), so each project's candidate cap clamps to 200. For
// the fan-out cost to be realistic every project must saturate its OWN cap, so
// each project corpus is sized so its both-query-token set (i%5==0) exceeds
// ~200 docs. With wsBenchDocsPerProj=1200 that set is ~240 docs per project.
// ---------------------------------------------------------------------------

const (
	wsBenchProjects    = 4
	wsBenchDocsPerProj = 1200
	wsBenchQuery       = "kubernetes deployment"
	wsBenchNodesPerDoc = 11 // 1 document + ~10 headings
)

// wsVocab is a fixed vocabulary for deterministic synthetic text. It excludes
// the two query tokens so selectivity is controlled by explicit injection.
var wsVocab = []string{
	"service", "cluster", "node", "pod", "container", "registry", "image", "volume",
	"network", "ingress", "egress", "policy", "config", "secret", "namespace", "quota",
	"resource", "limit", "request", "scale", "replica", "rollout", "rollback", "canary",
	"pipeline", "build", "artifact", "release", "stage", "environment", "promote", "approve",
	"monitor", "metric", "alert", "dashboard", "trace", "span", "latency", "throughput",
	"storage", "backup", "restore", "snapshot", "retention", "archive", "compaction", "index",
	"query", "schema", "migration", "transaction", "lock", "consistency", "replication", "shard",
	"document", "section", "heading", "reference", "citation", "wikilink", "anchor", "outline",
	"summary", "abstract", "overview", "background", "detail", "appendix", "glossary", "footnote",
	"gateway", "proxy", "upstream", "downstream", "retry", "timeout", "circuit", "failover",
}

func wsBenchProjCount(def int) int {
	if v := os.Getenv("DG_QUERY_BENCH_PROJECTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func wsBenchDocCount(def int) int {
	if v := os.Getenv("DG_QUERY_BENCH_DOCS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// genProjectStore deterministically populates one project store with nDocs
// synthetic docs. seedOffset varies the rand stream per project so the projects
// are not byte-identical, while remaining reproducible. Returns the node count.
func genProjectStore(tb testing.TB, st *store.Store, nDocs, seedOffset int) int {
	tb.Helper()
	rng := rand.New(rand.NewSource(int64(42 + seedOffset)))
	nowUnix := time.Now().UTC().Unix()

	nodes := make([]store.Node, 0, nDocs*wsBenchNodesPerDoc)
	chunks := make([]store.SectionChunk, 0, nDocs*wsBenchNodesPerDoc)
	edges := make([]store.Edge, 0, nDocs*5)

	sentence := func(n int) string {
		out := ""
		for i := 0; i < n; i++ {
			if i > 0 {
				out += " "
			}
			out += wsVocab[rng.Intn(len(wsVocab))]
		}
		return out
	}

	for i := 0; i < nDocs; i++ {
		path := fmt.Sprintf("docs/doc%04d.md", i)
		docID := path

		// Both query tokens co-located in i%5==0 docs so each project saturates
		// its own candidate cap (per-project Limit=40 → candidateLimit=200).
		var hot string
		switch {
		case i%5 == 0:
			hot = "kubernetes deployment orchestration"
		case i%7 == 0:
			hot = "deployment pipeline notes"
		default:
			hot = sentence(4)
		}
		docBody := hot + " " + sentence(18)

		nodes = append(nodes, store.Node{
			ID: docID, Kind: "document", Name: fmt.Sprintf("Doc %04d", i),
			QualifiedName: docID, FilePath: path, StartLine: 1, EndLine: 200,
			BodyExcerpt: docBody, UpdatedAt: nowUnix,
		})
		chunks = append(chunks, store.SectionChunk{
			NodeID: docID, FilePath: path, StartLine: 1, EndLine: 200,
			ContentHash: fmt.Sprintf("ch-%d", i), SectionHash: fmt.Sprintf("sh-%d", i),
			Text: docBody + " " + sentence(25),
		})

		nHeadings := wsBenchNodesPerDoc - 1
		line := 5
		for hIdx := 0; hIdx < nHeadings; hIdx++ {
			hID := fmt.Sprintf("%s#h%d", path, hIdx)
			start, end := line, line+15
			line = end + 1
			headBody := sentence(20)
			if i%5 == 0 && hIdx == 0 {
				headBody = "kubernetes deployment " + headBody
			}
			nodes = append(nodes, store.Node{
				ID: hID, Kind: "heading", Name: fmt.Sprintf("Heading %d-%d", i, hIdx),
				QualifiedName: hID, FilePath: path, StartLine: start, EndLine: end,
				Level: 1 + hIdx%3, BodyExcerpt: headBody, UpdatedAt: nowUnix,
			})
			chunks = append(chunks, store.SectionChunk{
				NodeID: hID, FilePath: path, StartLine: start, EndLine: end,
				ContentHash: fmt.Sprintf("ch-%d-%d", i, hIdx),
				SectionHash: fmt.Sprintf("sh-%d-%d", i, hIdx),
				HeadingPath: fmt.Sprintf("Heading %d-%d", i, hIdx), Text: headBody,
			})
		}

		// A few cross-doc edges so the graph-density rerank has input.
		if nDocs > 1 {
			for r := 0; r < 3; r++ {
				tgt := fmt.Sprintf("docs/doc%04d.md", rng.Intn(nDocs))
				if tgt != docID {
					edges = append(edges, store.Edge{Source: docID, Target: tgt, Kind: "references", Line: 10 + r})
				}
			}
		}
	}

	if err := st.InsertNodes(nodes); err != nil {
		tb.Fatalf("InsertNodes: %v", err)
	}
	if err := st.UpsertSectionChunks(chunks); err != nil {
		tb.Fatalf("UpsertSectionChunks: %v", err)
	}
	if err := st.InsertEdges(edges); err != nil {
		tb.Fatalf("InsertEdges: %v", err)
	}
	return len(nodes)
}

// buildBenchWorkspace constructs a Workspace with nProj populated project
// stores directly — no file indexing — so the benchmark measures only the
// query fan-out, not disk I/O or parsing.
func buildBenchWorkspace(tb testing.TB, nProj, docsPerProj int) *Workspace {
	tb.Helper()
	root := tb.TempDir()
	w := &Workspace{Root: root}
	totalNodes := 0
	for p := 0; p < nProj; p++ {
		name := fmt.Sprintf("proj%02d", p)
		dbPath := filepath.Join(tb.TempDir(), name+".db")
		st, err := store.Open(dbPath)
		if err != nil {
			tb.Fatalf("store.Open: %v", err)
		}
		tb.Cleanup(func() { st.Close() })
		totalNodes += genProjectStore(tb, st, docsPerProj, p)
		w.Projects = append(w.Projects, &Project{Name: name, Path: filepath.Join(root, name), Store: st})
	}
	tb.Logf("workspace bench: %d projects x %d docs = %d docs, %d nodes total",
		nProj, docsPerProj, nProj*docsPerProj, totalNodes)
	return w
}

func BenchmarkQueryWorkspaceSearch(b *testing.B) {
	if os.Getenv("DG_QUERY_BENCH") == "" {
		b.Skip("set DG_QUERY_BENCH=1 to run query-serving benchmarks")
	}
	nProj := wsBenchProjCount(wsBenchProjects)
	docsPerProj := wsBenchDocCount(wsBenchDocsPerProj)
	w := buildBenchWorkspace(b, nProj, docsPerProj)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := w.SearchWithOptions(store.SearchOptions{Query: wsBenchQuery, Limit: 20}); err != nil {
			b.Fatal(err)
		}
	}
}

// TestQueryWorkspaceBenchCorpusIsSearchable is the validity gate for the
// workspace bench: it proves each project saturates its own candidate cap and
// the merged fan-out returns a non-empty result, with a small ungated corpus so
// it runs in the normal suite.
func TestQueryWorkspaceBenchCorpusIsSearchable(t *testing.T) {
	gated := os.Getenv("DG_QUERY_BENCH") != ""
	// Tiny corpus by default so this runs in the normal suite at near-zero cost
	// (a 3600-doc ungated build would add ~8s to every `go test ./...`). The
	// gated branch uses the full per-project corpus, the only size the benchmark
	// actually runs on — and the only size that saturates each project's cap.
	nProj := 3
	docsPerProj := 250
	if gated {
		nProj = wsBenchProjCount(wsBenchProjects)
		docsPerProj = wsBenchDocCount(wsBenchDocsPerProj)
	}
	w := buildBenchWorkspace(t, nProj, docsPerProj)

	// Per-project searchability: each project's store returns real hits (proves
	// FTS is populated in every project). Saturation (>=160 at Limit=200) is a
	// property of the FULL per-project corpus — a 250-doc project cannot produce
	// 160 distinct candidates at this selectivity — so that bar is gated.
	probe, err := w.Projects[0].Store.SearchWithOptions(store.SearchOptions{Query: wsBenchQuery, Limit: 200})
	if err != nil {
		t.Fatalf("per-project probe: %v", err)
	}
	t.Logf("per-project SearchWithOptions(limit=200) returned %d results (saturation probe, gated assert=%v)", len(probe), gated)
	if len(probe) < 10 {
		t.Fatalf("each project must be searchable: want >=10 hits, got %d (FTS not populated?)", len(probe))
	}
	if gated && len(probe) < 160 {
		t.Fatalf("each project must saturate its own candidate cap: want >=160 at limit=200, got %d", len(probe))
	}

	// Merged fan-out returns a full, non-empty page.
	merged, err := w.SearchWithOptions(store.SearchOptions{Query: wsBenchQuery, Limit: 20})
	if err != nil {
		t.Fatalf("workspace SearchWithOptions: %v", err)
	}
	t.Logf("workspace SearchWithOptions(limit=20) returned %d merged results", len(merged))
	if len(merged) < 20 {
		t.Fatalf("merged fan-out should fill the page: want 20, got %d", len(merged))
	}
}

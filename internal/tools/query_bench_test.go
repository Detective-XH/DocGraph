package tools

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

// ---------------------------------------------------------------------------
// Query-serving (read-path) benchmark harness.
//
// These benchmarks profile the WARM steady-state cost of DocGraph's hot query
// tools — what a long-running `serve` process actually spends per request once
// the SQLite page cache is warm. They are GATED behind DG_QUERY_BENCH so the
// normal `go test ./...` suite skips them at zero CI cost (mirrors the
// DG_WS_GIT_BENCH gate in internal/workspace/gitbench_test.go).
//
// Run WITHOUT -race: the race detector distorts timing and these measure
// on-CPU ns/op + allocs/op, not concurrency correctness.
//
// Corpus design — selectivity is the validity-critical knob. The benchmark
// query is "kubernetes deployment". newSearchRequest clamps the candidate cap
// to limit*8 in [40,200]; the FTS collectors (collectNodeCandidates and
// collectSectionCandidates in search_collectors.go) each run an OR query
// (buildFTSQuery joins terms with " OR ", search_sql.go:22) capped at that
// limit. To exercise the FULL bounded rerank loop — the worst realistic case —
// genQueryCorpus co-locates BOTH query tokens in ~20% of docs (i%5==0), which
// saturates the candidate cap under either AND or OR semantics, and scatters
// single tokens elsewhere purely for corpus realism.
// ---------------------------------------------------------------------------

const (
	// queryBenchDocs mirrors the real corpus order of magnitude (~4162 docs /
	// ~50k nodes). Override with DG_QUERY_BENCH_DOCS.
	queryBenchDocs = 3000

	// queryBenchQuery is the benchmark query. Both tokens are >=3 chars so the
	// FTS5 trigram tokenizer indexes them (sub-trigram terms hit the LIKE
	// fallback instead — a different, non-representative path).
	queryBenchQuery = "kubernetes deployment"

	// queryHubDoc is a well-connected document that ~queryHubFanIn other docs
	// reference and wikilink TO. handleGraphFacade operation=incoming/impact
	// traverse INCOMING edges (target==node.ID), so the corpus needs a doc with
	// real in-degree or those benches measure an empty edge set.
	queryHubDoc   = "docs/doc0000.md"
	queryHubFanIn = 40

	// querySimilarHub is a document with a real similarity NEIGHBORHOOD:
	// querySimilarFanOut distinct similar_to edges point at it. handleSimilar is
	// benched on this doc — benching it on a chain-endpoint doc with a single
	// edge would measure the cost of rendering ONE result, not a realistic
	// neighborhood. Kept distinct from queryHubDoc so the reference fan-in and
	// the similarity fan-out are independent.
	querySimilarHub    = "docs/doc0001.md"
	querySimilarFanOut = 30
)

// queryVocab is a fixed ~150-word vocabulary used to synthesize realistic
// heading/section text deterministically. It deliberately excludes the two
// query tokens ("kubernetes", "deployment") so selectivity is controlled
// solely by the explicit injection in genQueryCorpus.
var queryVocab = []string{
	"service", "cluster", "node", "pod", "container", "registry", "image", "volume",
	"network", "ingress", "egress", "policy", "config", "secret", "namespace", "quota",
	"resource", "limit", "request", "scale", "replica", "rollout", "rollback", "canary",
	"pipeline", "build", "artifact", "release", "stage", "environment", "promote", "approve",
	"monitor", "metric", "alert", "dashboard", "trace", "span", "latency", "throughput",
	"error", "budget", "incident", "postmortem", "runbook", "oncall", "escalate", "page",
	"storage", "backup", "restore", "snapshot", "retention", "archive", "compaction", "index",
	"query", "schema", "migration", "transaction", "lock", "consistency", "replication", "shard",
	"auth", "token", "session", "credential", "rotation", "audit", "compliance", "governance",
	"document", "section", "heading", "reference", "citation", "wikilink", "anchor", "outline",
	"summary", "abstract", "overview", "background", "detail", "appendix", "glossary", "footnote",
	"queue", "broker", "topic", "partition", "consumer", "producer", "offset", "commit",
	"cache", "eviction", "invalidate", "warmup", "prefetch", "hitrate", "miss", "tier",
	"gateway", "proxy", "loadbalancer", "upstream", "downstream", "retry", "timeout", "circuit",
	"deploy", "provision", "terraform", "ansible", "helm", "chart", "manifest", "template",
	"observability", "logging", "structured", "correlation", "context", "propagation", "sampling", "exporter",
	"resilience", "failover", "redundancy", "availability", "durability", "partition", "quorum", "consensus",
	"workflow", "orchestration", "scheduler", "trigger", "cron", "event", "webhook", "callback",
	"feature", "flag", "toggle", "experiment", "variant", "cohort", "rollforward", "guardrail",
}

// queryBenchDocCount returns the effective corpus size, honoring the
// DG_QUERY_BENCH_DOCS override and falling back to the default on any parse
// error or non-positive value.
func queryBenchDocCount(def int) int {
	if v := os.Getenv("DG_QUERY_BENCH_DOCS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// genDocHeadings generates ~8–12 heading nodes and their section chunks for
// one document. Extracted from genQueryCorpus to lower its cyclomatic complexity.
// docIdx is the document's loop index (used for selectivity seeding on hIdx==0).
// nowUnix is the shared timestamp for all generated nodes.
func genDocHeadings(path string, docIdx int, rng *rand.Rand, nowUnix int64, sentence func(int) string) ([]store.Node, []store.SectionChunk) {
	nHeadings := 8 + rng.Intn(5)
	nodes := make([]store.Node, 0, nHeadings)
	chunks := make([]store.SectionChunk, 0, nHeadings)
	line := 5
	for hIdx := range nHeadings {
		hID := fmt.Sprintf("%s#h%d", path, hIdx)
		headName := fmt.Sprintf("%s %s", capitalize(queryVocab[rng.Intn(len(queryVocab))]), capitalize(queryVocab[rng.Intn(len(queryVocab))]))
		start := line
		end := line + 20
		line = end + 1
		// Seed a fraction of headings with the hot tokens too, so heading-level
		// FTS rows participate (the real corpus has the query terms in headings).
		headBody := sentence(25)
		if docIdx%5 == 0 && hIdx == 0 {
			headBody = "kubernetes deployment " + headBody
		}
		nodes = append(nodes, store.Node{
			ID: hID, Kind: "heading", Name: headName, QualifiedName: hID,
			FilePath: path, StartLine: start, EndLine: end, Level: 1 + hIdx%3,
			BodyExcerpt: headBody, UpdatedAt: nowUnix,
		})
		chunks = append(chunks, store.SectionChunk{
			NodeID: hID, FilePath: path, StartLine: start, EndLine: end,
			ContentHash: fmt.Sprintf("ch-%d-%d", docIdx, hIdx),
			SectionHash: fmt.Sprintf("sh-%d-%d", docIdx, hIdx),
			HeadingPath: headName, Text: headBody,
		})
	}
	return nodes, chunks
}

// genDocEdges builds the outgoing graph edges for one document:
//   - ~3 references + ~2 wikilinks to random other docs (graph density / rerank data)
//   - hub fan-in: docs 1..queryHubFanIn each reference + wikilink the hub doc
//   - consecutive similar_to chain (score 0.82) for the precomputed-edge path
//   - similarity fan-out from the tail of the corpus to querySimilarHub
func genDocEdges(docID string, docIdx, nDocs int, rng *rand.Rand) []store.Edge {
	var edges []store.Edge
	// Outgoing graph edges to other docs (~3 references + ~2 wikilinks) so
	// graph traversal, the graph-density rerank, and outgoing links all have
	// data. Targets are document IDs (other docs).
	if nDocs > 1 {
		for r := range 3 {
			tgt := fmt.Sprintf("docs/doc%04d.md", rng.Intn(nDocs))
			if tgt == docID {
				continue
			}
			edges = append(edges, store.Edge{Source: docID, Target: tgt, Kind: "references", Line: 10 + r})
		}
		for w := range 2 {
			tgt := fmt.Sprintf("docs/doc%04d.md", rng.Intn(nDocs))
			if tgt == docID {
				continue
			}
			edges = append(edges, store.Edge{Source: docID, Target: tgt, Kind: "wikilinks_to", Line: 20 + w})
		}
	}
	// Hub fan-in: the first queryHubFanIn docs (other than the hub) each
	// reference + wikilink the hub, giving it a guaranteed in-degree so
	// operation=incoming/impact have real data to traverse.
	if docIdx != 0 && docIdx <= queryHubFanIn {
		edges = append(edges,
			store.Edge{Source: docID, Target: queryHubDoc, Kind: "references", Line: 30},
			store.Edge{Source: docID, Target: queryHubDoc, Kind: "wikilinks_to", Line: 31},
		)
	}
	// similar_to edges (TF-IDF engine, score >= 0.75) between consecutive
	// docs so handleSimilar's precomputed-edge path has data AND the drift
	// audit's duplicate/conflict detection (SimilarityMin 0.75) has input.
	if docIdx > 0 {
		prev := fmt.Sprintf("docs/doc%04d.md", docIdx-1)
		edges = append(edges, store.Edge{
			Source: prev, Target: docID, Kind: "similar_to",
			Metadata: `{"score":0.82,"engine":"tfidf"}`,
		})
	}
	// Similarity fan-out: the last querySimilarFanOut docs each link to
	// querySimilarHub with a similar_to edge (varied scores), giving that hub
	// a real neighborhood so handleSimilar measures rendering MANY results,
	// not one. Sourced from the tail of the corpus so the fan-out is present
	// for any corpus large enough to hold it (incl. the small smoke corpus),
	// not a fixed offset that only exists at full size.
	if nDocs >= querySimilarFanOut+2 && docIdx >= nDocs-querySimilarFanOut && docID != querySimilarHub {
		score := 0.75 + float64(docIdx%20)/100.0 // 0.75..0.94, all >= SimilarityMin
		edges = append(edges, store.Edge{
			Source: docID, Target: querySimilarHub, Kind: "similar_to",
			Metadata: fmt.Sprintf(`{"score":%.2f,"engine":"tfidf"}`, score),
		})
	}
	return edges
}

// insertCorpusSlices writes the fully-built node/chunk/edge/history slices into
// st, fataling tb on the first error. Extracted from genQueryCorpus to move the
// insert error-handling branches out of the main generator.
func insertCorpusSlices(tb testing.TB, st *store.Store, nodes []store.Node, chunks []store.SectionChunk, edges []store.Edge, histories []store.FileHistory) {
	tb.Helper()
	if err := st.InsertNodes(nodes); err != nil {
		tb.Fatalf("InsertNodes: %v", err)
	}
	if err := st.UpsertSectionChunks(chunks); err != nil {
		tb.Fatalf("UpsertSectionChunks: %v", err)
	}
	if err := st.InsertEdges(edges); err != nil {
		tb.Fatalf("InsertEdges: %v", err)
	}
	for _, h := range histories {
		if err := st.UpsertFileHistory(h); err != nil {
			tb.Fatalf("UpsertFileHistory: %v", err)
		}
	}
}

// upsertCorpusGovernance writes governance status metadata on every 13th doc so
// the drift audit's status-driven finders (stale_review, superseded_referenced,
// non_canonical) have input. Extracted from genQueryCorpus to reduce its
// cyclomatic complexity.
func upsertCorpusGovernance(tb testing.TB, st *store.Store, nDocs int, statuses []string) {
	tb.Helper()
	for i := 0; i < nDocs; i += 13 {
		docID := fmt.Sprintf("docs/doc%04d.md", i)
		status := statuses[(i/13)%len(statuses)]
		if err := st.UpsertGovernanceMetadata(docID, []store.MetadataTuple{
			{Key: "status", Value: status, ValueType: "string", Source: "frontmatter"},
		}); err != nil {
			tb.Fatalf("UpsertGovernanceMetadata: %v", err)
		}
	}
}

// genQueryCorpus deterministically populates st with nDocs synthetic documents
// modelled on the real corpus (~12 nodes/doc, section chunks for FTS, a graph
// of references/wikilinks, varied git history for the churn rerank, governance
// status + similar_to edges + stale git for the drift audit). It returns the
// total node count inserted.
//
// Deterministic: a fixed-seed math/rand source drives every choice, so the
// corpus structure (which docs get which tokens, edges, headings) is
// byte-reproducible across runs. Timestamps are the one exception and
// DELIBERATELY relative to wall-clock now (captured once below): the drift
// audit defaults AsOf=now() and classifies doc.stale_by_git against AsOf-365d,
// so "fresh" docs must stay newer than a moving cutoff and "stale" docs older
// than it. Pinning an absolute base would let fresh docs drift past the cutoff
// over months and silently flip every doc to stale. Absolute timestamp values
// affect no ranking (ranks are relative) and no measurement — only the
// stale/fresh classification, which the relative base keeps invariant.
func genQueryCorpus(tb testing.TB, st *store.Store, nDocs int) int {
	tb.Helper()
	rng := rand.New(rand.NewSource(42))

	nodes := make([]store.Node, 0, nDocs*13)
	chunks := make([]store.SectionChunk, 0, nDocs*12)
	edges := make([]store.Edge, 0, nDocs*6)
	histories := make([]store.FileHistory, 0, nDocs)

	now := time.Now().UTC()
	nowUnix := now.Unix()
	// staleUnix is older than the doc.stale_by_git default cutoff (AsOf-365d),
	// so docs assigned this timestamp surface as doc.stale_by_git findings.
	staleUnix := now.AddDate(-2, 0, 0).Unix()
	statuses := []string{"approved", "draft", "review", "superseded"}

	sentence := func(n int) string {
		words := make([]string, n)
		for i := range words {
			words[i] = queryVocab[rng.Intn(len(queryVocab))]
		}
		return joinWords(words)
	}

	for i := range nDocs {
		path := fmt.Sprintf("docs/doc%04d.md", i)
		docID := path

		// Selectivity injection. Base saturating set: every i%5==0 doc gets BOTH
		// query tokens co-located, so the candidate cap saturates under AND or
		// OR. Single-token scatter elsewhere is purely additional realism.
		var hot string
		switch {
		case i%5 == 0:
			hot = "kubernetes deployment orchestration" // both tokens
		case i%7 == 0:
			hot = "deployment pipeline notes" // single-token realism scatter
		case i%11 == 0:
			hot = "kubernetes cluster notes" // single-token realism scatter
		default:
			hot = sentence(4)
		}

		docBody := hot + " " + sentence(20)
		nodes = append(nodes, store.Node{
			ID: docID, Kind: "document", Name: fmt.Sprintf("Doc %04d", i),
			QualifiedName: docID, FilePath: path, StartLine: 1, EndLine: 400,
			Level: 0, BodyExcerpt: docBody, UpdatedAt: nowUnix,
		})
		// Document-level section chunk carries the hot tokens into
		// section_chunks_fts so the section collector also matches.
		chunks = append(chunks, store.SectionChunk{
			NodeID: docID, FilePath: path, StartLine: 1, EndLine: 400,
			ContentHash: fmt.Sprintf("ch-%d", i), SectionHash: fmt.Sprintf("sh-%d", i),
			HeadingPath: "", Text: docBody + " " + sentence(30),
		})

		// ~8–12 heading nodes per doc (realistic ~12 nodes/doc with the document
		// + chunks). Headings get their own section chunks with real text.
		hNodes, hChunks := genDocHeadings(path, i, rng, nowUnix, sentence)
		nodes = append(nodes, hNodes...)
		chunks = append(chunks, hChunks...)

		edges = append(edges, genDocEdges(docID, i, nDocs, rng)...)

		// File history with varied commit/author counts so the #30 churn rerank
		// is exercised. A fraction get a stale last_commit_at so doc.stale_by_git
		// fires in the drift audit.
		last := nowUnix
		if i%9 == 0 {
			last = staleUnix
		}
		histories = append(histories, store.FileHistory{
			Path: path, CommitCount: 1 + rng.Intn(40), AuthorCount: 1 + rng.Intn(8),
			FirstCommitAt: staleUnix, LastCommitAt: last,
			LastAuthor: "dev", LastSubject: "update " + path,
		})
	}

	insertCorpusSlices(tb, st, nodes, chunks, edges, histories)
	upsertCorpusGovernance(tb, st, nDocs, statuses)

	return len(nodes)
}

func joinWords(words []string) string {
	out := ""
	for i, w := range words {
		if i > 0 {
			out += " "
		}
		out += w
	}
	return out
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-32) + s[1:] // ASCII lowercase vocab only
}

// requireQueryBench skips a benchmark unless DG_QUERY_BENCH is set.
func requireQueryBench(b *testing.B) {
	b.Helper()
	if os.Getenv("DG_QUERY_BENCH") == "" {
		b.Skip("set DG_QUERY_BENCH=1 to run query-serving benchmarks")
	}
}

// setupQueryBenchStore builds + populates a store ONCE for a benchmark. The
// returned store is warm (the populate transactions touched every page) and is
// shared read-only across the timed loop and any b.Run variants.
func setupQueryBenchStore(b *testing.B) (*handler, *store.Store, int, int) {
	b.Helper()
	h, st := newBenchHandler(b)
	nDocs := queryBenchDocCount(queryBenchDocs)
	nNodes := genQueryCorpus(b, st, nDocs)
	b.Logf("query bench corpus: %d docs, %d nodes", nDocs, nNodes)
	return h, st, nDocs, nNodes
}

// newBenchHandler mirrors newTestHandler (embeddings_test.go) but accepts a
// testing.TB so benchmarks (*testing.B) and the smoke test (*testing.T) can
// share it — newTestHandler's signature is fixed to *testing.T and cannot take
// a *testing.B. Same two-line construction: temp-file store + temp project root.
func newBenchHandler(tb testing.TB) (*handler, *store.Store) {
	tb.Helper()
	dbPath := filepath.Join(tb.TempDir(), "bench.db")
	st, err := store.Open(dbPath)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { st.Close() })
	return &handler{store: st, projectRoot: tb.TempDir()}, st
}

// ---------------------------------------------------------------------------
// Store-level benchmarks
// ---------------------------------------------------------------------------

func BenchmarkQuerySearchStore(b *testing.B) {
	requireQueryBench(b)
	_, st, _, _ := setupQueryBenchStore(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := st.Searcher.SearchWithOptions(store.SearchOptions{Query: queryBenchQuery, Limit: 20}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkQueryDriftStore(b *testing.B) {
	requireQueryBench(b)
	_, st, _, _ := setupQueryBenchStore(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := st.GetDriftFindings(store.DriftAuditOpts{}); err != nil {
			b.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------
// Handler-level benchmarks
// ---------------------------------------------------------------------------

func BenchmarkQueryContextHandler(b *testing.B) {
	requireQueryBench(b)
	h, _, _, _ := setupQueryBenchStore(b)

	b.Run("summary", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			res, err := callTool(h, h.handleContext, map[string]any{"task": queryBenchQuery})
			if err != nil || res.IsError {
				b.Fatalf("handleContext summary: err=%v res=%v", err, res)
			}
		}
	})

	b.Run("context_pack", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			res, err := callTool(h, h.handleContext, map[string]any{
				"task":   queryBenchQuery,
				"format": "context_pack",
			})
			if err != nil || res.IsError {
				b.Fatalf("handleContext context_pack: err=%v res=%v", err, res)
			}
		}
	})
}

func BenchmarkQueryDriftAuditHandler(b *testing.B) {
	requireQueryBench(b)
	h, _, _, _ := setupQueryBenchStore(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		res, err := callTool(h, h.handleContext, map[string]any{
			"task":   queryBenchQuery,
			"format": "drift_audit",
		})
		if err != nil || res.IsError {
			b.Fatalf("handleContext drift_audit: err=%v res=%v", err, res)
		}
	}
}

func BenchmarkQuerySearchHandler(b *testing.B) {
	requireQueryBench(b)
	h, _, _, _ := setupQueryBenchStore(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		res, err := callTool(h, h.handleSearch, map[string]any{"query": queryBenchQuery, "limit": 20})
		if err != nil || res.IsError {
			b.Fatalf("handleSearch: err=%v res=%v", err, res)
		}
	}
}

func BenchmarkQuerySimilarHandler(b *testing.B) {
	requireQueryBench(b)
	h, _, _, _ := setupQueryBenchStore(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		res, err := callTool(h, h.handleSimilar, map[string]any{"document": querySimilarHub})
		if err != nil || res.IsError {
			b.Fatalf("handleSimilar: err=%v res=%v", err, res)
		}
	}
}

func BenchmarkQueryGraphHandler(b *testing.B) {
	requireQueryBench(b)
	h, _, _, _ := setupQueryBenchStore(b)

	b.Run("incoming", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			res, err := callTool(h, h.handleGraphFacade, map[string]any{
				"operation": "incoming",
				"document":  queryHubDoc,
				"limit":     20,
			})
			if err != nil || res.IsError {
				b.Fatalf("graph incoming: err=%v res=%v", err, res)
			}
		}
	})

	b.Run("impact", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			res, err := callTool(h, h.handleGraphFacade, map[string]any{
				"operation": "impact",
				"document":  queryHubDoc,
				"depth":     3,
			})
			if err != nil || res.IsError {
				b.Fatalf("graph impact: err=%v res=%v", err, res)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Smoke validation — proves the harness measures a POPULATED fast path, not an
// empty one. Ungated with a small corpus so it runs in the normal suite and
// guards the whole harness; the gated branch re-checks at full corpus size.
// ---------------------------------------------------------------------------

func TestQueryBenchCorpusIsSearchable(t *testing.T) {
	// Gated like the benchmarks (DG_QUERY_BENCH). Building the corpus + FTS index
	// is too heavy for the normal `go test -race -timeout 120s ./...` suite, so it
	// is compile-checked there but only RUN as the harness validity gate when the
	// benches run — mirroring the DG_WS_BENCH / DG_WS_GIT_BENCH harnesses, which
	// likewise carry no ungated smoke.
	if os.Getenv("DG_QUERY_BENCH") == "" {
		t.Skip("set DG_QUERY_BENCH=1 to run the query-bench corpus validation")
	}
	nDocs := queryBenchDocCount(queryBenchDocs)
	h, st := newBenchHandler(t)
	nNodes := genQueryCorpus(t, st, nDocs)
	t.Logf("corpus: %d docs, %d nodes", nDocs, nNodes)

	// 1. Store search returns a realistic, non-empty result set at Limit=20.
	//    This is the searchability bar (proves FTS is populated) and is the
	//    cheap ungated regression guard for the whole harness.
	res20, err := st.Searcher.SearchWithOptions(store.SearchOptions{Query: queryBenchQuery, Limit: 20})
	if err != nil {
		t.Fatalf("SearchWithOptions limit=20: %v", err)
	}
	t.Logf("SearchWithOptions(limit=20) returned %d results", len(res20))
	if len(res20) < 10 {
		t.Fatalf("expected >=10 results at limit=20, got %d — corpus not searchable (FTS not populated?)", len(res20))
	}

	// 2. Candidate-cap saturation: at Limit=200 the candidate cap clamps to 200
	//    and each FTS collector returns up to that many rows. A near-200 result
	//    count is the only API-visible proof the full bounded rerank loop runs.
	//    This is a property of the FULL bench corpus the instrument runs on — a
	//    200-doc corpus structurally cannot produce 160 distinct candidates at
	//    this selectivity (~0.58 candidates/doc), so the assertion is gated.
	res200, err := st.Searcher.SearchWithOptions(store.SearchOptions{Query: queryBenchQuery, Limit: 200})
	if err != nil {
		t.Fatalf("SearchWithOptions limit=200: %v", err)
	}
	t.Logf("SearchWithOptions(limit=200) returned %d results (saturation probe)", len(res200))
	if len(res200) < 160 {
		t.Fatalf("expected >=160 results at limit=200 (rerank loop must run at full width), got %d", len(res200))
	}

	// 3. Hub doc has real incoming fan-in for the graph benches.
	inEdges, err := st.GetIncomingEdges(queryHubDoc)
	if err != nil {
		t.Fatalf("GetIncomingEdges(%s): %v", queryHubDoc, err)
	}
	t.Logf("graph hub %s incoming edges: %d", queryHubDoc, len(inEdges))
	if len(inEdges) < 20 {
		t.Fatalf("graph hub must have >=20 incoming edges for graph benches, got %d", len(inEdges))
	}

	// 3b. Similar hub has a real similarity neighborhood for handleSimilar — a
	//     chain-endpoint doc would have a single edge and measure rendering ONE
	//     result, not a neighborhood.
	simEdges, err := st.GetSimilarEdgesForDoc(querySimilarHub)
	if err != nil {
		t.Fatalf("GetSimilarEdgesForDoc(%s): %v", querySimilarHub, err)
	}
	t.Logf("similar hub %s similar_to edges: %d", querySimilarHub, len(simEdges))
	if len(simEdges) < 20 {
		t.Fatalf("similar hub must have >=20 similar_to edges for handleSimilar bench, got %d", len(simEdges))
	}

	// 4. Each hot handler returns a non-error, non-empty result.
	checks := []struct {
		name string
		args map[string]any
		fn   func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	}{
		{"handleSearch", map[string]any{"query": queryBenchQuery, "limit": 20}, h.handleSearch},
		{"handleContext", map[string]any{"task": queryBenchQuery}, h.handleContext},
		{"handleContext/context_pack", map[string]any{"task": queryBenchQuery, "format": "context_pack"}, h.handleContext},
		{"handleContext/drift_audit", map[string]any{"task": queryBenchQuery, "format": "drift_audit"}, h.handleContext},
		{"handleSimilar", map[string]any{"document": querySimilarHub}, h.handleSimilar},
		{"handleGraphFacade/incoming", map[string]any{"operation": "incoming", "document": queryHubDoc, "limit": 20}, h.handleGraphFacade},
		{"handleGraphFacade/impact", map[string]any{"operation": "impact", "document": queryHubDoc, "depth": 3}, h.handleGraphFacade},
	}
	for _, c := range checks {
		res, err := callTool(h, c.fn, c.args)
		if err != nil {
			t.Fatalf("%s returned err: %v", c.name, err)
		}
		if res.IsError {
			t.Fatalf("%s returned tool error: %s", c.name, extractText(res))
		}
		if len(extractText(res)) == 0 {
			t.Fatalf("%s returned empty output", c.name)
		}
	}

	// 5. Store drift findings are non-empty (governance status + similar_to +
	//    stale git were seeded so the audit has something to find).
	findings, err := st.GetDriftFindings(store.DriftAuditOpts{})
	if err != nil {
		t.Fatalf("GetDriftFindings: %v", err)
	}
	t.Logf("GetDriftFindings returned %d findings", len(findings))
}

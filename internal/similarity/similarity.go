package similarity

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/Detective-XH/docgraph/internal/store"
)

var stopWords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true,
	"was": true, "were": true, "be": true, "been": true, "being": true,
	"have": true, "has": true, "had": true, "do": true, "does": true,
	"did": true, "will": true, "would": true, "shall": true, "should": true,
	"may": true, "might": true, "can": true, "could": true, "of": true,
	"in": true, "to": true, "for": true, "with": true, "on": true,
	"at": true, "by": true, "from": true, "as": true, "or": true,
	"and": true, "but": true, "not": true, "this": true, "that": true,
	"it": true, "its": true,
}

type docFeatures struct {
	tfidf   map[string]float64
	targets map[string]bool
	tags    map[string]bool
}

// maxTargetsPerDoc bounds the per-document reference set used for the Jaccard
// "shared targets" signal. The pairwise loop pays O(|targets|) per pair across
// O(n^2) pairs and holds one such set per doc for the entire pass (the
// incremental path rebuilds every doc's set on every reindex), so an untrusted
// document with hundreds of thousands of distinct wikilinks/references — a
// single <=1MB .md can carry ~100k — would amplify both CPU and memory far out
// of proportion to its size. Legitimate documents have at most tens-to-hundreds
// of distinct outgoing references, so this cap never bites a real corpus. Same
// structural-bound rationale as docformat.ReadFileCapped / getIntArgClamped.
const maxTargetsPerDoc = 10000

// buildCappedTargets loads a document's outgoing reference targets (the kinds
// GetEdgesBySource returns: references/wikilinks_to/related_to/embeds) into a
// set, capped at maxTargetsPerDoc. On truncation it logs once to stderr so the
// degradation is observable rather than silent.
func buildCappedTargets(st *store.Store, docID string) (map[string]bool, error) {
	edges, err := st.GetEdgesBySource(docID)
	if err != nil {
		return nil, fmt.Errorf("get edges for %s: %w", docID, err)
	}
	targets := make(map[string]bool)
	for _, e := range edges {
		if len(targets) >= maxTargetsPerDoc {
			fmt.Fprintf(os.Stderr, "similarity: %s has %d reference edges; capping the similarity target set at %d (excess ignored for scoring)\n",
				docID, len(edges), maxTargetsPerDoc)
			break
		}
		targets[e.Target] = true
	}
	return targets, nil
}

// similarityEdgeBatch bounds how many similar_to edges are buffered in memory
// before being flushed to the store. The full-rebuild pairwise loop is O(n^2)
// in the worst case (a dense corpus where most pairs clear the threshold), so
// accumulating every matched edge into one slice before a single bulk insert is
// a memory hazard: ~4k dense docs reached ~4M edges / ~970 MB resident, risking
// OOM at larger scale. Flushing in fixed-size batches caps resident edge memory
// at O(similarityEdgeBatch) and bounds WAL growth (each flush commits). The
// up-front edge deletion (DeleteEdgesByKind / DeleteSimilarityEdgesForDocs)
// means a crash mid-flush self-heals on the next rebuild — the same consistency
// model as before, since delete and insert were already separate transactions.
const similarityEdgeBatch = 10000

// edgeBatcher accumulates similar_to edges and flushes them to the store in
// fixed-size batches, bounding peak memory on the O(n^2) full-rebuild path. The
// backing slice is reused across flushes — safe because InsertEdges consumes it
// synchronously (the rows are written before it returns).
type edgeBatcher struct {
	st    *store.Store
	buf   []store.Edge
	total int
}

func newEdgeBatcher(st *store.Store) *edgeBatcher {
	return &edgeBatcher{st: st, buf: make([]store.Edge, 0, similarityEdgeBatch)}
}

// add buffers one edge, flushing when the buffer reaches similarityEdgeBatch.
func (b *edgeBatcher) add(e store.Edge) error {
	b.buf = append(b.buf, e)
	if len(b.buf) >= similarityEdgeBatch {
		return b.flush()
	}
	return nil
}

// flush writes any buffered edges and resets the buffer for reuse.
func (b *edgeBatcher) flush() error {
	if len(b.buf) == 0 {
		return nil
	}
	if err := b.st.InsertEdges(b.buf); err != nil {
		return err
	}
	b.total += len(b.buf)
	b.buf = b.buf[:0]
	return nil
}

// similarityParallelMinDocs is the corpus size at or above which the pairwise
// scoring loop fans out across NumCPU workers. Below it the loop runs serially:
// the O(n^2) work is small (sub-100ms for a few hundred docs) and goroutine +
// lock setup would only add overhead. Most DocGraph projects sit below this, so
// they keep the exact serial path.
const similarityParallelMinDocs = 512

// scorePair computes the composite similarity of docs[i] and docs[j] and, when
// it meets the threshold, returns the canonical similar_to edge (lower ID as
// source). The bool is false when the pair scores below threshold. It is pure
// and reads only docs/features, so it is safe to call from many goroutines.
func scorePair(docs []store.Node, features []docFeatures, i, j int, threshold float64) (store.Edge, bool) {
	ts := cosineSimilarity(features[i].tfidf, features[j].tfidf)
	rs := jaccard(features[i].targets, features[j].targets)
	gs := jaccard(features[i].tags, features[j].tags)
	score := 0.5*ts + 0.3*rs + 0.2*gs
	if score < threshold {
		return store.Edge{}, false
	}
	idA, idB := docs[i].ID, docs[j].ID
	if idA > idB {
		idA, idB = idB, idA
	}
	meta, _ := json.Marshal(map[string]float64{
		"score": roundTo(score, 2),
		"tfidf": roundTo(ts, 2),
		"refs":  roundTo(rs, 2),
		"tags":  roundTo(gs, 2),
	})
	return store.Edge{Source: idA, Target: idB, Kind: "similar_to", Metadata: string(meta)}, true
}

// postingIndex maps each feature (text term / reference target / tag) to the
// ascending doc indices carrying it. It lets the pairwise pass skip the pairs
// that share no feature — which score exactly 0 and can never be an edge — so a
// sparse corpus collapses from O(n^2) toward O(n · avg co-occurrence).
type postingIndex struct {
	termDocs   map[string][]int
	targetDocs map[string][]int
	tagDocs    map[string][]int
}

// buildPostingIndex builds postings over all three similarity signals. Terms
// with tfidf == 0 (idf 0, i.e. present in every doc) are skipped: they add
// nothing to cosine, so omitting them is lossless and avoids a length-n posting.
func buildPostingIndex(features []docFeatures) *postingIndex {
	p := &postingIndex{
		termDocs:   make(map[string][]int),
		targetDocs: make(map[string][]int),
		tagDocs:    make(map[string][]int),
	}
	for i, f := range features {
		for term, w := range f.tfidf {
			if w != 0 {
				p.termDocs[term] = append(p.termDocs[term], i)
			}
		}
		for tgt := range f.targets {
			p.targetDocs[tgt] = append(p.targetDocs[tgt], i)
		}
		for tag := range f.tags {
			p.tagDocs[tag] = append(p.tagDocs[tag], i)
		}
	}
	return p
}

// worthPruning reports whether candidate gathering is cheaper than the full
// triangular scan. The gather visits Σ |posting|² (postings, summed over ALL
// three signals — a near-universal term OR a huge shared target/tag set, the
// C.targets shape, each inflate one posting toward length n and regenerate ~all
// pairs many times). When that reaches the full-scan pair count we fall back to
// the plain scan instead of paying the postings overhead for no gain. Sums
// short-circuit at the limit to stay O(1)-bounded and overflow-free.
func (p *postingIndex) worthPruning(n int) bool {
	limit := n * (n - 1) / 2
	cost := 0
	for _, m := range []map[string][]int{p.termDocs, p.targetDocs, p.tagDocs} {
		for _, docs := range m {
			cost += len(docs) * len(docs)
			if cost >= limit {
				return false
			}
		}
	}
	return true
}

// candidates appends to dst every doc index j > i that shares at least one
// feature with doc i, deduped via the caller-owned seen/epoch scratch
// (seen[j] == epoch ⇒ already added this call). The caller bumps epoch before
// each call and reuses seen + dst across rows to avoid per-row allocation. A
// pair sharing K features is therefore emitted once, not K times — the dedup is
// required for correctness, not just speed (K edges would otherwise be written).
func (p *postingIndex) candidates(features []docFeatures, i int, seen []int, epoch int, dst []int) []int {
	f := features[i]
	add := func(posting []int) {
		for _, j := range posting {
			if j > i && seen[j] != epoch {
				seen[j] = epoch
				dst = append(dst, j)
			}
		}
	}
	for term, w := range f.tfidf {
		if w != 0 {
			add(p.termDocs[term])
		}
	}
	for tgt := range f.targets {
		add(p.targetDocs[tgt])
	}
	for tag := range f.tags {
		add(p.tagDocs[tag])
	}
	return dst
}

// scoreAndCollectEdges scores every document pair (or, when changed != nil, only
// pairs touching a changed doc) and writes the matched similar_to edges through
// one edgeBatcher, returning the edge count. Large corpora fan the CPU-bound
// scoring across NumCPU workers and, when sparse, prune the pair space via an
// inverted index (see postingIndex). The threshold is clamped to > 0 here, which
// is what makes pruning lossless: a pair sharing no term/target/tag scores
// exactly 0, strictly below the threshold, so it is never an edge — enforcing
// the invariant at the point the prune decision is made rather than trusting the
// caller.
func scoreAndCollectEdges(st *store.Store, docs []store.Node, features []docFeatures, threshold float64, changed map[string]bool) (int, error) {
	if threshold <= 0 {
		threshold = 0.25
	}
	n := len(docs)
	workers := runtime.NumCPU()
	if n < similarityParallelMinDocs {
		workers = 1
	}
	var postings *postingIndex
	if n >= similarityParallelMinDocs {
		// Pruning helps only when the corpus is sparse enough; a real corpus's
		// efficacy is distribution-dependent (a high-DF term or a heavily-
		// referenced doc can trip the density guard), so log which path ran.
		if pi := buildPostingIndex(features); pi.worthPruning(n) {
			postings = pi
			fmt.Fprintf(os.Stderr, "similarity: inverted-index pruning active (%d docs)\n", n)
		} else {
			fmt.Fprintf(os.Stderr, "similarity: pruning skipped — dense/high-DF corpus, full scan (%d docs)\n", n)
		}
	}
	return scorePairsToBatcher(st, docs, features, threshold, changed, workers, postings)
}

// rowProcessor scores all qualifying pairs (i, j>i) for one row i, invoking
// addEdge for each match. Returning a non-nil error aborts the worker.
type rowProcessor func(i int, addEdge func(store.Edge) error) error

// runPairwiseWorkers drives makeProcess() over rows [0,n) — serially when
// workers <= 1, else across `workers` goroutines taking rows round-robin (row i
// → worker i%workers, balancing the triangular load) — and funnels every
// emitted edge through one edgeBatcher. Writes stay serialized (SQLite is a
// single writer), preserving P2(c)'s O(n^2) memory bound; a sparse corpus rarely
// matches so lock contention is negligible, a dense corpus is writer-bound
// either way. makeProcess builds a fresh rowProcessor per goroutine so each owns
// its scratch state (e.g. the pruned gather's dedup buffer) without sharing.
func runPairwiseWorkers(st *store.Store, n, workers int, makeProcess func() rowProcessor) (int, error) {
	b := newEdgeBatcher(st)

	if workers <= 1 {
		process := makeProcess()
		add := func(e store.Edge) error { return b.add(e) }
		for i := range n {
			if err := process(i, add); err != nil {
				return 0, err
			}
		}
		if err := b.flush(); err != nil {
			return 0, err
		}
		return b.total, nil
	}

	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
	)
	add := func(e store.Edge) error {
		mu.Lock()
		if firstErr != nil {
			mu.Unlock()
			return firstErr
		}
		err := b.add(e)
		if err != nil {
			firstErr = err
		}
		mu.Unlock()
		return err
	}
	for w := range workers {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			process := makeProcess()
			for i := start; i < n; i += workers {
				if err := process(i, add); err != nil {
					return // first error is recorded in firstErr via add
				}
			}
		}(w)
	}
	wg.Wait()
	if firstErr != nil {
		return 0, firstErr
	}
	if err := b.flush(); err != nil {
		return 0, err
	}
	return b.total, nil
}

// scorePairsToBatcher runs the pairwise pass and writes matched similar_to
// edges. With postings == nil it scans the full triangular pair space; with a
// postingIndex it visits only candidate pairs sharing a feature (lossless for
// threshold > 0 — non-candidates score exactly 0). workers <= 1 forces the
// serial path. Split from scoreAndCollectEdges so tests can force the concurrent
// and/or pruned path regardless of host core count or corpus density.
func scorePairsToBatcher(st *store.Store, docs []store.Node, features []docFeatures, threshold float64, changed map[string]bool, workers int, postings *postingIndex) (int, error) {
	n := len(docs)
	if workers > n {
		workers = n
	}

	var makeProcess func() rowProcessor
	if postings == nil {
		// Full triangular scan.
		makeProcess = func() rowProcessor {
			return func(i int, addEdge func(store.Edge) error) error {
				for j := i + 1; j < n; j++ {
					if changed != nil && !changed[docs[i].ID] && !changed[docs[j].ID] {
						continue
					}
					if e, ok := scorePair(docs, features, i, j, threshold); ok {
						if err := addEdge(e); err != nil {
							return err
						}
					}
				}
				return nil
			}
		}
	} else {
		// Pruned scan: only candidate neighbors sharing a feature with i. Each
		// goroutine owns its seen/nb scratch, reused across rows (no per-row alloc).
		makeProcess = func() rowProcessor {
			seen := make([]int, n)
			var epoch int
			var nb []int
			return func(i int, addEdge func(store.Edge) error) error {
				epoch++
				nb = postings.candidates(features, i, seen, epoch, nb[:0])
				for _, j := range nb {
					if changed != nil && !changed[docs[i].ID] && !changed[docs[j].ID] {
						continue
					}
					if e, ok := scorePair(docs, features, i, j, threshold); ok {
						if err := addEdge(e); err != nil {
							return err
						}
					}
				}
				return nil
			}
		}
	}
	return runPairwiseWorkers(st, n, workers, makeProcess)
}

// ComputeSimilarity computes pairwise topic similarity between all documents
// using a hybrid of TF-IDF text similarity, shared reference targets, and tag
// overlap. Edges of kind "similar_to" are inserted for pairs whose composite
// score meets the threshold.
func ComputeSimilarity(st *store.Store, threshold float64) error {
	if threshold <= 0 {
		threshold = 0.25
	}

	docs, err := st.GetAllDocumentNodes()
	if err != nil {
		return fmt.Errorf("get documents: %w", err)
	}
	if len(docs) < 2 {
		fmt.Fprintln(os.Stderr, "Computed similarity: 0 similar_to edges (fewer than 2 documents)")
		return nil
	}

	// Clean up old similarity edges before recomputing
	if err := st.DeleteEdgesByKind("similar_to"); err != nil {
		return fmt.Errorf("clean similarity edges: %w", err)
	}

	// Step 1: build per-document raw token lists, target sets, and tag sets.
	type rawDoc struct {
		id      string
		tokens  []string
		targets map[string]bool
		tags    map[string]bool
	}
	raw := make([]rawDoc, len(docs))
	df := make(map[string]int)
	for i, d := range docs {
		tokens := tokenize(d.Name + " " + d.BodyExcerpt)

		targets, err := buildCappedTargets(st, d.ID)
		if err != nil {
			return err
		}

		raw[i] = rawDoc{
			id:      d.ID,
			tokens:  tokens,
			targets: targets,
			tags:    extractTagSet(d.Metadata),
		}

		seen := make(map[string]bool)
		for _, t := range tokens {
			if !seen[t] {
				df[t]++
				seen[t] = true
			}
		}
	}

	// Step 2: compute TF-IDF vectors.
	n := float64(len(docs))
	features := make([]docFeatures, len(raw))
	for i, rd := range raw {
		tf := make(map[string]float64)
		total := float64(len(rd.tokens))
		if total == 0 {
			features[i] = docFeatures{tfidf: tf, targets: rd.targets, tags: rd.tags}
			continue
		}
		for _, t := range rd.tokens {
			tf[t]++
		}
		tfidf := make(map[string]float64, len(tf))
		for term, count := range tf {
			tfidf[term] = (count / total) * math.Log(n/float64(df[term]))
		}
		features[i] = docFeatures{tfidf: tfidf, targets: rd.targets, tags: rd.tags}
	}

	// Steps 3–6: pairwise comparison → edge creation. Scoring fans out across
	// workers for large corpora; matched edges flush in fixed-size batches (P2(c)).
	total, err := scoreAndCollectEdges(st, docs, features, threshold, nil)
	if err != nil {
		return fmt.Errorf("insert similarity edges: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Computed similarity: %d similar_to edges (threshold=%.2f)\n", total, threshold)
	return nil
}

// ComputeSimilarityIncremental recomputes similar_to edges for docs in
// changedDocIDs only, leaving other pairs untouched. Falls back to a full
// rebuild when: changedDocIDs is empty, k > n/2, or the threshold changed.
//
// IDF drift note: adding a new doc shifts IDF for its terms, which can
// slightly alter scores for unchanged pairs. These pairs are corrected on
// their next change or a forced full rebuild (--force on index).
func ComputeSimilarityIncremental(st *store.Store, changedDocIDs []string, threshold float64) error {
	if len(changedDocIDs) == 0 {
		return nil
	}
	if threshold <= 0 {
		threshold = 0.25
	}

	// Full rebuild when threshold changed since last run.
	threshKey := "similarity_threshold"
	stored, ok, _ := st.GetProjectMeta(threshKey)
	if ok && stored != strconv.FormatFloat(threshold, 'f', 4, 64) {
		return ComputeSimilarity(st, threshold)
	}

	docs, err := st.GetAllDocumentNodes()
	if err != nil {
		return fmt.Errorf("get documents: %w", err)
	}
	if len(docs) < 2 {
		return nil
	}

	// Bail out when more than half the corpus changed — full rebuild wins.
	if len(changedDocIDs) > len(docs)/2 {
		return ComputeSimilarity(st, threshold)
	}

	changed := make(map[string]bool, len(changedDocIDs))
	for _, id := range changedDocIDs {
		changed[id] = true
	}

	// Build per-doc raw features and global IDF from all docs (O(n)).
	type rawDoc struct {
		id      string
		tokens  []string
		targets map[string]bool
		tags    map[string]bool
	}
	raw := make([]rawDoc, len(docs))
	df := make(map[string]int)
	for i, d := range docs {
		tokens := tokenize(d.Name + " " + d.BodyExcerpt)
		targets, err := buildCappedTargets(st, d.ID)
		if err != nil {
			return err
		}
		raw[i] = rawDoc{id: d.ID, tokens: tokens, targets: targets, tags: extractTagSet(d.Metadata)}
		seen := make(map[string]bool)
		for _, t := range tokens {
			if !seen[t] {
				df[t]++
				seen[t] = true
			}
		}
	}

	n := float64(len(docs))
	features := make([]docFeatures, len(raw))
	for i, rd := range raw {
		tf := make(map[string]float64)
		total := float64(len(rd.tokens))
		if total == 0 {
			features[i] = docFeatures{tfidf: tf, targets: rd.targets, tags: rd.tags}
			continue
		}
		for _, t := range rd.tokens {
			tf[t]++
		}
		tfidf := make(map[string]float64, len(tf))
		for term, count := range tf {
			tfidf[term] = (count / total) * math.Log(n/float64(df[term]))
		}
		features[i] = docFeatures{tfidf: tfidf, targets: rd.targets, tags: rd.tags}
	}

	// Remove stale edges involving changed docs.
	if err := st.DeleteSimilarityEdgesForDocs(changedDocIDs); err != nil {
		return fmt.Errorf("clean similarity edges: %w", err)
	}

	// Recompute only pairs where at least one doc is changed (O(k×n)). Scored in
	// parallel and batched like the full rebuild — k can approach n/2 before the
	// bail-out above, so a dense corpus reaches the same O(n^2) edge volume.
	total, err := scoreAndCollectEdges(st, docs, features, threshold, changed)
	if err != nil {
		return fmt.Errorf("insert similarity edges: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Incremental similarity: %d similar_to edges recomputed (%d changed docs, threshold=%.2f)\n",
		total, len(changedDocIDs), threshold)

	_ = st.UpsertProjectMeta(threshKey, strconv.FormatFloat(threshold, 'f', 4, 64))
	return nil
}

// tokenize lowercases text, splits on non-letter/digit boundaries, removes
// stop words and short tokens, and produces CJK bigrams where appropriate.
func tokenize(text string) []string {
	text = strings.ToLower(text)
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	var tokens []string
	for _, p := range parts {
		if hasCJK(p) {
			runes := []rune(p)
			for k := 0; k+1 < len(runes); k++ {
				tokens = append(tokens, string(runes[k:k+2]))
			}
		} else {
			if len(p) < 2 || stopWords[p] {
				continue
			}
			tokens = append(tokens, p)
		}
	}
	return tokens
}

func hasCJK(s string) bool {
	for _, r := range s {
		if isCJK(r) {
			return true
		}
	}
	return false
}

func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hangul, unicode.Katakana, unicode.Hiragana)
}

// cosineSimilarity computes cosine similarity between two sparse vectors.
// It iterates over the shorter vector for efficiency.
func cosineSimilarity(a, b map[string]float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	var dot, normA, normB float64
	for term, va := range a {
		normA += va * va
		if vb, ok := b[term]; ok {
			dot += va * vb
		}
	}
	for _, vb := range b {
		normB += vb * vb
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// jaccard computes the Jaccard similarity of two sets.
func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func extractTagSet(metadataJSON string) map[string]bool {
	set := make(map[string]bool)
	if metadataJSON == "" {
		return set
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &m); err != nil {
		return set
	}
	arr, _ := m["tags"].([]any)
	for _, v := range arr {
		if s, ok := v.(string); ok {
			set[strings.ToLower(s)] = true
		}
	}
	return set
}

func roundTo(v float64, decimals int) float64 {
	p := math.Pow(10, float64(decimals))
	return math.Round(v*p) / p
}

// denseCosineSimilarity computes cosine similarity between two dense float64 vectors.
func denseCosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// ComputeNeuralSimilarityForDoc recomputes neural similar_to edges for a single
// document against all other documents that share the same model_id embedding.
// Existing neural edges for docID are replaced. Uses the stored similarity
// threshold from project_metadata (default 0.25).
func ComputeNeuralSimilarityForDoc(st *store.Store, docID, modelID string, threshold float64) error {
	if threshold <= 0 {
		stored, ok, _ := st.GetProjectMeta("similarity_threshold")
		if ok {
			if v, err := strconv.ParseFloat(stored, 64); err == nil {
				threshold = v
			}
		}
		if threshold <= 0 {
			threshold = 0.25
		}
	}

	embs, err := st.GetEmbeddingsByModel(modelID)
	if err != nil {
		return fmt.Errorf("get embeddings: %w", err)
	}
	if len(embs) < 2 {
		return nil
	}

	// Find this doc's vector.
	var docVec []float64
	for _, e := range embs {
		if e.DocID == docID {
			docVec = e.Vector
			break
		}
	}
	if docVec == nil {
		return nil
	}

	// Clear existing neural edges for this doc before recomputing.
	if err := st.DeleteNeuralSimilarityEdgesForDoc(docID); err != nil {
		return fmt.Errorf("delete neural edges: %w", err)
	}

	edges := newEdgeBatcher(st)
	for _, e := range embs {
		if e.DocID == docID {
			continue
		}
		score := denseCosineSimilarity(docVec, e.Vector)
		if score < threshold {
			continue
		}
		// Store canonical order (lower ID first) to avoid duplicate edges.
		src, tgt := docID, e.DocID
		if src > tgt {
			src, tgt = tgt, src
		}
		meta, _ := json.Marshal(map[string]any{
			"engine":   "neural",
			"model_id": modelID,
			"score":    roundTo(score, 4),
		})
		if err := edges.add(store.Edge{
			Source:   src,
			Target:   tgt,
			Kind:     "similar_to",
			Metadata: string(meta),
		}); err != nil {
			return fmt.Errorf("insert neural edges: %w", err)
		}
	}

	if err := edges.flush(); err != nil {
		return fmt.Errorf("insert neural edges: %w", err)
	}
	return nil
}

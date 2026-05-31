package similarity

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sync"

	"github.com/Detective-XH/docgraph/internal/store"
)

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

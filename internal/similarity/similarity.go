// Package similarity computes topic similarity between documents. The pairwise
// scoring engine (edge batching, posting-index pruning, worker fan-out) lives in
// pairwise.go and posting.go; text tokenization in tokenize.go; the vector/set
// math primitives in vectormath.go. This file holds the document feature model
// and the public Compute* entry points.
package similarity

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/Detective-XH/docgraph/internal/store"
)

type docFeatures struct {
	tfidf   map[string]float64
	targets map[string]bool
	tags    map[string]bool
}

// rawDoc is used internally by buildDocFeatures to carry per-document token
// and set data before TF-IDF weights are applied.
type rawDoc struct {
	id      string
	tokens  []string
	targets map[string]bool
	tags    map[string]bool
}

// buildDocFeatures builds the TF-IDF feature vector for every document in
// docs. It is shared between ComputeSimilarity and ComputeSimilarityIncremental
// to avoid duplicating the raw→df→tfidf loop.
func buildDocFeatures(st SimilarityStore, docs []store.Node) ([]docFeatures, error) {
	raw := make([]rawDoc, len(docs))
	df := make(map[string]int)
	for i, d := range docs {
		tokens := tokenize(d.Name + " " + d.BodyExcerpt)
		targets, err := buildCappedTargets(st, d.ID)
		if err != nil {
			return nil, err
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
	return features, nil
}

// resolveNeuralThreshold resolves the effective threshold for neural similarity.
// If threshold > 0 it is returned unchanged. Otherwise the stored project-meta
// value is used, falling back to 0.25. This mirrors the pre-refactor logic
// exactly: any successfully-parsed stored value is assigned, and only a final
// threshold <= 0 triggers the 0.25 fallback (a stored NaN parses successfully
// and is <= 0-false, so it propagates as before — do not "fix" that here; a
// behavior change belongs in its own commit with a test).
func resolveNeuralThreshold(st SimilarityStore, threshold float64) float64 {
	if threshold > 0 {
		return threshold
	}
	if stored, ok, _ := st.GetProjectMeta("similarity_threshold"); ok {
		if v, err := strconv.ParseFloat(stored, 64); err == nil {
			threshold = v
		}
	}
	if threshold <= 0 {
		return 0.25
	}
	return threshold
}

// findDocVector returns the embedding vector for docID from embs, or nil if
// not present.
func findDocVector(embs []store.Embedding, docID string) []float64 {
	for _, e := range embs {
		if e.DocID == docID {
			return e.Vector
		}
	}
	return nil
}

// ComputeSimilarity computes pairwise topic similarity between all documents
// using a hybrid of TF-IDF text similarity, shared reference targets, and tag
// overlap. Edges of kind "similar_to" are inserted for pairs whose composite
// score meets the threshold.
func ComputeSimilarity(st SimilarityStore, threshold float64) error {
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

	features, err := buildDocFeatures(st, docs)
	if err != nil {
		return err
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
func ComputeSimilarityIncremental(st SimilarityStore, changedDocIDs []string, threshold float64) error {
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

	// Build per-doc features and global IDF from all docs (O(n)).
	features, err := buildDocFeatures(st, docs)
	if err != nil {
		return err
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

// ComputeNeuralSimilarityForDoc recomputes neural similar_to edges for a single
// document against all other documents that share the same model_id embedding.
// Existing neural edges for docID are replaced. Uses the stored similarity
// threshold from project_metadata (default 0.25).
func ComputeNeuralSimilarityForDoc(st SimilarityStore, docID, modelID string, threshold float64) error {
	threshold = resolveNeuralThreshold(st, threshold)

	embs, err := st.GetEmbeddingsByModel(modelID)
	if err != nil {
		return fmt.Errorf("get embeddings: %w", err)
	}
	if len(embs) < 2 {
		return nil
	}

	docVec := findDocVector(embs, docID)
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

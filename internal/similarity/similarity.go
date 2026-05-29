package similarity

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
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

	// Steps 3–6: pairwise comparison → edge creation.
	var edges []store.Edge
	for i := 0; i < len(docs); i++ {
		for j := i + 1; j < len(docs); j++ {
			idA, idB := raw[i].id, raw[j].id
			if idA > idB {
				idA, idB = idB, idA
			}

			ts := cosineSimilarity(features[i].tfidf, features[j].tfidf)
			rs := jaccard(features[i].targets, features[j].targets)
			gs := jaccard(features[i].tags, features[j].tags)
			score := 0.5*ts + 0.3*rs + 0.2*gs

			if score >= threshold {
				meta, _ := json.Marshal(map[string]float64{
					"score": roundTo(score, 2),
					"tfidf": roundTo(ts, 2),
					"refs":  roundTo(rs, 2),
					"tags":  roundTo(gs, 2),
				})
				edges = append(edges, store.Edge{
					Source:   idA,
					Target:   idB,
					Kind:     "similar_to",
					Metadata: string(meta),
				})
			}
		}
	}

	if len(edges) > 0 {
		if err := st.InsertEdges(edges); err != nil {
			return fmt.Errorf("insert similarity edges: %w", err)
		}
	}
	fmt.Fprintf(os.Stderr, "Computed similarity: %d similar_to edges (threshold=%.2f)\n", len(edges), threshold)
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

	// Recompute only pairs where at least one doc is changed (O(k×n)).
	var edges []store.Edge
	for i := 0; i < len(docs); i++ {
		for j := i + 1; j < len(docs); j++ {
			if !changed[raw[i].id] && !changed[raw[j].id] {
				continue
			}
			idA, idB := raw[i].id, raw[j].id
			if idA > idB {
				idA, idB = idB, idA
			}
			ts := cosineSimilarity(features[i].tfidf, features[j].tfidf)
			rs := jaccard(features[i].targets, features[j].targets)
			gs := jaccard(features[i].tags, features[j].tags)
			score := 0.5*ts + 0.3*rs + 0.2*gs
			if score >= threshold {
				meta, _ := json.Marshal(map[string]float64{
					"score": roundTo(score, 2),
					"tfidf": roundTo(ts, 2),
					"refs":  roundTo(rs, 2),
					"tags":  roundTo(gs, 2),
				})
				edges = append(edges, store.Edge{
					Source:   idA,
					Target:   idB,
					Kind:     "similar_to",
					Metadata: string(meta),
				})
			}
		}
	}

	if len(edges) > 0 {
		if err := st.InsertEdges(edges); err != nil {
			return fmt.Errorf("insert similarity edges: %w", err)
		}
	}
	fmt.Fprintf(os.Stderr, "Incremental similarity: %d similar_to edges recomputed (%d changed docs, threshold=%.2f)\n",
		len(edges), len(changedDocIDs), threshold)

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

	var edges []store.Edge
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
		edges = append(edges, store.Edge{
			Source:   src,
			Target:   tgt,
			Kind:     "similar_to",
			Metadata: string(meta),
		})
	}

	if len(edges) > 0 {
		if err := st.InsertEdges(edges); err != nil {
			return fmt.Errorf("insert neural edges: %w", err)
		}
	}
	return nil
}

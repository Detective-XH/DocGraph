package similarity

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

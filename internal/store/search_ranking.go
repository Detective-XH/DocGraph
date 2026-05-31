package store

import (
	"math"
	"strings"
)

func (s *Store) applyFieldRanking(req searchRequest, c *searchCandidate) {
	c.Score += weightedTextScore(req.Query, req.Terms, c.Node.Name, 12)
	c.Score += weightedTextScore(req.Query, req.Terms, c.HeadingPath, 10)
	c.Score += weightedTextScore(req.Query, req.Terms, c.Node.QualifiedName, 6)
	c.Score += weightedTextScore(req.Query, req.Terms, c.Node.Metadata, 5)
	c.Score += weightedTextScore(req.Query, req.Terms, c.Node.BodyExcerpt, 3)
	c.Score += weightedTextScore(req.Query, req.Terms, c.SectionText, 2)

	if len(req.ExpandedTerms) > 0 {
		c.Score += weightedTermScore(req.ExpandedTerms, c.Node.Name, 2)
		c.Score += weightedTermScore(req.ExpandedTerms, c.HeadingPath, 2)
		c.Score += weightedTermScore(req.ExpandedTerms, c.SectionText, 0.75)
	}

	if c.Node.Kind == "heading" && c.Sources["section_fts"] {
		c.Score += 8
	}
	if c.Node.Kind == "definition" {
		c.Score += 5
	}
	switch req.Intent {
	case SearchIntentExact:
		if strings.EqualFold(strings.Trim(req.Query, `"`), c.Node.Name) ||
			strings.EqualFold(strings.Trim(req.Query, `"`), c.Node.FilePath) ||
			strings.EqualFold(strings.Trim(req.Query, `"`), c.Node.QualifiedName) {
			c.Score += 40
		}
	case SearchIntentSection:
		if c.Node.Kind == "heading" {
			c.Score += 12
		}
	}
}

// applyGraphScore adds the graph-centrality rerank to a candidate from
// precomputed signal counts. The count fetch is split out (graphSignalsBatch in
// the SearchWithOptions hot path; graphSignals per-candidate as the equivalence
// reference) so both share this single scoring formula. Caps mirror the
// calibration discipline elsewhere: incoming ≤12 (= name-field weight),
// outgoing ≤5, tag matches at weight 8.
func applyGraphScore(c *searchCandidate, incoming, outgoing, tagMatches int) {
	c.Score += math.Min(math.Log1p(float64(incoming))*3, 12)
	c.Score += math.Min(math.Log1p(float64(outgoing))*1.25, 5)
	c.Score += float64(tagMatches) * 8
}

func (s *Store) applyMetadataReranking(req searchRequest, c *searchCandidate) {
	if c.Governance != nil {
		c.Score += governanceRetrievalScore(c.Governance, req.Governance.AllowedAudience, req.AsOf)
	}
	if c.Research != nil {
		c.Score += researchRetrievalScore(c.Research, req.AsOf)
	}
}

// applyHistoryReranking nudges a candidate up by its git churn — commit count and
// distinct-author count — as a proxy for importance/centrality: a doc edited many
// times by many people is likely load-bearing. This is deliberately churn (not
// recency): "recently committed" would bury authoritative-but-stable docs, so
// freshness stays in the doc.stale_by_git drift finder, not in ranking.
//
// Inert when absent: file_history rows exist only for git-tracked corpora with
// collection on, so c.History is nil (and CommitCount<=0 guards a degenerate row)
// for non-git / --no-history / never-committed files — those contribute exactly
// zero, never a penalty. The terms are log-compressed and capped (commit ≤7,
// author ≤3, combined ≤10) so the signal nudges but never dominates the text
// score — same calibration discipline as applyGraphReranking and ftsRankBoost,
// and below the name-field weight (12) and fts boost cap (12).
//
// Defense-in-depth against a malformed/corrupt row: a real git row always has
// commit_count≥1 and author_count≥1, but the schema does not CHECK >=0, so a
// hand-corrupted row could carry a negative count. math.Log1p of any value < -1
// returns NaN, which would poison c.Score and leave the search sort comparator
// (Rank < Rank) undefined for NaN — corrupting result order. The CommitCount<=0
// guard drops such a row, and math.Max(0,...) clamps the author term so a
// negative author_count can never reach Log1p. Cheap, removes the NaN class
// entirely; not a reachable exploit on real git data.
func (s *Store) applyHistoryReranking(req searchRequest, c *searchCandidate) {
	h := c.History
	if h == nil || h.CommitCount <= 0 {
		return
	}
	c.Score += math.Min(math.Log1p(float64(h.CommitCount))*2.5, 7)
	c.Score += math.Min(math.Log1p(math.Max(0, float64(h.AuthorCount)))*1.5, 3)
}

// graphSignals computes one node's reference/tag counts with a query per signal
// (and per term for tags). SearchWithOptions no longer calls it on the hot path —
// graphSignalsBatch does the whole candidate set in a few set-based queries — but
// it is retained as the readable per-candidate definition and as the oracle that
// TestSearchBatchEquivalence asserts graphSignalsBatch against.
func (s *Store) graphSignals(req searchRequest, n Node) (incoming, outgoing, tagMatches int, err error) {
	refKinds := "('references','wikilinks_to','related_to','embeds')"
	if n.Kind == "document" {
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM edges e JOIN nodes t ON t.id = e.target WHERE t.file_path = ? AND e.kind IN `+refKinds, n.FilePath).Scan(&incoming); err != nil {
			return
		}
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM edges e JOIN nodes src ON src.id = e.source WHERE src.file_path = ? AND e.kind IN `+refKinds, n.FilePath).Scan(&outgoing); err != nil {
			return
		}
	} else {
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM edges WHERE target = ? AND kind IN `+refKinds, n.ID).Scan(&incoming); err != nil {
			return
		}
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM edges WHERE source = ? AND kind IN `+refKinds, n.ID).Scan(&outgoing); err != nil {
			return
		}
	}

	for _, term := range append(req.Terms, req.ExpandedTerms...) {
		sourceID := n.ID
		if n.Kind != "document" {
			sourceID = n.FilePath
		}
		var count int
		if scanErr := s.db.QueryRow(`
			SELECT COUNT(*)
			FROM edges e
			JOIN nodes t ON t.id = e.target
			WHERE e.source = ?
			  AND e.kind = 'tagged'
			  AND t.kind = 'tag'
			  AND lower(t.name) = lower(?)`, sourceID, term).Scan(&count); scanErr != nil {
			err = scanErr
			return
		}
		tagMatches += count
	}
	return
}

func weightedTextScore(query string, terms []string, text string, weight float64) float64 {
	text = strings.ToLower(text)
	if text == "" {
		return 0
	}
	score := 0.0
	q := strings.ToLower(strings.TrimSpace(query))
	if q != "" && strings.Contains(text, q) {
		score += weight * 3
	}
	score += weightedTermScore(terms, text, weight)
	return score
}

func weightedTermScore(terms []string, text string, weight float64) float64 {
	text = strings.ToLower(text)
	if text == "" {
		return 0
	}
	score := 0.0
	for _, term := range terms {
		if term != "" && strings.Contains(text, term) {
			score += weight
		}
	}
	return score
}

// FTS relevance boost calibration. SQLite FTS5 bm25() returns a more-negative
// score for a more-relevant row, so -rank is a positive relevance magnitude
// (real hits land roughly in 0.5..15 with the column weights in use).
const (
	// ftsBoostScale maps bm25 magnitude to score 1:1. The previous 1e6 scale
	// pushed every real hit past the cap, saturating to a flat boost and
	// discarding the bm25 signal entirely.
	ftsBoostScale = 1.0
	// ftsBoostCap bounds the boost at the strongest field weight (name=12), so
	// a top FTS hit is comparable to a name-field match but never dominates the
	// Go-side field/graph/metadata ranking. It also tames corpus-dependent
	// bm25 magnitudes — runaway scores clamp here.
	ftsBoostCap = 12.0
)

func ftsRankBoost(rank float64) float64 {
	if rank < 0 {
		return math.Min(-rank*ftsBoostScale, ftsBoostCap)
	}
	// rank >= 0 only at the IDF<=0 boundary: a term so common it carries
	// near-zero relevance, so bm25 collapses to ~0. Keep the prior small,
	// decaying fallback — such a hit earns a negligible boost either way.
	return 1 / (1 + rank)
}

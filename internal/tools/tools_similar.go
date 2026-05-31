package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

var similarTool = mcp.NewTool("docgraph_similar",
	mcp.WithDescription("Find documents topically similar to a given document using TF-IDF term overlap + shared references + tag overlap (engine=auto/tfidf — the default, always on, no flags). Returns 0 results for a topically unique document (a broad README or changelog commonly has no similar_to edges even when the index is fully built and the engine is working): 0 does NOT mean the engine is off, embeddings are disabled, or the index is broken. Neural similarity is an OPTIONAL add-on layered on top — only if embeddings were stored via docgraph_embeddings action=store (engine=neural) are neural scores added; embeddings being disabled never causes a TF-IDF 0-result. For explicit link tracking use docgraph_graph. Accepts document paths only — heading anchors (doc.md#heading) return empty. The score is a 0-to-1 weighted blend (TF-IDF cosine 50% + shared-reference Jaccard 30% + tag Jaccard 20%); it is NOT a percentage. Each result shows the three signal components that drove its score. No per-vocabulary-term breakdown is available — the engine does not retain individual term contributions. Scores are corpus-relative; 0.4-0.5 can mean near-identical in a corpus with high shared vocabulary."),
	mcp.WithString("document", mcp.Required(), mcp.Description("Document name or path (document paths only; heading anchors return empty)")),
	mcp.WithNumber("limit", mcp.Description("Max results (default 10)")),
	mcp.WithString("engine", mcp.Description("Similarity engine: auto (default), tfidf, or neural. neural requires --enable-embeddings; returns an error if the server was not started with that flag.")),
)

func (h *handler) handleSimilar(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	document := getStringArg(args, "document", "")
	if document == "" {
		return mcp.NewToolResultError("document parameter is required"), nil
	}
	document = sanitizeArg(document, maxArgLength)
	limit := getIntArgClamped(args, "limit", 10, 0, maxListLimit)
	engine := strings.ToLower(strings.TrimSpace(getStringArg(args, "engine", "auto")))
	if engine == "neural" && !h.enableEmbeddings {
		return mcp.NewToolResultError("Neural similarity requires --enable-embeddings. Restart the server with that flag, or use the default TF-IDF similarity instead."), nil
	}

	node, err := h.resolveDoc(document)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve failed: %v", err)), nil
	}
	if node == nil {
		return mcp.NewToolResultError(fmt.Sprintf("document not found: %s — try docgraph_search to find the correct name or path", document)), nil
	}

	// Query similar_to edges (both directions) for this document.
	var edges []store.Edge
	if h.workspace != nil {
		for _, p := range h.workspace.Projects {
			if es, err := p.Store.GetSimilarEdgesForDoc(node.ID); err == nil {
				edges = append(edges, es...)
			}
		}
	} else {
		var err error
		edges, err = h.store.GetSimilarEdgesForDoc(node.ID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("get similar edges: %v", err)), nil
		}
	}

	deduped := rankSimilarEdges(edges, engine, limit)

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Documents similar to %q\n\nFound %d similar documents.\n", node.Name, len(deduped))

	if len(deduped) == 0 {
		sb.WriteString("\nNo similarity edges for this document. This is expected for topically unique documents (e.g. a README or changelog) — it does NOT mean the document is unconnected, and it does NOT mean the similarity engine is disabled (TF-IDF is always on; 0 edges is a real result, not a misconfiguration). To find related documents anyway, use docgraph_search for keyword-based discovery, docgraph_graph operation=incoming/outgoing for explicit citation links, or docgraph_context for topic neighbours. Note: a related-reading list built from those tools is keyword- and link-based — it is not a similarity score and results are not ranked by topical overlap.\n")
		return mcp.NewToolResultText(sb.String()), nil
	}

	for i, e := range deduped {
		otherID := e.Target
		if otherID == node.ID {
			otherID = e.Source
		}
		other := h.getNodeByID(otherID)
		if other == nil {
			continue
		}
		score := ""
		if e.Metadata != "" {
			var m map[string]any
			if json.Unmarshal([]byte(e.Metadata), &m) == nil {
				if s, ok := m["score"].(float64); ok {
					tf, okT := m["tfidf"].(float64)
					rs, okR := m["refs"].(float64)
					gs, okG := m["tags"].(float64)
					if okT && okR && okG {
						// TF-IDF edge: surface blend label + component breakdown.
						score = fmt.Sprintf(" (score: %.2f (0-1 weighted blend); signals behind this score: tfidf-cosine %.2f, shared-refs %.2f, shared-tags %.2f)", s, tf, rs, gs)
					} else {
						// Neural or other edge: render unchanged (score + engine + model).
						score = fmt.Sprintf(" (score: %.2f", s)
						if eng, ok := m["engine"].(string); ok {
							score += ", engine: " + eng
							if mid, ok := m["model_id"].(string); ok {
								score += ", model: " + mid
							}
						}
						score += ")"
					}
				}
			}
		}
		fmt.Fprintf(&sb, "\n%d. **%s** %s%s\n", i+1, other.Name, other.FilePath, score)
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// rankSimilarEdges turns raw similar_to edges into the display set:
//   - filter by engine ("tfidf"/"neural"; "" or "auto" keeps all), BEFORE dedup
//     so a tfidf edge sharing a pair with a neural edge is not lost;
//   - deduplicate by canonical doc pair, preferring a neural edge over tfidf;
//   - order by similarity score descending (canonical source/target tiebreak)
//     so the most-similar come first and limit truncates the least-similar tail
//     — the dedup map iterates randomly, so without this both order and which
//     results survive limit would be nondeterministic;
//   - truncate to limit (limit <= 0 keeps all).
//
// Pure (no store access) so the dedup/order/limit contract is unit-testable
// directly on []store.Edge.
func rankSimilarEdges(edges []store.Edge, engine string, limit int) []store.Edge {
	if engine == "tfidf" || engine == "neural" {
		filtered := edges[:0]
		for _, e := range edges {
			eng := edgeEngine(e)
			if engine == "neural" && eng == "neural" {
				filtered = append(filtered, e)
			} else if engine == "tfidf" && eng != "neural" {
				filtered = append(filtered, e)
			}
		}
		edges = filtered
	}

	type pairKey struct{ a, b string }
	best := make(map[pairKey]store.Edge)
	for _, e := range edges {
		src, tgt := e.Source, e.Target
		if src > tgt {
			src, tgt = tgt, src
		}
		k := pairKey{src, tgt}
		existing, ok := best[k]
		if !ok {
			best[k] = e
			continue
		}
		if edgeEngine(e) == "neural" && edgeEngine(existing) != "neural" {
			best[k] = e
		}
	}

	deduped := make([]store.Edge, 0, len(best))
	for _, e := range best {
		deduped = append(deduped, e)
	}
	sort.SliceStable(deduped, func(i, j int) bool {
		si, sj := similarEdgeScore(deduped[i]), similarEdgeScore(deduped[j])
		if si != sj {
			return si > sj
		}
		if deduped[i].Source != deduped[j].Source {
			return deduped[i].Source < deduped[j].Source
		}
		return deduped[i].Target < deduped[j].Target
	})
	if limit > 0 && len(deduped) > limit {
		deduped = deduped[:limit]
	}
	return deduped
}

// edgeEngine reads $.engine from a similar_to edge's metadata JSON, returning
// "" when the metadata is absent, unparseable, or has no engine field.
func edgeEngine(e store.Edge) string {
	if e.Metadata == "" {
		return ""
	}
	var m map[string]any
	if json.Unmarshal([]byte(e.Metadata), &m) != nil {
		return ""
	}
	eng, _ := m["engine"].(string)
	return eng
}

// similarEdgeScore extracts the composite similarity score from a similar_to
// edge's metadata JSON ($.score, written by both the tfidf and neural engines).
// Returns 0 when the metadata is absent, unparseable, or has no score.
func similarEdgeScore(e store.Edge) float64 {
	if e.Metadata == "" {
		return 0
	}
	var m map[string]any
	if json.Unmarshal([]byte(e.Metadata), &m) != nil {
		return 0
	}
	if s, ok := m["score"].(float64); ok {
		return s
	}
	return 0
}

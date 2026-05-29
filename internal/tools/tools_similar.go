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
	mcp.WithDescription("Find documents that are topically similar to a given document, even without explicit links. Uses TF-IDF text similarity + shared references + tag overlap. If neural embeddings have been stored via docgraph_embeddings action=store, results also include neural similarity scores (engine: neural). For explicit link tracking, use docgraph_graph instead. Returns empty if no similarity edges have been computed for the document (check docgraph_status → edge count). Accepts document paths only — heading anchors (doc.md#heading) return empty."),
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

	// Filter by engine before dedup to avoid losing tfidf edges that share a
	// pair with a neural edge (dedup would otherwise discard the tfidf edge).
	if engine == "tfidf" || engine == "neural" {
		filtered := edges[:0]
		for _, e := range edges {
			var m map[string]any
			var eng string
			if e.Metadata != "" {
				if json.Unmarshal([]byte(e.Metadata), &m) == nil {
					eng, _ = m["engine"].(string)
				}
			}
			if engine == "neural" && eng == "neural" {
				filtered = append(filtered, e)
			} else if engine == "tfidf" && eng != "neural" {
				filtered = append(filtered, e)
			}
		}
		edges = filtered
	}

	// Deduplicate: same pair can have both TF-IDF and neural edges.
	// Keep one edge per pair, preferring engine=neural over tfidf.
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
		// Prefer neural engine.
		var existingEng, newEng string
		var m map[string]any
		if json.Unmarshal([]byte(existing.Metadata), &m) == nil {
			existingEng, _ = m["engine"].(string)
		}
		if json.Unmarshal([]byte(e.Metadata), &m) == nil {
			newEng, _ = m["engine"].(string)
		}
		if newEng == "neural" && existingEng != "neural" {
			best[k] = e
		}
	}

	deduped := make([]store.Edge, 0, len(best))
	for _, e := range best {
		deduped = append(deduped, e)
	}
	// Order by similarity score (desc) so the most-similar docs come first and
	// `limit` truncates the least-similar tail. The dedup map above iterates in
	// random order, so without this both the displayed order AND which results
	// survive `limit` would be nondeterministic. Canonical source/target is the
	// stable tiebreak for equal scores.
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

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Documents similar to %q\n\nFound %d similar documents.\n", node.Name, len(deduped))

	if len(deduped) == 0 {
		sb.WriteString("\nNo similarity edges for this document. This is expected for topically unique documents (e.g. a README or changelog) — it does NOT mean the document is unconnected. To find explicit relationships, use docgraph_graph operation=incoming/outgoing for citation links, or docgraph_context for topic neighbours.\n")
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
		fmt.Fprintf(&sb, "\n%d. **%s** %s%s\n", i+1, other.Name, other.FilePath, score)
	}

	return mcp.NewToolResultText(sb.String()), nil
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

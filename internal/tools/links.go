package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

// ---------------------------------------------------------------------------
// Shared helpers used by docgraph_graph facade operations (incoming, outgoing,
// impact, trace).
// ---------------------------------------------------------------------------

func (h *handler) resolveDoc(document string) (*store.Node, error) {
	if h.workspace != nil {
		n, _, err := h.workspace.FindNodeByPath(document)
		if err != nil {
			return nil, err
		}
		if n != nil {
			return n, nil
		}
		n, _, err = h.workspace.FindNodeByName(document)
		return n, err
	}
	n, err := h.store.FindNodeByPath(document)
	if err != nil {
		return nil, err
	}
	if n != nil {
		return n, nil
	}
	return h.store.FindNodeByName(document)
}

// ---------------------------------------------------------------------------
// Shared format strings for derived-count summary lines.
// Both the graph facade (renderIncomingLinks / renderOutgoingLinks) and
// handleNode use these constants so the output never drifts between callers.
// ---------------------------------------------------------------------------

const incomingSummaryFmt = "%d incoming edges ← %d distinct other documents, %d same-document references.\n"
const outgoingSummaryFmt = "%d outgoing edges → %d distinct other documents, %d same-document references, %d external URLs.\n"

// ---------------------------------------------------------------------------
// Edge classification helpers — compute derived counts over the FULL edge set.
// The facade AND handleNode both call these so the summary line is identical.
// ---------------------------------------------------------------------------

// incomingEdgeSummary counts, over the full incoming edge set for node, the
// number of edges from other documents (distinct file paths) and from nodes
// within the same file (same-document references). total == len(edges).
func (h *handler) incomingEdgeSummary(node *store.Node, st *store.Store, edges []store.Edge) (total, distinctOther, sameDoc int) {
	total = len(edges)
	otherDocs := map[string]bool{}
	for _, e := range edges {
		src := h.getNodeByIDFromStore(st, e.Source)
		if src == nil {
			continue
		}
		if src.FilePath == node.FilePath {
			sameDoc++
			continue
		}
		otherDocs[src.FilePath] = true
	}
	distinctOther = len(otherDocs)
	return
}

// outgoingEdgeSummary counts, over the full outgoing edge set for node, the
// number of edges to other documents (distinct file paths), to nodes within
// the same file (same-document references), and to external URLs.
// total == len(edges).
func (h *handler) outgoingEdgeSummary(node *store.Node, st *store.Store, edges []store.Edge) (total, distinctOther, sameDoc, external int) {
	total = len(edges)
	otherDocs := map[string]bool{}
	for _, e := range edges {
		if e.Kind == "links_external" {
			external++
			continue
		}
		tgt := h.getNodeByIDFromStore(st, e.Target)
		if tgt == nil {
			continue
		}
		if tgt.FilePath == node.FilePath {
			sameDoc++
			continue
		}
		otherDocs[tgt.FilePath] = true
	}
	distinctOther = len(otherDocs)
	return
}

// ---------------------------------------------------------------------------
// Renderers used by docgraph_graph facade operations
// ---------------------------------------------------------------------------

func (h *handler) renderIncomingLinks(document string, limit int) (*mcp.CallToolResult, error) {
	node, err := h.resolveDoc(document)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve document failed: %v", err)), nil
	}
	if node == nil {
		return mcp.NewToolResultError(fmt.Sprintf("document not found: %s — try docgraph_search to find the correct name or path", document)), nil
	}

	st := h.getStoreForResolvedNode(node)
	if st == nil {
		return mcp.NewToolResultError("store unavailable"), nil
	}
	allEdges, err := st.GetIncomingEdges(node.ID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("get incoming edges failed: %v", err)), nil
	}

	// Summarize over the FULL edge set, BEFORE the limit truncation, so the
	// derived counts are never silently computed over a truncated subset.
	// Same-document references (the citing node lives in this same file — an
	// intra-doc structural edge, not a citation from another document) are
	// separated out so the agent never has to classify or dedup by hand.
	total, distinctOther, selfRefCount := h.incomingEdgeSummary(node, st, allEdges)

	edges := allEdges
	if limit > 0 && len(edges) > limit {
		edges = edges[:limit]
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## References to %q\n\n", node.Name)
	fmt.Fprintf(&sb, incomingSummaryFmt, total, distinctOther, selfRefCount)
	if total > len(edges) {
		fmt.Fprintf(&sb, "Showing the first %d of %d edges (raise limit= for more); the counts above cover all %d.\n", len(edges), total, total)
	}

	for i, e := range edges {
		src := h.getNodeByIDFromStore(st, e.Source)
		if src == nil {
			fmt.Fprintf(&sb, "\n### %d. (unknown node %s)\n", i+1, e.Source)
			fmt.Fprintf(&sb, "- **Kind:** %s\n", e.Kind)
			continue
		}
		fmt.Fprintf(&sb, "\n### %d. %s\n", i+1, src.Name)
		fmt.Fprintf(&sb, "- **Kind:** %s\n", e.Kind)
		fmt.Fprintf(&sb, "- **Path:** %s\n", src.FilePath)
		if src.FilePath == node.FilePath {
			sb.WriteString("- **Note:** same-document reference (not a citation from another document)\n")
		}
		if e.Line > 0 {
			fmt.Fprintf(&sb, "- **Line:** %d\n", e.Line)
		}
	}
	// When all edges are shown (no truncation) and the edge count exceeds the
	// distinct-document count, surface the distinction so agents report the
	// right number and do not re-count raw rows as documents.
	if total == len(edges) && distinctOther != total {
		fmt.Fprintf(&sb, "\n(All %d edges above resolve to %d distinct other documents — report the distinct-document count, not the edge-row count.)\n", total, distinctOther)
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (h *handler) renderOutgoingLinks(document string, limit int) (*mcp.CallToolResult, error) {
	node, err := h.resolveDoc(document)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve document failed: %v", err)), nil
	}
	if node == nil {
		return mcp.NewToolResultError(fmt.Sprintf("document not found: %s — try docgraph_search to find the correct name or path", document)), nil
	}

	st := h.getStoreForResolvedNode(node)
	if st == nil {
		return mcp.NewToolResultError("store unavailable"), nil
	}
	oedges, err := st.GetOutgoingEdges(node.ID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("get outgoing edges failed: %v", err)), nil
	}

	// Summarize over the FULL edge set, BEFORE the limit truncation, so the
	// derived counts are never silently computed over a truncated subset.
	// Distinct other-document targets, same-document references (the target
	// lives in this same file — a heading link, not a link to another document),
	// and external URLs are reported separately so the agent never has to dedup
	// or classify the raw rows by hand.
	total, distinctOther, selfRefCount, externalCount := h.outgoingEdgeSummary(node, st, oedges)

	edges := oedges
	if limit > 0 && len(edges) > limit {
		edges = edges[:limit]
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Links from %q\n\n", node.Name)
	fmt.Fprintf(&sb, outgoingSummaryFmt, total, distinctOther, selfRefCount, externalCount)
	if total > len(edges) {
		fmt.Fprintf(&sb, "Showing the first %d of %d edges (raise limit= for more); the counts above cover all %d.\n", len(edges), total, total)
	}

	for i, e := range edges {
		if e.Kind == "links_external" {
			url := extractURL(e.Metadata)
			fmt.Fprintf(&sb, "\n### %d. External Link\n", i+1)
			fmt.Fprintf(&sb, "- **Kind:** %s\n", e.Kind)
			fmt.Fprintf(&sb, "- **URL:** %s\n", url)
			continue
		}

		tgt := h.getNodeByIDFromStore(st, e.Target)
		if tgt == nil {
			fmt.Fprintf(&sb, "\n### %d. (unknown node %s)\n", i+1, e.Target)
			fmt.Fprintf(&sb, "- **Kind:** %s\n", e.Kind)
			continue
		}
		fmt.Fprintf(&sb, "\n### %d. %s\n", i+1, tgt.Name)
		fmt.Fprintf(&sb, "- **Kind:** %s\n", e.Kind)
		fmt.Fprintf(&sb, "- **Path:** %s\n", tgt.FilePath)
		if tgt.FilePath == node.FilePath {
			sb.WriteString("- **Note:** same-document reference (not a link to another document)\n")
		}
	}
	// When all edges are shown (no truncation) and the edge count exceeds the
	// distinct-document count, surface the distinction so agents report the
	// right number and do not re-count raw rows as documents.
	if total == len(edges) && distinctOther != total {
		fmt.Fprintf(&sb, "\n(All %d edges above resolve to %d distinct other documents — report the distinct-document count, not the edge-row count.)\n", total, distinctOther)
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (h *handler) getNodeByID(id string) *store.Node {
	if h.workspace != nil {
		for _, p := range h.workspace.Projects {
			if n, err := p.Store.GetNodeByID(id); err == nil && n != nil {
				n.ProjectName = p.Name
				return n
			}
		}
		return nil
	}
	n, _ := h.store.GetNodeByID(id)
	return n
}

func (h *handler) getNodeByIDForNode(node *store.Node, id string) *store.Node {
	if st := h.getStoreForResolvedNode(node); st != nil {
		return h.getNodeByIDFromStore(st, id)
	}
	return h.getNodeByID(id)
}

func (h *handler) getNodeByIDFromStore(st *store.Store, id string) *store.Node {
	if st == nil {
		return nil
	}
	n, _ := st.GetNodeByID(id)
	return n
}

// extractURL pulls a "url" field from a JSON metadata string.
func extractURL(metadata string) string {
	if metadata == "" {
		return "(no URL)"
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(metadata), &m); err != nil {
		return metadata
	}
	if u, ok := m["url"].(string); ok {
		return u
	}
	return metadata
}

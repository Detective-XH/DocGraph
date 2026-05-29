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
	edges, err := st.GetIncomingEdges(node.ID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("get incoming edges failed: %v", err)), nil
	}

	if limit > 0 && len(edges) > limit {
		edges = edges[:limit]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## References to %q\n\nFound %d incoming references.\n", node.Name, len(edges)))

	for i, e := range edges {
		src := h.getNodeByIDFromStore(st, e.Source)
		if src == nil {
			sb.WriteString(fmt.Sprintf("\n### %d. (unknown node %s)\n", i+1, e.Source))
			sb.WriteString(fmt.Sprintf("- **Kind:** %s\n", e.Kind))
			continue
		}
		sb.WriteString(fmt.Sprintf("\n### %d. %s\n", i+1, src.Name))
		sb.WriteString(fmt.Sprintf("- **Kind:** %s\n", e.Kind))
		sb.WriteString(fmt.Sprintf("- **Path:** %s\n", src.FilePath))
		if e.Line > 0 {
			sb.WriteString(fmt.Sprintf("- **Line:** %d\n", e.Line))
		}
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
	edges := oedges

	if limit > 0 && len(edges) > limit {
		edges = edges[:limit]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Links from %q\n\nFound %d outgoing links.\n", node.Name, len(edges)))

	for i, e := range edges {
		if e.Kind == "links_external" {
			url := extractURL(e.Metadata)
			sb.WriteString(fmt.Sprintf("\n### %d. External Link\n", i+1))
			sb.WriteString(fmt.Sprintf("- **Kind:** %s\n", e.Kind))
			sb.WriteString(fmt.Sprintf("- **URL:** %s\n", url))
			continue
		}

		tgt := h.getNodeByIDFromStore(st, e.Target)
		if tgt == nil {
			sb.WriteString(fmt.Sprintf("\n### %d. (unknown node %s)\n", i+1, e.Target))
			sb.WriteString(fmt.Sprintf("- **Kind:** %s\n", e.Kind))
			continue
		}
		sb.WriteString(fmt.Sprintf("\n### %d. %s\n", i+1, tgt.Name))
		sb.WriteString(fmt.Sprintf("- **Kind:** %s\n", e.Kind))
		sb.WriteString(fmt.Sprintf("- **Path:** %s\n", tgt.FilePath))
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

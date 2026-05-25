package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

// ---------------------------------------------------------------------------
// Tool definitions
// ---------------------------------------------------------------------------

var referencesTool = mcp.NewTool("docgraph_references",
	mcp.WithDescription("Find all documents that reference a given document (incoming links). Like codegraph_callers but for docs. For broad impact analysis, use docgraph_impact instead."),
	mcp.WithString("document", mcp.Required(), mcp.Description("Document name, path, or heading to find references to")),
	mcp.WithNumber("limit", mcp.Description("Max results (default 10)")),
)

var linksTool = mcp.NewTool("docgraph_links",
	mcp.WithDescription("Find all documents and headings that a given document links to (outgoing links). Like codegraph_callees but for docs."),
	mcp.WithString("document", mcp.Required(), mcp.Description("Document name or path")),
	mcp.WithNumber("limit", mcp.Description("Max results (default 10)")),
)

// ---------------------------------------------------------------------------
// Shared helper
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
// Handlers
// ---------------------------------------------------------------------------

func (h *handler) handleReferences(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	document := getStringArg(args, "document", "")
	if document == "" {
		return mcp.NewToolResultError("document parameter is required"), nil
	}
	document = sanitizeArg(document, maxArgLength)
	limit := getIntArg(args, "limit", 10)

	node, err := h.resolveDoc(document)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve document failed: %v", err)), nil
	}
	if node == nil {
		return mcp.NewToolResultError(fmt.Sprintf("document not found: %s — try docgraph_search to find the correct name or path", document)), nil
	}

	var edges []store.Edge
	if h.workspace != nil {
		for _, p := range h.workspace.Projects {
			if es, err := p.Store.GetIncomingEdges(node.ID); err == nil {
				edges = append(edges, es...)
			}
		}
	} else {
		var err error
		edges, err = h.store.GetIncomingEdges(node.ID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("get incoming edges failed: %v", err)), nil
		}
	}

	if limit > 0 && len(edges) > limit {
		edges = edges[:limit]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## References to %q\n\nFound %d incoming references.\n", node.Name, len(edges)))

	for i, e := range edges {
		src := h.getNodeByID(e.Source)
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

func (h *handler) handleLinks(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	document := getStringArg(args, "document", "")
	if document == "" {
		return mcp.NewToolResultError("document parameter is required"), nil
	}
	document = sanitizeArg(document, maxArgLength)
	limit := getIntArg(args, "limit", 10)

	node, err := h.resolveDoc(document)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve document failed: %v", err)), nil
	}
	if node == nil {
		return mcp.NewToolResultError(fmt.Sprintf("document not found: %s — try docgraph_search to find the correct name or path", document)), nil
	}

	var oedges []store.Edge
	if h.workspace != nil {
		for _, p := range h.workspace.Projects {
			if es, err := p.Store.GetOutgoingEdges(node.ID); err == nil {
				oedges = append(oedges, es...)
			}
		}
	} else {
		var err error
		oedges, err = h.store.GetOutgoingEdges(node.ID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("get outgoing edges failed: %v", err)), nil
		}
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

		tgt := h.getNodeByID(e.Target)
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
				return n
			}
		}
		return nil
	}
	n, _ := h.store.GetNodeByID(id)
	return n
}

// extractURL pulls a "url" field from a JSON metadata string.
func extractURL(metadata string) string {
	if metadata == "" {
		return "(no URL)"
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(metadata), &m); err != nil {
		return metadata
	}
	if u, ok := m["url"].(string); ok {
		return u
	}
	return metadata
}

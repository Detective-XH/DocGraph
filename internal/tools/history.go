package tools

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

var historyTool = mcp.NewTool("docgraph_history",
	mcp.WithDescription("Show git commit history for a document: how many times it was amended, by how many authors, first/last change dates, and the most recent commit message. Returns empty for files not tracked by git (gitignored or untracked)."),
	mcp.WithString("document", mcp.Required(), mcp.Description("Document name or path")),
)

func (h *handler) handleHistory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	document := sanitizeArg(getStringArg(req.GetArguments(), "document", ""), maxArgLength)
	if document == "" {
		return mcp.NewToolResultError("document parameter is required"), nil
	}

	node, err := h.resolveDoc(document)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve failed: %v", err)), nil
	}
	if node == nil {
		return mcp.NewToolResultError(fmt.Sprintf("document not found: %s — try docgraph_search to find the correct name or path", document)), nil
	}

	s := h.getStoreForResolvedNode(node)
	if s == nil {
		return mcp.NewToolResultError("store unavailable"), nil
	}

	text, err := historyText(s, node.Name, node.FilePath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("history query failed: %v", err)), nil
	}
	return mcp.NewToolResultText(text), nil
}

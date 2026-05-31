package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var graphFacadeTool = mcp.NewTool("docgraph_graph",
	mcp.WithDescription("Document graph traversal facade. Operations:\n  incoming — documents that cite/reference this document\n  outgoing — documents this document links to\n  impact   — transitive incoming references (who depends on this)\n  trace    — shortest forward path from document A to document B over reference edges (markdown links, wikilinks, embeds); one-directional, ignores similarity/tag edges; \"no path\" ≠ \"unrelated\""),
	mcp.WithString("operation", mcp.Required(), mcp.Description("Graph operation: incoming, outgoing, impact, or trace")),
	mcp.WithString("document", mcp.Description("Document name or path for incoming, outgoing, and impact. When copying a path from docgraph_search results, strip the trailing '#heading:line' suffix (and any '[project/]' prefix in workspace mode) to the bare file path before passing it here.")),
	mcp.WithString("from", mcp.Description("Starting document name or path for trace")),
	mcp.WithString("to", mcp.Description("Target document name or path for trace")),
	mcp.WithNumber("depth", mcp.Description("Impact depth (default 2, max 5)")),
	mcp.WithNumber("limit", mcp.Description("Max incoming/outgoing results (default 10)")),
)

func registerGraphFacadeTool(s *server.MCPServer, h *handler) {
	s.AddTool(graphFacadeTool, h.guardIndexing(h.handleGraphFacade))
}

func (h *handler) handleGraphFacade(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	operation := strings.ToLower(strings.TrimSpace(sanitizeArg(getStringArg(args, "operation", ""), 100)))

	switch operation {
	case "incoming":
		if hasGraphTraceArgs(args) {
			return mcp.NewToolResultError("from and to parameters are only valid for operation=trace"), nil
		}
		document := sanitizeArg(getStringArg(args, "document", ""), maxArgLength)
		if document == "" {
			return mcp.NewToolResultError("document parameter is required"), nil
		}
		return h.renderIncomingLinks(document, getIntArgClamped(args, "limit", 10, 0, maxListLimit))
	case "outgoing":
		if hasGraphTraceArgs(args) {
			return mcp.NewToolResultError("from and to parameters are only valid for operation=trace"), nil
		}
		document := sanitizeArg(getStringArg(args, "document", ""), maxArgLength)
		if document == "" {
			return mcp.NewToolResultError("document parameter is required"), nil
		}
		return h.renderOutgoingLinks(document, getIntArgClamped(args, "limit", 10, 0, maxListLimit))
	case "impact":
		if hasGraphTraceArgs(args) {
			return mcp.NewToolResultError("from and to parameters are only valid for operation=trace"), nil
		}
		document := sanitizeArg(getStringArg(args, "document", ""), maxArgLength)
		if document == "" {
			return mcp.NewToolResultError("document parameter is required"), nil
		}
		return h.renderImpact(document, getIntArgClamped(args, "depth", 2, 1, 5))
	case "trace":
		if sanitizeArg(getStringArg(args, "document", ""), maxArgLength) != "" {
			return mcp.NewToolResultError("document parameter is only valid for operation=incoming, operation=outgoing, and operation=impact"), nil
		}
		from := sanitizeArg(getStringArg(args, "from", ""), maxArgLength)
		to := sanitizeArg(getStringArg(args, "to", ""), maxArgLength)
		if from == "" || to == "" {
			return mcp.NewToolResultError("both 'from' and 'to' parameters are required"), nil
		}
		return h.renderTrace(from, to)
	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown operation %q: valid operations are incoming, outgoing, impact, trace", operation)), nil
	}
}

func hasGraphTraceArgs(args map[string]any) bool {
	return sanitizeArg(getStringArg(args, "from", ""), maxArgLength) != "" ||
		sanitizeArg(getStringArg(args, "to", ""), maxArgLength) != ""
}

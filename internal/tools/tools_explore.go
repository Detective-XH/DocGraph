package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

var exploreTool = mcp.NewTool("docgraph_explore",
	mcp.WithDescription("Survey several related documents and their cross-references in one call. More efficient than multiple docgraph_node calls. For a single known document, use docgraph_node instead. For governance filters or structured context, use docgraph_context instead."),
	mcp.WithString("query", mcp.Required(), mcp.Description("Search terms to find related documents")),
	mcp.WithNumber("maxDocs", mcp.Description("Max documents (default 5)")),
	mcp.WithString("project", mcp.Description("Workspace mode only: scope results to a single project by name (the directory name shown in docgraph_status). Omit to query all projects. No-op in single-store mode.")),
)

func (h *handler) handleExplore(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	query := getStringArg(args, "query", "")
	if query == "" {
		return mcp.NewToolResultError("query parameter is required"), nil
	}
	query = sanitizeArg(query, maxArgLength)
	maxDocs := getIntArgClamped(args, "maxDocs", 5, 1, 200)
	projectFilter := sanitizeArg(getStringArg(args, "project", ""), maxArgLength)

	var results []store.SearchResult
	var err error
	if h.workspace != nil {
		results, err = h.workspace.SearchWithOptions(store.SearchOptions{Query: query, Limit: maxDocs, ProjectFilter: projectFilter})
	} else {
		results, err = h.store.Search(query, "", maxDocs)
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Explore: %q\n\n", query)

	for i, sr := range results {
		node := sr.Node
		headings := h.getHeadings(&node)
		inCount, outCount := h.getEdgeCounts(&node)

		headingNames := make([]string, len(headings))
		for j, hd := range headings {
			headingNames[j] = hd.Name
		}

		fmt.Fprintf(&sb, "### %d. %s (%s)\n", i+1, node.Name, node.FilePath)
		if len(headingNames) > 0 {
			fmt.Fprintf(&sb, "Headings: %s\n", strings.Join(headingNames, ", "))
		}
		fmt.Fprintf(&sb, "Links out: %d | Links in: %d\n", outCount, inCount)

		if node.BodyExcerpt != "" {
			for line := range strings.SplitSeq(strings.TrimRight(node.BodyExcerpt, "\n"), "\n") {
				fmt.Fprintf(&sb, "> %s\n", line)
			}
		}
		sb.WriteString("\n")
	}

	return mcp.NewToolResultText(sb.String()), nil
}

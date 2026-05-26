package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

var historyTool = mcp.NewTool("docgraph_history",
	mcp.WithDescription("Show git commit history for a document: how many times it was amended, by how many authors, first/last change dates, and the most recent commit message."),
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

	hist, err := s.GetFileHistory(node.FilePath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("history query failed: %v", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## History: %s\n\n", node.Name))
	sb.WriteString(fmt.Sprintf("**Path:** %s\n", node.FilePath))

	if hist == nil || hist.CommitCount == 0 {
		sb.WriteString("\nNo git history found. The file may be untracked or outside a git repository.\n")
		return mcp.NewToolResultText(sb.String()), nil
	}

	amendWord := "time"
	if hist.CommitCount != 1 {
		amendWord = "times"
	}
	authorWord := "author"
	if hist.AuthorCount != 1 {
		authorWord = "authors"
	}

	sb.WriteString(fmt.Sprintf("**Commits:** %d — amended **%d %s** by **%d %s**\n",
		hist.CommitCount, hist.CommitCount, amendWord, hist.AuthorCount, authorWord))
	if hist.LastAuthor != "" {
		sb.WriteString(fmt.Sprintf("**Last author:** %s\n", hist.LastAuthor))
	}
	if hist.LastSubject != "" {
		sb.WriteString(fmt.Sprintf("**Last commit:** %s\n", hist.LastSubject))
	}
	if hist.FirstCommitAt > 0 {
		sb.WriteString(fmt.Sprintf("**First changed:** %s\n", time.Unix(hist.FirstCommitAt, 0).UTC().Format("2006-01-02")))
	}
	if hist.LastCommitAt > 0 {
		sb.WriteString(fmt.Sprintf("**Last changed:** %s\n", time.Unix(hist.LastCommitAt, 0).UTC().Format("2006-01-02")))
	}

	return mcp.NewToolResultText(sb.String()), nil
}

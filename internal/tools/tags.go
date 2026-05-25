package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

var tagsTool = mcp.NewTool("docgraph_tags",
	mcp.WithDescription("List all tags across indexed documents with document counts, or find all documents with a specific tag."),
	mcp.WithString("tag", mcp.Description("Tag name to filter by. If omitted, lists all tags with counts.")),
)

func (h *handler) handleTags(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tagFilter := sanitizeArg(getStringArg(req.GetArguments(), "tag", ""), maxArgLength)

	if tagFilter != "" {
		return h.handleTagFilter(tagFilter)
	}
	return h.handleTagList()
}

func (h *handler) handleTagList() (*mcp.CallToolResult, error) {
	var allTags []store.TagCount
	if h.workspace != nil {
		seen := map[string]int{}
		for _, p := range h.workspace.Projects {
			tags, err := p.Store.GetAllTags()
			if err != nil {
				continue
			}
			for _, t := range tags {
				seen[t.Name] += t.Count
			}
		}
		for name, cnt := range seen {
			allTags = append(allTags, store.TagCount{Name: name, Count: cnt})
		}
		sort.Slice(allTags, func(i, j int) bool {
			if allTags[i].Count != allTags[j].Count {
				return allTags[i].Count > allTags[j].Count
			}
			return allTags[i].Name < allTags[j].Name
		})
	} else {
		tags, err := h.store.GetAllTags()
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("query failed: %v", err)), nil
		}
		allTags = tags
	}

	if len(allTags) == 0 {
		return mcp.NewToolResultText("## Tags\n\nNo tags found. Add `tags:` to document frontmatter to enable tag navigation.\n"), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Tags (%d total)\n\n", len(allTags)))
	for _, t := range allTags {
		sb.WriteString(fmt.Sprintf("- **%s** (%d doc", t.Name, t.Count))
		if t.Count != 1 {
			sb.WriteString("s")
		}
		sb.WriteString(")\n")
	}
	return mcp.NewToolResultText(sb.String()), nil
}

func (h *handler) handleTagFilter(tagName string) (*mcp.CallToolResult, error) {
	var nodes []store.Node
	if h.workspace != nil {
		for _, p := range h.workspace.Projects {
			ns, err := p.Store.GetDocumentsByTag(tagName)
			if err != nil {
				continue
			}
			nodes = append(nodes, ns...)
		}
	} else {
		ns, err := h.store.GetDocumentsByTag(tagName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("query failed: %v", err)), nil
		}
		nodes = ns
	}

	if len(nodes) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("## Tag: %q\n\nNo documents found with this tag.\n", tagName)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Tag: %q (%d document", tagName, len(nodes)))
	if len(nodes) != 1 {
		sb.WriteString("s")
	}
	sb.WriteString(")\n\n")
	for _, n := range nodes {
		sb.WriteString(fmt.Sprintf("- **%s** — `%s`\n", n.Name, n.FilePath))
	}
	return mcp.NewToolResultText(sb.String()), nil
}

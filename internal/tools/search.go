package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

// ---------------------------------------------------------------------------
// Tool definitions
// ---------------------------------------------------------------------------

var searchTool = mcp.NewTool("docgraph_search",
	mcp.WithDescription("Full-text search across all indexed Markdown documents. Returns matching documents and headings with snippets. For topic-level context, prefer docgraph_context which combines search with structure."),
	mcp.WithString("query", mcp.Required(), mcp.Description("Search terms")),
	mcp.WithString("kind", mcp.Description("Filter by node kind: document, heading, definition, tag")),
	mcp.WithNumber("limit", mcp.Description("Max results (default 10)")),
)

var filesTool = mcp.NewTool("docgraph_files",
	mcp.WithDescription("List indexed Markdown files. Use path filter to narrow scope. For a single known doc, use docgraph_node instead."),
	mcp.WithString("path", mcp.Description("Filter to directory subtree")),
	mcp.WithNumber("limit", mcp.Description("Max files to return (default 50)")),
)

var statusTool = mcp.NewTool("docgraph_status",
	mcp.WithDescription("Index health: file count, node count, edge count, unresolved references, DB size."),
)

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (h *handler) handleSearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	query := getStringArg(args, "query", "")
	if query == "" {
		return mcp.NewToolResultError("query parameter is required"), nil
	}
	query = sanitizeArg(query, maxArgLength)
	kind := getStringArg(args, "kind", "")
	limit := getIntArg(args, "limit", 10)

	var results []store.SearchResult
	var err error
	if h.workspace != nil {
		results, err = h.workspace.Search(query, kind, limit)
	} else {
		results, err = h.store.Search(query, kind, limit)
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Search Results for %q\n\nFound %d results.\n", query, len(results)))

	for i, sr := range results {
		n := sr.Node
		path := n.FilePath
		if n.Kind == "heading" && n.QualifiedName != "" {
			path = n.QualifiedName
		}
		sb.WriteString(fmt.Sprintf("\n%d. **%s** [%s] %s:%d-%d\n", i+1, n.Name, n.Kind, path, n.StartLine, n.EndLine))

		if n.BodyExcerpt != "" {
			excerpt := strings.TrimRight(n.BodyExcerpt, "\n")
			firstLine := strings.SplitN(excerpt, "\n", 2)[0]
			if len(firstLine) > 100 {
				firstLine = firstLine[:100] + "..."
			}
			sb.WriteString(fmt.Sprintf("   > %s\n", firstLine))
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (h *handler) handleFiles(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	pathFilter := getStringArg(args, "path", "")
	pathFilter = sanitizeArg(pathFilter, maxArgLength)
	fileLimit := getIntArg(args, "limit", 50)

	var files []store.FileInfo
	var err error
	if h.workspace != nil {
		allFiles, ferr := h.workspace.GetAllFiles(pathFilter)
		if ferr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("list files failed: %v", ferr)), nil
		}
		for proj, fs := range allFiles {
			for i := range fs {
				fs[i].Path = "[" + proj + "] " + fs[i].Path
			}
			files = append(files, fs...)
		}
	} else {
		files, err = h.store.GetFiles(pathFilter)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("list files failed: %v", err)), nil
		}
	}

	total := len(files)
	if fileLimit > 0 && len(files) > fileLimit {
		files = files[:fileLimit]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Indexed Files (%d total", total))
	if total > len(files) {
		sb.WriteString(fmt.Sprintf(", showing %d", len(files)))
	}
	sb.WriteString(")\n\n")
	sb.WriteString("| Path | Size | Nodes | Frontmatter |\n")
	sb.WriteString("|------|------|-------|-------------|\n")

	for _, f := range files {
		fm := "No"
		if f.HasFrontmatter {
			fm = "Yes"
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %d | %s |\n", f.Path, formatSize(f.Size), f.NodeCount, fm))
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (h *handler) handleStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var sb strings.Builder

	if h.workspace != nil {
		allStats, err := h.workspace.GetAllStats()
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("get stats failed: %v", err)), nil
		}
		sb.WriteString("## DocGraph Workspace Status\n\n")
		sb.WriteString("| Project | Files | Nodes | Edges | Unresolved | DB Size |\n")
		sb.WriteString("|---------|-------|-------|-------|------------|--------|\n")
		for name, s := range allStats {
			sb.WriteString(fmt.Sprintf("| %s | %d | %d | %d | %d | %s |\n",
				name, s.FileCount, s.NodeCount, s.EdgeCount, s.UnresolvedCount, formatSize(s.DBSizeBytes)))
		}

		// Neural embeddings — fan-out across all projects.
		var allEmbStats []store.EmbeddingModelStat
		modelTotals := make(map[string]*store.EmbeddingModelStat)
		for _, p := range h.workspace.Projects {
			if embStats, err := p.Store.GetEmbeddingModelStats(); err == nil {
				for _, es := range embStats {
					if m, ok := modelTotals[es.ModelID]; ok {
						m.Total += es.Total
						m.Stale += es.Stale
					} else {
						cp := es
						modelTotals[es.ModelID] = &cp
					}
				}
			}
		}
		for _, s := range modelTotals {
			allEmbStats = append(allEmbStats, *s)
		}
		appendEmbeddingStats(&sb, allEmbStats)
	} else {
		stats, err := h.store.GetStats()
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("get stats failed: %v", err)), nil
		}
		sb.WriteString(fmt.Sprintf("Files: %d | Nodes: %d | Edges: %d | Unresolved: %d | DB: %s\n\n",
			stats.FileCount, stats.NodeCount, stats.EdgeCount, stats.UnresolvedCount, formatSize(stats.DBSizeBytes)))
		if len(stats.NodesByKind) > 2 {
			sb.WriteString("### Nodes by Kind\n| Kind | Count |\n|------|-------|\n")
			for kind, count := range stats.NodesByKind {
				sb.WriteString(fmt.Sprintf("| %s | %d |\n", kind, count))
			}
		}
		if len(stats.EdgesByKind) > 2 {
			sb.WriteString("\n### Edges by Kind\n| Kind | Count |\n|------|-------|\n")
			for kind, count := range stats.EdgesByKind {
				sb.WriteString(fmt.Sprintf("| %s | %d |\n", kind, count))
			}
		}

		embStats, err := h.store.GetEmbeddingModelStats()
		if err == nil {
			appendEmbeddingStats(&sb, embStats)
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func appendEmbeddingStats(sb *strings.Builder, stats []store.EmbeddingModelStat) {
	if len(stats) == 0 {
		return
	}
	sb.WriteString("\n### Neural Embeddings\n| Model | Total | Stale |\n|-------|-------|-------|\n")
	for _, s := range stats {
		sb.WriteString(fmt.Sprintf("| %s | %d | %d |\n", s.ModelID, s.Total, s.Stale))
	}
}

// ---------------------------------------------------------------------------
// MCP input length limits
// ---------------------------------------------------------------------------

const maxArgLength = 10000

func sanitizeArg(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func getStringArg(args map[string]interface{}, key string, defaultVal string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return defaultVal
	}
	s, ok := v.(string)
	if !ok {
		return defaultVal
	}
	return s
}

func getIntArg(args map[string]interface{}, key string, defaultVal int) int {
	v, ok := args[key]
	if !ok || v == nil {
		return defaultVal
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return defaultVal
	}
}

func getBoolArg(args map[string]interface{}, key string, defaultVal bool) bool {
	v, ok := args[key]
	if !ok || v == nil {
		return defaultVal
	}
	b, ok := v.(bool)
	if !ok {
		return defaultVal
	}
	return b
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(bytes)/1024/1024)
	case bytes >= 1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

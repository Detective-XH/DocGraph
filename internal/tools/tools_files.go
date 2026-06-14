package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

var filesTool = mcp.NewTool("docgraph_files",
	mcp.WithDescription("List all indexed files (.md, .docx, .html, .pdf). Use path filter to narrow scope (bare directory name, e.g. path=docs). For a single known doc, use docgraph_node instead."),
	mcp.WithString("path", mcp.Description("Filter to directory subtree (bare directory name, e.g. docs or reports/2024)")),
	mcp.WithNumber("limit", mcp.Description("Max files to return (default 50)")),
	mcp.WithString("project", mcp.Description("Workspace mode only: scope results to a single project by name (the directory name shown in docgraph_status). Omit to query all projects. No-op in single-store mode.")),
)

// buildFilesEmptyResponse constructs the explanatory zero-result response for
// handleFiles. When pathFilter is non-empty, it queries the store/workspace for
// known top-level directories to give the agent a useful hint.
func (h *handler) buildFilesEmptyResponse(pathFilter, projectFilter string) *mcp.CallToolResult {
	if pathFilter != "" {
		var sb strings.Builder
		fmt.Fprintf(&sb, "No indexed files found under path %q.\n", pathFilter)
		var dirs []string
		var dirsErr error
		if h.workspace != nil {
			dirs, dirsErr = h.workspace.GetAllTopLevelDirs(projectFilter)
		} else {
			dirs, dirsErr = h.store.GetTopLevelDirs()
		}
		if dirsErr == nil && len(dirs) > 0 {
			fmt.Fprintf(&sb, "Known top-level indexed directories: %s", strings.Join(dirs, ", "))
		} else {
			sb.WriteString("The index appears to be empty.")
		}
		return mcp.NewToolResultText(sb.String())
	}
	return mcp.NewToolResultText("No files have been indexed yet.")
}

func (h *handler) handleFiles(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	pathFilter := getStringArg(args, "path", "")
	pathFilter = sanitizeArg(pathFilter, maxArgLength)
	fileLimit := getIntArgClamped(args, "limit", 50, 0, maxListLimit)
	projectFilter := sanitizeArg(getStringArg(args, "project", ""), maxArgLength)

	var files []store.FileInfo
	var err error
	if h.workspace != nil {
		allFiles, ferr := h.workspace.GetAllFiles(pathFilter, projectFilter)
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

	// Zero-result guard: explain WHY no results were returned before falling
	// back to an empty table that leaves agents without actionable context.
	if len(files) == 0 {
		return h.buildFilesEmptyResponse(pathFilter, projectFilter), nil
	}

	total := len(files)

	if fileLimit > 0 && len(files) > fileLimit {
		files = files[:fileLimit]
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Indexed Files (%d total", total)
	if total > len(files) {
		fmt.Fprintf(&sb, ", showing %d", len(files))
	}
	sb.WriteString(")\n\n")
	sb.WriteString("| Path | Size | Nodes | Frontmatter |\n")
	sb.WriteString("|------|------|-------|-------------|\n")

	for _, f := range files {
		fm := "No"
		if f.HasFrontmatter {
			fm = "Yes"
		}
		fmt.Fprintf(&sb, "| %s | %s | %d | %s |\n", f.Path, formatSize(f.Size), f.NodeCount, fm)
	}

	return mcp.NewToolResultText(sb.String()), nil
}

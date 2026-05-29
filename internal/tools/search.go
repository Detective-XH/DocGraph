package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

// ---------------------------------------------------------------------------
// Tool definitions
// ---------------------------------------------------------------------------

var searchTool = mcp.NewTool("docgraph_search",
	mcp.WithDescription("Full-text search across all indexed Markdown documents. Returns matching documents and headings with snippets. For topic-level context, prefer docgraph_context which combines search with structure. Use governance and research filters to constrain retrieval without adding separate tools."),
	mcp.WithString("query", mcp.Required(), mcp.Description("Search terms")),
	mcp.WithString("kind", mcp.Description("Filter by node kind: document, heading, definition, tag. Code files (code_doc pack) are excluded by default; pass kind=code_file or include_code=true to surface them.")),
	mcp.WithNumber("limit", mcp.Description("Max results (default 10)")),
	mcp.WithBoolean("include_code", mcp.Description("Include code_file results from the code_doc pack alongside documentation. Default false (docs only).")),
	mcp.WithString("status", mcp.Description("Filter by governance status (e.g. approved, draft, superseded). Requires metadata reindex.")),
	mcp.WithString("sensitivity", mcp.Description("Filter by sensitivity (e.g. public, internal, confidential, restricted). Requires metadata reindex.")),
	mcp.WithString("canonical_source", mcp.Description("Filter by canonical source marker or value. Requires metadata reindex.")),
	mcp.WithString("allowed_audience", mcp.Description("Filter to documents available to an audience label. Public documents are included.")),
	mcp.WithString("as_of_date", mcp.Description("Evaluate effective_date and valid_until against YYYY-MM-DD.")),
	mcp.WithString("claim_id", mcp.Description("Filter by research claim_id. Requires metadata reindex.")),
	mcp.WithString("source_type", mcp.Description("Filter by research source_type (e.g. primary, secondary, internal). Requires metadata reindex.")),
	mcp.WithString("confidence", mcp.Description("Filter by research confidence (e.g. high, medium, low). Requires metadata reindex.")),
	mcp.WithString("analyst_status", mcp.Description("Filter by research analyst_status. Requires metadata reindex.")),
	mcp.WithString("entity_type", mcp.Description("Filter to documents that mention entities of this type (e.g. person, organization).")),
	mcp.WithString("entity_id", mcp.Description("Filter to documents that mention a specific entity UUID.")),
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
	limit := getIntArgClamped(args, "limit", 10, 1, 200)
	includeCode := getBoolArg(args, "include_code", false)
	statusFilter := sanitizeArg(getStringArg(args, "status", ""), 100)
	sensitivityFilter := sanitizeArg(getStringArg(args, "sensitivity", ""), 100)
	canonicalSourceFilter := sanitizeArg(getStringArg(args, "canonical_source", ""), 300)
	allowedAudienceFilter := sanitizeArg(getStringArg(args, "allowed_audience", ""), 100)
	asOfDateFilter := sanitizeArg(getStringArg(args, "as_of_date", ""), 20)
	claimIDFilter := sanitizeArg(getStringArg(args, "claim_id", ""), 100)
	sourceTypeFilter := sanitizeArg(getStringArg(args, "source_type", ""), 100)
	confidenceFilter := sanitizeArg(getStringArg(args, "confidence", ""), 100)
	analystStatusFilter := sanitizeArg(getStringArg(args, "analyst_status", ""), 100)

	opts := store.SearchOptions{
		Query:       query,
		Kind:        kind,
		Limit:       limit,
		IncludeCode: includeCode,
		Governance: store.GovernanceSearchOptions{
			Status:          statusFilter,
			Sensitivity:     sensitivityFilter,
			CanonicalSource: canonicalSourceFilter,
			AllowedAudience: allowedAudienceFilter,
			AsOfDate:        asOfDateFilter,
		},
		Entity: parseEntityFilters(args),
		Research: store.ResearchSearchOptions{
			ClaimID:       claimIDFilter,
			SourceType:    sourceTypeFilter,
			Confidence:    confidenceFilter,
			AnalystStatus: analystStatusFilter,
		},
	}
	useMetadataFilters := opts.HasMetadataFilters()

	var sb strings.Builder

	var results []store.SearchResult
	var err error
	if h.workspace != nil {
		results, err = h.workspace.SearchWithOptions(opts)
	} else {
		results, err = h.store.SearchWithOptions(opts)
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	if useMetadataFilters {
		fmt.Fprintf(&sb, "## Search Results for %q [metadata filter: %s]\n\nFound %d results.\n",
			query, describeSearchFilters(opts), len(results))
	} else {
		fmt.Fprintf(&sb, "## Search Results for %q\n\nFound %d results.\n", query, len(results))
	}

	for i, sr := range results {
		n := sr.Node
		path := formatNodePath(n)
		if n.Kind == "heading" && n.QualifiedName != "" {
			path = n.QualifiedName
		}
		fmt.Fprintf(&sb, "\n%d. **%s** [%s] %s:%d-%d\n", i+1, n.Name, n.Kind, path, n.StartLine, n.EndLine)

		if n.BodyExcerpt != "" {
			excerpt := strings.TrimRight(n.BodyExcerpt, "\n")
			firstLine, _, _ := strings.Cut(excerpt, "\n")
			if len(firstLine) > 100 {
				firstLine = firstLine[:100] + "..."
			}
			fmt.Fprintf(&sb, "   > %s\n", firstLine)
		}
		if st := h.getStoreForResolvedNode(&n); st != nil {
			if quality, err := st.GetMetadataQuality(n.ID, time.Time{}); err == nil && quality != nil {
				fmt.Fprintf(&sb, "   Quality: %d/100 %s%s\n",
					quality.Score, quality.Level, formatQualityIssueCodes(quality.Issues, 3))
			}
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func describeSearchFilters(opts store.SearchOptions) string {
	parts := []string{
		fmt.Sprintf("status=%q", opts.Governance.Status),
		fmt.Sprintf("sensitivity=%q", opts.Governance.Sensitivity),
		fmt.Sprintf("canonical_source=%q", opts.Governance.CanonicalSource),
		fmt.Sprintf("allowed_audience=%q", opts.Governance.AllowedAudience),
		fmt.Sprintf("as_of_date=%q", opts.Governance.AsOfDate),
		fmt.Sprintf("claim_id=%q", opts.Research.ClaimID),
		fmt.Sprintf("source_type=%q", opts.Research.SourceType),
		fmt.Sprintf("confidence=%q", opts.Research.Confidence),
		fmt.Sprintf("analyst_status=%q", opts.Research.AnalystStatus),
	}
	return strings.Join(parts, " ")
}

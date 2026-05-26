package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Detective-XH/docgraph/internal/domainpacks"
	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

// ---------------------------------------------------------------------------
// Tool definitions
// ---------------------------------------------------------------------------

var searchTool = mcp.NewTool("docgraph_search",
	mcp.WithDescription("Full-text search across all indexed Markdown documents. Returns matching documents and headings with snippets. For topic-level context, prefer docgraph_context which combines search with structure. Use governance and research filters to constrain retrieval without adding separate tools."),
	mcp.WithString("query", mcp.Required(), mcp.Description("Search terms")),
	mcp.WithString("kind", mcp.Description("Filter by node kind: document, heading, definition, tag")),
	mcp.WithNumber("limit", mcp.Description("Max results (default 10)")),
	mcp.WithString("status", mcp.Description("Filter by governance status (e.g. approved, draft, superseded). Requires metadata reindex.")),
	mcp.WithString("sensitivity", mcp.Description("Filter by sensitivity (e.g. public, internal, confidential, restricted). Requires metadata reindex.")),
	mcp.WithString("canonical_source", mcp.Description("Filter by canonical source marker or value. Requires metadata reindex.")),
	mcp.WithString("allowed_audience", mcp.Description("Filter to documents available to an audience label. Public documents are included.")),
	mcp.WithString("as_of_date", mcp.Description("Evaluate effective_date and valid_until against YYYY-MM-DD.")),
	mcp.WithString("claim_id", mcp.Description("Filter by research claim_id. Requires metadata reindex.")),
	mcp.WithString("source_type", mcp.Description("Filter by research source_type (e.g. primary, secondary, internal). Requires metadata reindex.")),
	mcp.WithString("confidence", mcp.Description("Filter by research confidence (e.g. high, medium, low). Requires metadata reindex.")),
	mcp.WithString("analyst_status", mcp.Description("Filter by research analyst_status. Requires metadata reindex.")),
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
		Query: query,
		Kind:  kind,
		Limit: limit,
		Governance: store.GovernanceSearchOptions{
			Status:          statusFilter,
			Sensitivity:     sensitivityFilter,
			CanonicalSource: canonicalSourceFilter,
			AllowedAudience: allowedAudienceFilter,
			AsOfDate:        asOfDateFilter,
		},
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
		sb.WriteString(fmt.Sprintf("## Search Results for %q [metadata filter: %s]\n\nFound %d results.\n",
			query, describeSearchFilters(opts), len(results)))
		if len(results) == 0 && h.metadataReindexPending() {
			sb.WriteString("\n> **Note:** metadata reindex pending — run `docgraph index --force` to populate governance filters.\n")
		}
	} else {
		sb.WriteString(fmt.Sprintf("## Search Results for %q\n\nFound %d results.\n", query, len(results)))
	}

	for i, sr := range results {
		n := sr.Node
		path := formatNodePath(n)
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
		if st := h.getStoreForResolvedNode(&n); st != nil {
			if quality, err := st.GetMetadataQuality(n.ID, time.Time{}); err == nil && quality != nil {
				sb.WriteString(fmt.Sprintf("   Quality: %d/100 %s%s\n",
					quality.Score, quality.Level, formatQualityIssueCodes(quality.Issues, 3)))
			}
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (h *handler) getWorkspaceMetadataFilteredNodes(status, sensitivity, claimID, sourceType, confidence, analystStatus string, limit int) []store.Node {
	var out []store.Node
	for _, p := range h.workspace.Projects {
		ns, err := h.getMetadataFilteredNodes(p.Store, status, sensitivity, claimID, sourceType, confidence, analystStatus, limit)
		if err == nil {
			for i := range ns {
				ns[i].ProjectName = p.Name
				if ns[i].QualifiedName != "" && !strings.HasPrefix(ns[i].QualifiedName, "[") {
					ns[i].QualifiedName = "[" + p.Name + "] " + ns[i].QualifiedName
				}
			}
			out = append(out, ns...)
		}
	}
	return out
}

func nodeMatchesSearchQuery(n store.Node, query, kind string) bool {
	if kind != "" && n.Kind != kind {
		return false
	}
	haystack := strings.ToLower(strings.Join([]string{
		n.Name,
		n.QualifiedName,
		n.BodyExcerpt,
		n.Metadata,
	}, "\n"))
	for _, word := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(haystack, word) {
			return false
		}
	}
	return true
}

func formatNodePath(n store.Node) string {
	if n.ProjectName == "" {
		return n.FilePath
	}
	return "[" + n.ProjectName + "] " + n.FilePath
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

func (h *handler) metadataReindexPending() bool {
	if h.store != nil {
		reindexVal, reindexFound, _ := h.store.GetProjectMeta(store.MetaKeyReindexRequired)
		return reindexFound && reindexVal == "true"
	}
	if h.workspace != nil {
		for _, p := range h.workspace.Projects {
			reindexVal, reindexFound, _ := p.Store.GetProjectMeta(store.MetaKeyReindexRequired)
			if reindexFound && reindexVal == "true" {
				return true
			}
		}
	}
	return false
}

func (h *handler) getMetadataFilteredNodes(st *store.Store, status, sensitivity, claimID, sourceType, confidence, analystStatus string, limit int) ([]store.Node, error) {
	useGovernance := status != "" || sensitivity != ""
	useResearch := claimID != "" || sourceType != "" || confidence != "" || analystStatus != ""

	var candidates []store.Node
	if useResearch {
		nodes, err := st.GetNodesByResearch(claimID, sourceType, confidence, analystStatus, limit)
		if err != nil {
			return nil, err
		}
		candidates = nodes
	} else if useGovernance {
		nodes, err := st.GetNodesByGovernance(status, sensitivity, limit)
		if err != nil {
			return nil, err
		}
		candidates = nodes
	}

	if useResearch && useGovernance {
		govNodes, err := st.GetNodesByGovernance(status, sensitivity, 0)
		if err != nil {
			return nil, err
		}
		govIDs := make(map[string]bool, len(govNodes))
		for _, n := range govNodes {
			govIDs[n.ID] = true
		}
		filtered := candidates[:0]
		for _, n := range candidates {
			if govIDs[n.ID] {
				filtered = append(filtered, n)
			}
		}
		candidates = filtered
	}

	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
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
		sb.WriteString("| Project | Files | Nodes | Edges | Unresolved | DB Size | Schema Version |\n")
		sb.WriteString("|---------|-------|-------|-------|------------|--------|----------------|\n")

		// Collect per-project schema info for warnings below.
		type projectWarning struct {
			name          string
			reindexReason string
			reindexScope  string
			lastFailure   string
		}
		var warnings []projectWarning

		for _, p := range h.workspace.Projects {
			s, ok := allStats[p.Name]
			if !ok {
				continue
			}
			schemaVer, schemaName, _ := p.Store.SchemaVersion()
			var schemaCol string
			if schemaVer == 0 {
				schemaCol = "none"
			} else {
				schemaCol = fmt.Sprintf("v%d (%s)", schemaVer, schemaName)
			}
			sb.WriteString(fmt.Sprintf("| %s | %d | %d | %d | %d | %s | %s |\n",
				p.Name, s.FileCount, s.NodeCount, s.EdgeCount, s.UnresolvedCount, formatSize(s.DBSizeBytes), schemaCol))

			// Collect warnings.
			reindexVal, reindexFound, _ := p.Store.GetProjectMeta(store.MetaKeyReindexRequired)
			lastFailure, failureFound, _ := p.Store.GetProjectMeta(store.MetaKeyMigrationLastFailure)
			if (reindexFound && reindexVal == "true") || (failureFound && lastFailure != "") {
				w := projectWarning{name: p.Name}
				if reindexFound && reindexVal == "true" {
					w.reindexReason, _, _ = p.Store.GetProjectMeta(store.MetaKeyReindexReason)
					w.reindexScope, _, _ = p.Store.GetProjectMeta(store.MetaKeyReindexScope)
				}
				if failureFound {
					w.lastFailure = lastFailure
				}
				warnings = append(warnings, w)
			}
		}

		// Append warnings if any projects need attention.
		if len(warnings) > 0 {
			sb.WriteString("\n### Warnings\n")
			for _, w := range warnings {
				sb.WriteString(fmt.Sprintf("\n**%s**\n", w.name))
				if w.reindexReason != "" || w.reindexScope != "" || w.lastFailure == "" {
					sb.WriteString("Reindex required: yes\n")
					if w.reindexReason != "" {
						sb.WriteString(fmt.Sprintf("  Reason: %s\n", w.reindexReason))
					}
					scope := w.reindexScope
					if scope == "" {
						scope = "unknown"
					}
					sb.WriteString(fmt.Sprintf("  Scope: %s\n", scope))
				}
				if w.lastFailure != "" {
					sb.WriteString(fmt.Sprintf("Last migration failure: %s\n", w.lastFailure))
				}
			}
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
		appendWorkspaceDomainPacks(&sb, h)
		appendWorkspaceMetadataQualityStats(&sb, h)
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

		// Schema / migration section.
		schemaVer, schemaName, _ := h.store.SchemaVersion()
		reindexVal, reindexFound, _ := h.store.GetProjectMeta(store.MetaKeyReindexRequired)
		lastFailure, failureFound, _ := h.store.GetProjectMeta(store.MetaKeyMigrationLastFailure)

		sb.WriteString("\n### Schema\n")
		if schemaVer == 0 {
			sb.WriteString("Schema version: none (pre-migration DB)\n")
		} else {
			sb.WriteString(fmt.Sprintf("Schema version: v%d (%s)\n", schemaVer, schemaName))
		}
		reindexRequired := reindexFound && reindexVal == "true"
		if reindexRequired {
			sb.WriteString("Reindex required: yes\n")
			if reason, _, _ := h.store.GetProjectMeta(store.MetaKeyReindexReason); reason != "" {
				sb.WriteString(fmt.Sprintf("  Reason: %s\n", reason))
			}
			scope, _, _ := h.store.GetProjectMeta(store.MetaKeyReindexScope)
			if scope == "" {
				scope = "unknown"
			}
			sb.WriteString(fmt.Sprintf("  Scope: %s\n", scope))
		} else {
			sb.WriteString("Reindex required: no\n")
		}
		if failureFound && lastFailure != "" {
			sb.WriteString(fmt.Sprintf("Last migration failure: %s\n", lastFailure))
		}

		// Metadata index stats.
		if metaStats, err := h.store.GetMetadataStats(); err == nil {
			sb.WriteString("\n### Metadata Index\n")
			sb.WriteString(fmt.Sprintf("Documents with metadata: %d / %d\n", metaStats.DocsWithMetadata, metaStats.TotalDocs))
			sb.WriteString(fmt.Sprintf("Documents with research metadata: %d / %d\n", metaStats.DocsWithResearch, metaStats.TotalDocs))
			if reindexRequired {
				scope, _, _ := h.store.GetProjectMeta(store.MetaKeyReindexScope)
				if scope == "metadata" {
					sb.WriteString("> metadata reindex pending — run `docgraph index --force`\n")
				}
			}
		}

		if qualityStats, err := h.store.GetMetadataQualityStats(time.Time{}); err == nil {
			appendMetadataQualityStats(&sb, qualityStats)
		}

		if packs, err := h.store.GetDomainPacks(); err == nil {
			if packStats, err := h.store.GetDomainPackStats(); err == nil {
				appendDomainPackStats(&sb, packs, packStats)
			}
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func appendMetadataQualityStats(sb *strings.Builder, stats store.MetadataQualityStats) {
	sb.WriteString("\n### Metadata Quality\n")
	sb.WriteString(fmt.Sprintf("Average score: %.1f / 100 across %d documents\n", stats.AverageScore, stats.TotalDocs))
	sb.WriteString(fmt.Sprintf("Good: %d | Warning: %d | Poor: %d\n", stats.GoodDocs, stats.WarningDocs, stats.PoorDocs))
	if len(stats.IssueCounts) == 0 {
		sb.WriteString("Issues: none\n")
		return
	}
	codes := sortedIssueCounts(stats.IssueCounts)
	sb.WriteString("| Issue | Documents |\n|-------|-----------|\n")
	limit := len(codes)
	if limit > 8 {
		limit = 8
	}
	for _, code := range codes[:limit] {
		sb.WriteString(fmt.Sprintf("| `%s` | %d |\n", code, stats.IssueCounts[code]))
	}
}

func appendWorkspaceMetadataQualityStats(sb *strings.Builder, h *handler) {
	if h == nil || h.workspace == nil {
		return
	}
	sb.WriteString("\n### Metadata Quality\n")
	sb.WriteString("| Project | Avg Score | Good | Warning | Poor | Top Issues |\n")
	sb.WriteString("|---------|-----------|------|---------|------|------------|\n")
	for _, project := range h.workspace.Projects {
		stats, err := project.Store.GetMetadataQualityStats(time.Time{})
		if err != nil {
			continue
		}
		topIssues := sortedIssueCounts(stats.IssueCounts)
		if len(topIssues) > 3 {
			topIssues = topIssues[:3]
		}
		for i, code := range topIssues {
			topIssues[i] = fmt.Sprintf("%s:%d", code, stats.IssueCounts[code])
		}
		issues := strings.Join(topIssues, ", ")
		if issues == "" {
			issues = "none"
		}
		sb.WriteString(fmt.Sprintf("| %s | %.1f | %d | %d | %d | %s |\n",
			project.Name, stats.AverageScore, stats.GoodDocs, stats.WarningDocs, stats.PoorDocs, issues))
	}
}

func formatQualityIssueCodes(issues []store.MetadataQualityIssue, limit int) string {
	if len(issues) == 0 {
		return " (issues: none)"
	}
	if limit <= 0 || limit > len(issues) {
		limit = len(issues)
	}
	codes := make([]string, 0, limit)
	for _, issue := range issues[:limit] {
		codes = append(codes, issue.Code)
	}
	suffix := ""
	if len(issues) > limit {
		suffix = fmt.Sprintf(", +%d", len(issues)-limit)
	}
	return fmt.Sprintf(" (issues: %s%s)", strings.Join(codes, ", "), suffix)
}

func sortedIssueCounts(counts map[string]int) []string {
	codes := make([]string, 0, len(counts))
	for code := range counts {
		codes = append(codes, code)
	}
	sort.Slice(codes, func(i, j int) bool {
		if counts[codes[i]] == counts[codes[j]] {
			return codes[i] < codes[j]
		}
		return counts[codes[i]] > counts[codes[j]]
	})
	return codes
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

func appendDomainPackStats(sb *strings.Builder, packs []domainpacks.Pack, stats store.DomainPackStats) {
	if len(packs) == 0 {
		return
	}
	sb.WriteString("\n### Domain Packs\n")
	sb.WriteString(fmt.Sprintf("Loaded packs: %d (%d enabled, %d fields)\n", stats.TotalPacks, stats.EnabledPacks, stats.TotalFields))
	sb.WriteString("| Pack | Domain | Version | Enabled | Fields |\n")
	sb.WriteString("|------|--------|---------|---------|--------|\n")
	for _, pack := range packs {
		enabled := "no"
		if pack.EnabledByDefault {
			enabled = "yes"
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %d |\n",
			pack.ID, pack.Domain, pack.Version, enabled, len(pack.Fields)))
	}
}

func appendWorkspaceDomainPacks(sb *strings.Builder, h *handler) {
	if h == nil || h.workspace == nil {
		return
	}
	type aggregate struct {
		pack            domainpacks.Pack
		projectCount    int
		enabledProjects int
	}
	byID := make(map[string]*aggregate)
	for _, project := range h.workspace.Projects {
		packs, err := project.Store.GetDomainPacks()
		if err != nil {
			continue
		}
		for _, pack := range packs {
			agg, ok := byID[pack.ID]
			if !ok {
				cp := pack
				agg = &aggregate{pack: cp}
				byID[pack.ID] = agg
			}
			agg.projectCount++
			if pack.EnabledByDefault {
				agg.enabledProjects++
			}
		}
	}
	if len(byID) == 0 {
		return
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	sb.WriteString("\n### Domain Packs\n")
	sb.WriteString("| Pack | Domain | Version | Projects | Enabled Projects | Fields |\n")
	sb.WriteString("|------|--------|---------|----------|------------------|--------|\n")
	for _, id := range ids {
		agg := byID[id]
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %d | %d | %d |\n",
			agg.pack.ID, agg.pack.Domain, agg.pack.Version,
			agg.projectCount, agg.enabledProjects, len(agg.pack.Fields)))
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

package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Detective-XH/docgraph/internal/domainpacks"
	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

var statusTool = mcp.NewTool("docgraph_status",
	mcp.WithDescription("Index health: file count, node count, edge count, unresolved references, DB size."),
)

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

		for _, p := range h.workspace.Projects {
			s, ok := allStats[p.Name]
			if !ok {
				continue
			}
			sb.WriteString(fmt.Sprintf("| %s | %d | %d | %d | %d | %s |\n",
				p.Name, s.FileCount, s.NodeCount, s.EdgeCount, s.UnresolvedCount, formatSize(s.DBSizeBytes)))
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

		// Metadata index stats.
		if metaStats, err := h.store.GetMetadataStats(); err == nil {
			sb.WriteString("\n### Metadata Index\n")
			sb.WriteString(fmt.Sprintf("Documents with metadata: %d / %d\n", metaStats.DocsWithMetadata, metaStats.TotalDocs))
			sb.WriteString(fmt.Sprintf("Documents with research metadata: %d / %d\n", metaStats.DocsWithResearch, metaStats.TotalDocs))
		}

		if qualityStats, err := h.store.GetMetadataQualityStats(time.Time{}); err == nil {
			appendMetadataQualityStats(&sb, qualityStats)
		}

		if packs, err := h.store.GetDomainPacks(); err == nil {
			if packStats, err := h.store.GetDomainPackStats(); err == nil {
				appendDomainPackStats(&sb, packs, packStats)
			}
		}

		if entities, mentions, err := h.store.GetEntityStats(); err == nil && (entities > 0 || mentions > 0) {
			sb.WriteString("\n### Entity Graph\n")
			sb.WriteString(fmt.Sprintf("Entities: %d | Mentions: %d\n", entities, mentions))
		}

		appendDriftAuditStats(&sb, h.store)
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

// appendDriftAuditStats adds a compact policy drift audit summary to the status
// output. It omits the section entirely when there are no findings, so a clean
// project has no extra noise in docgraph_status output.
func appendDriftAuditStats(sb *strings.Builder, st *store.Store) {
	if st == nil {
		return
	}
	findings, err := st.GetDriftFindings(store.DriftAuditOpts{})
	if err != nil || len(findings) == 0 {
		return
	}
	summary := store.SummarizeDriftFindings(findings)
	sb.WriteString("\n### Drift Audit\n")
	sb.WriteString(fmt.Sprintf("Total findings: %d", summary.TotalFindings))
	if e := summary.BySeverity["error"]; e > 0 {
		sb.WriteString(fmt.Sprintf(" | Errors: %d", e))
	}
	if w := summary.BySeverity["warning"]; w > 0 {
		sb.WriteString(fmt.Sprintf(" | Warnings: %d", w))
	}
	sb.WriteString("\n")
	for code, count := range summary.ByCode {
		sb.WriteString(fmt.Sprintf("  %s: %d\n", code, count))
	}
	sb.WriteString("Use `docgraph_context format=drift_audit` for full report.\n")
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
	sb.WriteString("| Pack | Domain | Version | Enabled | Fields | Description |\n")
	sb.WriteString("|------|--------|---------|---------|--------|-------------|\n")
	for _, pack := range packs {
		enabled := "no"
		if pack.EnabledByDefault {
			enabled = "yes"
		}
		desc := truncateRunes(pack.Description, 60)
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %d | %s |\n",
			pack.ID, pack.Domain, pack.Version, enabled, len(pack.Fields), desc))
	}
	// Capability notes for disabled built-in packs.
	for _, pack := range packs {
		if pack.EnabledByDefault || !pack.BuiltIn {
			continue
		}
		note := packCapabilityNote(pack)
		sb.WriteString(note)
		sb.WriteByte('\n')
	}
}

// packCapabilityNote returns a blockquote capability hint for a disabled built-in pack.
// Total line length is kept under 120 chars.
func packCapabilityNote(pack domainpacks.Pack) string {
	prefix := fmt.Sprintf("> **`%s`** (opt-in): ", pack.ID)
	suffix := ""
	if pack.ID == domainpacks.PackPolicyProcess {
		suffix = ". Use `docgraph_context format=drift_audit` to run the audit."
	}
	if pack.ID == domainpacks.PackAssessmentDrift {
		suffix = ". Use `docgraph_context format=drift_audit`."
	}
	if pack.ID == "code_doc" {
		suffix = ". When enabled, `format=drift_audit` also reports code.* findings."
	}
	// Budget: 120 chars - len(prefix) - len(suffix), minus 3 for "..."
	budget := 120 - utf8.RuneCountInString(prefix) - utf8.RuneCountInString(suffix)
	desc := pack.Description
	if utf8.RuneCountInString(desc) > budget {
		runes := []rune(desc)
		desc = string(runes[:budget-3]) + "..."
	}
	return prefix + desc + suffix
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
	sb.WriteString("| Pack | Domain | Version | Projects | Enabled Projects | Fields | Description |\n")
	sb.WriteString("|------|--------|---------|----------|------------------|--------|-------------|\n")
	for _, id := range ids {
		agg := byID[id]
		desc := truncateRunes(agg.pack.Description, 60)
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %d | %d | %d | %s |\n",
			agg.pack.ID, agg.pack.Domain, agg.pack.Version,
			agg.projectCount, agg.enabledProjects, len(agg.pack.Fields), desc))
	}
	// Capability notes for disabled built-in packs (zero enabled projects).
	for _, id := range ids {
		agg := byID[id]
		if agg.enabledProjects > 0 || !agg.pack.BuiltIn {
			continue
		}
		note := packCapabilityNote(agg.pack)
		sb.WriteString(note)
		sb.WriteByte('\n')
	}
}

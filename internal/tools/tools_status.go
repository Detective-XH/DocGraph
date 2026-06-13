package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Detective-XH/docgraph/internal/domainpacks"
	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

var statusTool = mcp.NewTool("docgraph_status",
	mcp.WithDescription("Index health: file count, node count, edge count, unresolved references, DB size. Use to verify the index is ready before other operations, or to inspect embedding model state, LLM callout tool state (docgraph_embeddings/docgraph_enrichment enabled/disabled + required flags), domain packs, and drift findings. Metadata quality scores (0–100) reflect frontmatter completeness; deductions for missing status, owner, or review_due are the most common and do not affect content reliability."),
)

func (h *handler) handleStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var sb strings.Builder

	if h.indexing.Load() {
		sb.WriteString("Indexing: in progress\n\n")
	}

	if h.workspace != nil {
		if errResult := appendWorkspaceStatus(&sb, h); errResult != nil {
			return errResult, nil
		}
	} else {
		if errResult := appendSingleStoreStatus(&sb, h); errResult != nil {
			return errResult, nil
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// appendWorkspaceStatus renders the workspace-mode section of handleStatus output
// into sb. Returns a non-nil *mcp.CallToolResult only on a fatal stats error.
func appendWorkspaceStatus(sb *strings.Builder, h *handler) *mcp.CallToolResult {
	allStats, err := h.workspace.GetAllStats()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("get stats failed: %v", err))
	}
	sb.WriteString("## DocGraph Workspace Status\n\n")
	sb.WriteString("| Project | Files | Nodes | Edges | Unresolved | DB Size |\n")
	sb.WriteString("|---------|-------|-------|-------|------------|--------|\n")

	for _, p := range h.workspace.Projects {
		s, ok := allStats[p.Name]
		if !ok {
			continue
		}
		fmt.Fprintf(sb, "| %s | %d | %d | %d | %d | %s |\n",
			p.Name, s.FileCount, s.NodeCount, s.EdgeCount, s.UnresolvedCount, formatSize(s.DBSizeBytes))
	}

	// Projects table — compact name→file-count index so the LLM can discover
	// project names to pass as project=<name> to any query tool.
	sb.WriteString("\n## Projects\n\n")
	sb.WriteString("| Name | Files |\n")
	sb.WriteString("|------|-------|\n")
	for _, p := range h.workspace.Projects {
		s, ok := allStats[p.Name]
		if !ok {
			continue
		}
		fmt.Fprintf(sb, "| %s | %d |\n", p.Name, s.FileCount)
	}

	appendWorkspaceEmbeddingStats(sb, h)
	appendWorkspaceEnrichmentStats(sb, h)
	appendWorkspaceDomainPacks(sb, h)
	appendWorkspaceMetadataQualityStats(sb, h)
	if es, err := h.workspace.GetEntityStats(); err == nil && (es.TotalEntities > 0 || es.TotalMentions > 0) {
		appendEntityGraph(sb, es.TotalEntities, es.TotalMentions)
	}
	appendWorkspaceDriftAudit(sb, h)
	appendIgnoreConfig(sb, h)
	appendLLMCalloutState(sb, h)
	return nil
}

// appendSingleStoreStatus renders the single-store section of handleStatus output
// into sb. Returns a non-nil *mcp.CallToolResult only on a fatal stats error.
func appendSingleStoreStatus(sb *strings.Builder, h *handler) *mcp.CallToolResult {
	stats, err := h.store.GetStats()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("get stats failed: %v", err))
	}
	fmt.Fprintf(sb, "Files: %d | Nodes: %d | Edges: %d | Unresolved: %d | DB: %s\n\n",
		stats.FileCount, stats.NodeCount, stats.EdgeCount, stats.UnresolvedCount, formatSize(stats.DBSizeBytes))
	if len(stats.NodesByKind) > 2 {
		sb.WriteString("### Nodes by Kind\n| Kind | Count |\n|------|-------|\n")
		for kind, count := range stats.NodesByKind {
			fmt.Fprintf(sb, "| %s | %d |\n", kind, count)
		}
	}
	if len(stats.EdgesByKind) > 2 {
		sb.WriteString("\n### Edges by Kind\n| Kind | Count |\n|------|-------|\n")
		for kind, count := range stats.EdgesByKind {
			fmt.Fprintf(sb, "| %s | %d |\n", kind, count)
		}
	}

	embStats, err := h.store.GetEmbeddingModelStats()
	if err == nil {
		appendEmbeddingStats(sb, embStats)
	}

	// Metadata index stats.
	if metaStats, err := h.store.GetMetadataStats(); err == nil {
		sb.WriteString("\n### Metadata Index\n")
		fmt.Fprintf(sb, "Documents with metadata: %d / %d\n", metaStats.DocsWithMetadata, metaStats.TotalDocs)
		fmt.Fprintf(sb, "Documents with research metadata: %d / %d\n", metaStats.DocsWithResearch, metaStats.TotalDocs)
	}
	if enrichmentStats, err := h.store.GetEnrichmentStats(); err == nil && enrichmentStats.EligibleDocs > 0 {
		appendEnrichmentStats(sb, enrichmentStats)
	}

	if qualityStats, err := h.store.GetMetadataQualityStats(time.Time{}); err == nil {
		appendMetadataQualityStats(sb, qualityStats)
	}

	if packs, err := h.store.GetDomainPacks(); err == nil {
		if packStats, err := h.store.GetDomainPackStats(); err == nil {
			appendDomainPackStats(sb, packs, packStats)
		}
	}

	if entities, mentions, err := h.store.Entity.GetEntityStats(); err == nil && (entities > 0 || mentions > 0) {
		appendEntityGraph(sb, entities, mentions)
	}

	appendDriftAuditStats(sb, h.store)
	appendIgnoreConfig(sb, h)
	appendLLMCalloutState(sb, h)
	return nil
}

// appendIgnoreConfig reports which ignore sources are active and how to exclude
// files from the index — the only place the MCP surface surfaces the
// .docgraphignore mechanism, so an agent can discover and complete an exclusion
// without reading the README or grepping the serve invocation.
func appendIgnoreConfig(sb *strings.Builder, h *handler) {
	sb.WriteString("\n### Index Configuration\n")
	if h.noGitignore {
		sb.WriteString("- `.gitignore`: not applied (`--no-gitignore`)\n")
	} else {
		sb.WriteString("- `.gitignore`: applied\n")
	}
	fmt.Fprintf(sb, "- `.docgraphignore`: always applied (even under `--no-gitignore`)%s\n", docgraphIgnorePresence(h))
	sb.WriteString("- Exclude files by adding glob patterns to `.docgraphignore` at the project root (e.g. `*.pdf`). A running server applies the change on save (the affected nodes are pruned); otherwise rebuild with `docgraph index --force <path>`.\n")
}

// docgraphIgnorePresence reports whether a .docgraphignore file is present, so the
// status output distinguishes "no exclusions configured" from "exclusions active".
func docgraphIgnorePresence(h *handler) string {
	exists := func(root string) bool {
		if root == "" {
			return false
		}
		_, err := os.Stat(filepath.Join(root, ".docgraphignore"))
		return err == nil
	}
	if h.workspace != nil {
		present := 0
		for _, p := range h.workspace.Projects {
			if exists(p.Path) {
				present++
			}
		}
		return fmt.Sprintf(" (present in %d/%d projects)", present, len(h.workspace.Projects))
	}
	if exists(h.projectRoot) {
		return " (present at project root)"
	}
	return " (none at project root)"
}

// appendEntityGraph writes the compact Entity Graph section.
// It is shared by the workspace fan-out branch and the single-store branch.
func appendEntityGraph(sb *strings.Builder, entities, mentions int) {
	sb.WriteString("\n### Entity Graph\n")
	fmt.Fprintf(sb, "Entities: %d | Mentions: %d\n", entities, mentions)
}

// appendWorkspaceEmbeddingStats fans out embedding stats across every project,
// aggregates by model ID, and delegates to appendEmbeddingStats for rendering.
// Extracted from the workspace branch of handleStatus to reduce its complexity.
func appendWorkspaceEmbeddingStats(sb *strings.Builder, h *handler) {
	if h == nil || h.workspace == nil {
		return
	}
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
	var allEmbStats []store.EmbeddingModelStat
	for _, s := range modelTotals {
		allEmbStats = append(allEmbStats, *s)
	}
	appendEmbeddingStats(sb, allEmbStats)
}

func appendEnrichmentStats(sb *strings.Builder, stats store.EnrichmentStats) {
	sb.WriteString("\n### Agent Metadata Enrichment\n")
	fmt.Fprintf(sb, "Eligible frontmatter-less documents: %d\n", stats.EligibleDocs)
	fmt.Fprintf(sb, "Enriched summaries: %d", stats.EnrichedDocs)
	if stats.StaleDocs > 0 {
		fmt.Fprintf(sb, " | Stale: %d", stats.StaleDocs)
	}
	sb.WriteString("\n")
	sb.WriteString("Use `docgraph_enrichment action=pending` for the pull-then-push enrichment workflow.\n")
}

func appendMetadataQualityStats(sb *strings.Builder, stats store.MetadataQualityStats) {
	sb.WriteString("\n### Metadata Quality\n")
	fmt.Fprintf(sb, "Average score: %.1f / 100 across %d documents\n", stats.AverageScore, stats.TotalDocs)
	fmt.Fprintf(sb, "Good: %d | Warning: %d | Poor: %d\n", stats.GoodDocs, stats.WarningDocs, stats.PoorDocs)
	if len(stats.IssueCounts) == 0 {
		sb.WriteString("Issues: none\n")
		return
	}
	codes := sortedIssueCounts(stats.IssueCounts)
	sb.WriteString("| Issue | Documents |\n|-------|-----------|\n")
	limit := min(len(codes), 8)
	for _, code := range codes[:limit] {
		fmt.Fprintf(sb, "| `%s` | %d |\n", code, stats.IssueCounts[code])
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
		fmt.Fprintf(sb, "| %s | %.1f | %d | %d | %d | %s |\n",
			project.Name, stats.AverageScore, stats.GoodDocs, stats.WarningDocs, stats.PoorDocs, issues)
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
func appendDriftAuditStats(sb *strings.Builder, auditor DriftAuditor) {
	if auditor == nil {
		return
	}
	if summary, ok := driftSummaryFor(auditor); ok {
		appendDriftSummary(sb, summary)
	}
}

// appendWorkspaceDriftAudit fans out the drift audit across every project and
// renders a single combined summary, mirroring the single-store branch.
func appendWorkspaceDriftAudit(sb *strings.Builder, h *handler) {
	if h == nil || h.workspace == nil {
		return
	}
	agg := store.DriftAuditStats{BySeverity: map[string]int{}, ByCode: map[string]int{}}
	for _, project := range h.workspace.Projects {
		summary, ok := driftSummaryFor(project.Store)
		if !ok {
			continue
		}
		agg.TotalFindings += summary.TotalFindings
		for code, count := range summary.BySeverity {
			agg.BySeverity[code] += count
		}
		for code, count := range summary.ByCode {
			agg.ByCode[code] += count
		}
	}
	appendDriftSummary(sb, agg)
}

// appendDriftSummary renders a drift summary, omitting the section entirely when
// there are no findings so a clean project adds no noise to docgraph_status.
func appendDriftSummary(sb *strings.Builder, summary store.DriftAuditStats) {
	if summary.TotalFindings == 0 {
		return
	}
	sb.WriteString("\n### Drift Audit\n")
	fmt.Fprintf(sb, "Total findings: %d", summary.TotalFindings)
	if e := summary.BySeverity["error"]; e > 0 {
		fmt.Fprintf(sb, " | Errors: %d", e)
	}
	if w := summary.BySeverity["warning"]; w > 0 {
		fmt.Fprintf(sb, " | Warnings: %d", w)
	}
	sb.WriteString("\n")
	for code, count := range summary.ByCode {
		fmt.Fprintf(sb, "  %s: %d\n", code, count)
	}
	sb.WriteString("Use `docgraph_context format=drift_audit` for full report.\n")
}

// appendWorkspaceEnrichmentStats fans out enrichment stats across every project
// and renders the aggregate, mirroring the single-store branch.
func appendWorkspaceEnrichmentStats(sb *strings.Builder, h *handler) {
	if h == nil || h.workspace == nil {
		return
	}
	var agg store.EnrichmentStats
	for _, project := range h.workspace.Projects {
		stats, err := project.Store.GetEnrichmentStats()
		if err != nil {
			continue
		}
		agg.EligibleDocs += stats.EligibleDocs
		agg.EnrichedDocs += stats.EnrichedDocs
		agg.StaleDocs += stats.StaleDocs
	}
	if agg.EligibleDocs == 0 {
		return
	}
	appendEnrichmentStats(sb, agg)
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
		fmt.Fprintf(sb, "| %s | %d | %d |\n", s.ModelID, s.Total, s.Stale)
	}
}

func appendDomainPackStats(sb *strings.Builder, packs []domainpacks.Pack, stats store.DomainPackStats) {
	if len(packs) == 0 {
		return
	}
	sb.WriteString("\n### Domain Packs\n")
	fmt.Fprintf(sb, "Loaded packs: %d (%d enabled, %d fields)\n", stats.TotalPacks, stats.EnabledPacks, stats.TotalFields)
	sb.WriteString("| Pack | Domain | Version | Enabled | Fields | Description |\n")
	sb.WriteString("|------|--------|---------|---------|--------|-------------|\n")
	for _, pack := range packs {
		enabled := "no"
		if pack.Enabled {
			enabled = "yes"
		}
		desc := truncateRunes(pack.Description, 60)
		fmt.Fprintf(sb, "| %s | %s | %s | %s | %d | %s |\n",
			pack.ID, pack.Domain, pack.Version, enabled, len(pack.Fields), desc)
	}
	// Capability notes for disabled built-in packs.
	for _, pack := range packs {
		if pack.Enabled || !pack.BuiltIn {
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
			if pack.Enabled {
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
		fmt.Fprintf(sb, "| %s | %s | %s | %d | %d | %d | %s |\n",
			agg.pack.ID, agg.pack.Domain, agg.pack.Version,
			agg.projectCount, agg.enabledProjects, len(agg.pack.Fields), desc)
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

// appendLLMCalloutState appends the LLM Callout Tools section to the status
// output. When both tools are disabled (default), it shows a short opt-in
// notice. When either is enabled, it shows real stats.
func appendLLMCalloutState(sb *strings.Builder, h *handler) {
	embEnabled := h.enableEmbeddings
	enrichEnabled := h.enableEnrichment

	// Decide section header: include "(opt-in)" only when both are disabled.
	if !embEnabled && !enrichEnabled {
		sb.WriteString("\n### LLM Callout Tools (opt-in)\n")
		sb.WriteString("  docgraph_embeddings : disabled  (--enable-embeddings to activate)\n")
		sb.WriteString("  docgraph_enrichment : disabled  (--enable-enrichment to activate)\n")
		sb.WriteString("  Note: these tools send document content to an external LLM provider.\n")
		return
	}

	sb.WriteString("\n### LLM Callout Tools\n")

	// Embeddings line.
	if embEnabled {
		var totalVectors int
		if h.workspace != nil {
			for _, p := range h.workspace.Projects {
				if stats, err := p.Store.GetEmbeddingModelStats(); err == nil {
					for _, s := range stats {
						totalVectors += s.Total
					}
				}
			}
		} else if h.store != nil {
			if stats, err := h.store.GetEmbeddingModelStats(); err == nil {
				for _, s := range stats {
					totalVectors += s.Total
				}
			}
		}
		fmt.Fprintf(sb, "  docgraph_embeddings : enabled   — %d vectors stored\n", totalVectors)
	} else {
		sb.WriteString("  docgraph_embeddings : disabled  (--enable-embeddings to activate)\n")
	}

	// Enrichment line.
	if enrichEnabled {
		var enriched, pending int
		if h.workspace != nil {
			for _, p := range h.workspace.Projects {
				if stats, err := p.Store.GetEnrichmentStats(); err == nil {
					enriched += stats.EnrichedDocs
					pending += stats.EligibleDocs - stats.EnrichedDocs
					if pending < 0 {
						pending = 0
					}
				}
			}
		} else if h.store != nil {
			if stats, err := h.store.GetEnrichmentStats(); err == nil {
				enriched = stats.EnrichedDocs
				pending = stats.EligibleDocs - stats.EnrichedDocs
				pending = max(pending, 0)
			}
		}
		fmt.Fprintf(sb, "  docgraph_enrichment : enabled   — %d enriched, %d pending\n", enriched, pending)
	} else {
		sb.WriteString("  docgraph_enrichment : disabled  (--enable-enrichment to activate)\n")
	}
}

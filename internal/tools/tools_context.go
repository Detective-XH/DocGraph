package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

var contextTool = mcp.NewTool("docgraph_context",
	mcp.WithDescription("PRIMARY TOOL. Build relevant documentation context for a task or topic. Composes governance-aware search + node details + cross-references + bounded source content in one call. For a single known document, use docgraph_node instead. For broad queries, set includeContent=false or reduce maxContentBytes (default 2000, hard cap 6000) to avoid large responses; a 10-node query with default settings can produce 20–50 KB of output."),
	mcp.WithString("task", mcp.Required(), mcp.Description("Description of the task/topic to find context for")),
	mcp.WithString("format", mcp.Description("Output format: summary (default), context_pack for a reviewable evidence pack, or drift_audit for a drift audit report (finding codes: policy.stale_review, policy.superseded_referenced, policy.duplicate, policy.non_canonical, policy.conflicting, research.stale_assessment, research.unverified_evidence, research.competing_interpretations, research.superseded_claim, research.impacted_deliverable; doc.stale_by_git when git history is present; when code_doc is enabled: code.missing_symbol, code.undocumented_export, code.unanchored_feature).")),
	mcp.WithNumber("maxNodes", mcp.Description("Max documents to return (default 10)")),
	mcp.WithBoolean("includeContent", mcp.Description("Include bounded source content for each result (default true)")),
	mcp.WithNumber("maxContentBytes", mcp.Description("Max source bytes per result (default 2000, hard cap 6000)")),
	mcp.WithNumber("impactDepth", mcp.Description("Context pack impact depth for incoming references (default 1, max 3).")),
	mcp.WithNumber("referenceLimit", mcp.Description("Context pack max incoming/outgoing references per item (default 5, max 20).")),
	mcp.WithString("status", mcp.Description("Filter by governance status.")),
	mcp.WithString("sensitivity", mcp.Description("Filter by sensitivity.")),
	mcp.WithString("canonical_source", mcp.Description("Filter by canonical source marker or value.")),
	mcp.WithString("allowed_audience", mcp.Description("Filter to documents available to an audience label. Public documents are included.")),
	mcp.WithString("as_of_date", mcp.Description("Evaluate effective_date and valid_until against YYYY-MM-DD.")),
	mcp.WithString("claim_id", mcp.Description("Filter by research claim_id.")),
	mcp.WithString("source_type", mcp.Description("Filter by research source_type.")),
	mcp.WithString("confidence", mcp.Description("Filter by research confidence.")),
	mcp.WithString("analyst_status", mcp.Description("Filter by research analyst_status.")),
	mcp.WithString("project", mcp.Description("Workspace mode only: scope results to a single project by name (the directory name shown in docgraph_status). Omit to query all projects. No-op in single-store mode.")),
)

func formatHeadingOutline(headings []store.Node) string {
	var sb strings.Builder
	for _, h := range headings {
		indent := strings.Repeat("  ", h.Level-1)
		fmt.Fprintf(&sb, "%s- H%d: %s\n", indent, h.Level, h.Name)
	}
	return sb.String()
}

func headingNames(headings []store.Node) string {
	names := make([]string, len(headings))
	for i, h := range headings {
		names[i] = h.Name
	}
	return strings.Join(names, ", ")
}

// maxContextResponseBytes is the soft output-size cap for the summary format.
// When the rendered payload exceeds this limit mid-loop, remaining documents are
// skipped and a truncation notice is emitted so the agent can refine the query.
//
// Calibrated against workspace mode (23 projects): focused queries (maxNodes≤5)
// measure ~8–14 KB; broad cross-workspace queries (maxNodes=10) measure ~24–27 KB.
// 20 KB sits strictly between those bands — below broad queries, above focused ones.
const maxContextResponseBytes = 20 * 1024

// maxTailStubBytes bounds the overflow stub-tail — the path+refs list emitted
// after maxContextResponseBytes is hit. It caps the stub LIST only, NOT the whole
// response: a single huge-heading document can already overshoot the head budget
// on its own (the separate structure-outline follow-on). The reported overflow
// was 11 docs and the default maxNodes is 10, so the tail is normally listed in
// full; even 2 KB covers every realistic overflow. 4 KB (~30–50 stubs) only
// truncates pathological large-maxNodes (≤200) cross-workspace sweeps — and the
// un-listed remainder is then disclosed as a trailing count, never silently
// dropped. Capping by bytes (not stub count) also bounds the getEdgeCounts calls
// to the listed stubs, so a 200-doc tail cannot fan out 200 edge lookups.
const maxTailStubBytes = 4 * 1024

// ctxEntry is a single deduped context result (one per source file).
type ctxEntry struct {
	sr       store.SearchResult
	sections []string
}

// contextRenderOpts holds the render-time options extracted from the request.
type contextRenderOpts struct {
	includeContent  bool
	maxContentBytes int
}

// parseContextArgs validates and parses the handleContext request into a
// SearchOptions plus render opts. Returns a non-nil tool-error result on failure.
func parseContextArgs(args map[string]any) (task string, opts store.SearchOptions, render contextRenderOpts, toolErr *mcp.CallToolResult) {
	task = getStringArg(args, "task", "")
	if task == "" {
		return "", opts, render, mcp.NewToolResultError("task parameter is required")
	}
	task = sanitizeArg(task, maxArgLength)
	maxNodes := getIntArgClamped(args, "maxNodes", 10, 1, 200)
	render.includeContent = getBoolArg(args, "includeContent", true)
	render.maxContentBytes = getIntArg(args, "maxContentBytes", 2000)
	if render.maxContentBytes <= 0 {
		render.maxContentBytes = 2000
	}
	if render.maxContentBytes > 6000 {
		render.maxContentBytes = 6000
	}
	opts = store.SearchOptions{
		Query: task,
		Limit: maxNodes,
		Governance: store.GovernanceSearchOptions{
			Status:          sanitizeArg(getStringArg(args, "status", ""), 100),
			Sensitivity:     sanitizeArg(getStringArg(args, "sensitivity", ""), 100),
			CanonicalSource: sanitizeArg(getStringArg(args, "canonical_source", ""), 300),
			AllowedAudience: sanitizeArg(getStringArg(args, "allowed_audience", ""), 100),
			AsOfDate:        sanitizeArg(getStringArg(args, "as_of_date", ""), 20),
		},
		Research: store.ResearchSearchOptions{
			ClaimID:       sanitizeArg(getStringArg(args, "claim_id", ""), 100),
			SourceType:    sanitizeArg(getStringArg(args, "source_type", ""), 100),
			Confidence:    sanitizeArg(getStringArg(args, "confidence", ""), 100),
			AnalystStatus: sanitizeArg(getStringArg(args, "analyst_status", ""), 100),
		},
		ProjectFilter: sanitizeArg(getStringArg(args, "project", ""), maxArgLength),
	}
	return task, opts, render, nil
}

// dedupeContextResultsByFile collapses the ranked result set to one entry per
// source file (P-v3-1: prevents double-counting of heading hits from the same doc).
// Render-only: store ranking and order are untouched.
func dedupeContextResultsByFile(results []store.SearchResult) []ctxEntry {
	var deduped []ctxEntry
	seenFile := map[string]int{}
	for _, sr := range results {
		fp := sr.Node.FilePath
		section := ""
		if sr.Node.Kind == "heading" && sr.Node.Name != "" {
			section = sr.Node.Name
		}
		if idx, ok := seenFile[fp]; ok {
			if section != "" {
				deduped[idx].sections = append(deduped[idx].sections, section)
			}
			continue
		}
		seenFile[fp] = len(deduped)
		entry := ctxEntry{sr: sr}
		if section != "" {
			entry.sections = append(entry.sections, section)
		}
		deduped = append(deduped, entry)
	}
	return deduped
}

// appendContextEntryMetadata appends AI summary + governance + research + quality
// + entity sections when metadata is available for the node.
func (h *handler) appendContextEntryMetadata(sb *strings.Builder, node *store.Node) {
	st := h.getStoreForResolvedNode(node)
	if st == nil {
		return
	}
	docID := contextPackDocID(*node)
	if summary, err := st.GetAISummary(docID); err == nil && summary != nil {
		sb.WriteString(appendAISummarySection(summary))
	}
	if gov, err := st.GetGovernanceMetadata(docID); err == nil && !store.IsGovernanceEmpty(gov) {
		sb.WriteString(appendGovernanceSection(gov))
	}
	if research, err := st.GetResearchMetadata(docID); err == nil && !store.IsResearchEmpty(research) {
		sb.WriteString(appendResearchSection(research))
	}
	if quality, err := st.GetMetadataQuality(docID, time.Time{}); err == nil && quality != nil {
		sb.WriteString(appendMetadataQualitySection(quality))
	}
	if mentions, err := st.Entity.GetEntityMentions(node.ID); err == nil {
		sb.WriteString(appendEntitySection(st.Entity, mentions))
	}
}

// appendContextEntry renders one deduped entry (heading + excerpt + content +
// governance metadata) into sb.
func (h *handler) appendContextEntry(sb *strings.Builder, i int, entry ctxEntry, render contextRenderOpts) {
	sr := entry.sr
	node := sr.Node
	headings := h.getHeadings(&node)
	inCount, outCount := h.getEdgeCounts(&node)

	fmt.Fprintf(sb, "\n### %d. %s\n", i+1, node.Name)
	fmt.Fprintf(sb, "**Path:** %s | **Headings:** %d | **Refs in:** %d | **Refs out:** %d\n",
		node.FilePath, len(headings), inCount, outCount)
	if len(entry.sections) > 0 {
		fmt.Fprintf(sb, "Also matched %d section(s) in this same document: %s\n",
			len(entry.sections), strings.Join(entry.sections, "; "))
	}

	if len(headings) > 0 {
		sb.WriteString("\n#### Structure\n")
		sb.WriteString(formatHeadingOutline(headings))
	}

	if node.BodyExcerpt != "" {
		sb.WriteString("\n")
		for line := range strings.SplitSeq(strings.TrimRight(node.BodyExcerpt, "\n"), "\n") {
			fmt.Fprintf(sb, "> %s\n", line)
		}
	}

	if render.includeContent {
		appendBoundedContent(sb, h, &node, render.maxContentBytes)
	}

	// Governance metadata — appended when available.
	h.appendContextEntryMetadata(sb, &node)
}

// appendContextTailStubs writes the overflow stub list for entries past the budget cap.
// The budget-check condition and break MUST stay in the caller's loop.
func (h *handler) appendContextTailStubs(sb *strings.Builder, shown int, total int, tail []ctxEntry) {
	fmt.Fprintf(sb, "\n---\n> ⚠ Response budget reached — showing full content for %d of %d documents; the remaining %d follow as stubs (path + refs only, capped). Set includeContent=false, reduce maxNodes, or add project=<name> for their content.\n",
		shown, total, len(tail))
	tailStart := sb.Len()
	listed := 0
	for _, rest := range tail {
		rn := rest.sr.Node
		inC, outC := h.getEdgeCounts(&rn)
		// Build the stub, then enforce the cap BEFORE appending it — so a single
		// untrusted FilePath/Name long enough to exceed the whole tail budget on
		// its own cannot overshoot. Such a stub is counted (below), never written.
		stub := fmt.Sprintf("> - %s — %s (%d in / %d out)\n", rn.FilePath, rn.Name, inC, outC)
		if sb.Len()-tailStart+len(stub) > maxTailStubBytes {
			break
		}
		sb.WriteString(stub)
		listed++
	}
	if remaining := len(tail) - listed; remaining > 0 {
		fmt.Fprintf(sb, "> …and %d more not listed — narrow with project=<name> or a smaller maxNodes.\n", remaining)
	}
}

func (h *handler) handleContext(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	task, opts, render, toolErr := parseContextArgs(args)
	if toolErr != nil {
		return toolErr, nil
	}

	var results []store.SearchResult
	var err error
	if h.workspace != nil {
		results, err = h.workspace.SearchWithOptions(opts)
	} else {
		results, err = h.store.Searcher.SearchWithOptions(opts)
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	format := strings.ToLower(strings.TrimSpace(getStringArg(args, "format", "")))
	if format == "context_pack" || format == "evidence_pack" {
		impactDepth := getIntArgClamped(args, "impactDepth", 1, 1, 3)
		referenceLimit := getIntArgClamped(args, "referenceLimit", 5, 1, 20)
		return mcp.NewToolResultText(h.renderContextPack(task, results, contextPackOptions{
			IncludeContent:  render.includeContent,
			MaxContentBytes: render.maxContentBytes,
			ImpactDepth:     impactDepth,
			ReferenceLimit:  referenceLimit,
		})), nil
	}
	if format == "drift_audit" {
		return mcp.NewToolResultText(h.renderDriftAudit(task, opts.ProjectFilter)), nil
	}

	// P-v3-1: Collapse to one entry per source file (see dedupeContextResultsByFile).
	deduped := dedupeContextResultsByFile(results)

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Context for %q\n\nFound %d relevant documents.\n", task, len(deduped))

	for i, entry := range deduped {
		h.appendContextEntry(&sb, i, entry, render)

		// Check payload budget after rendering each document. P-v6-2 originally
		// dropped the remaining documents entirely (count-only notice), erasing
		// their paths and forfeiting recall on the first call. Degrade the tail
		// instead: list the remaining documents as stubs (path + refs only) so the
		// set stays visible. The stub list is byte-capped (maxTailStubBytes) so a
		// large maxNodes cannot ~double the payload; stubs past the cap are disclosed
		// as a trailing count, never silently dropped.
		if sb.Len() >= maxContextResponseBytes && i < len(deduped)-1 {
			h.appendContextTailStubs(&sb, i+1, len(deduped), deduped[i+1:])
			break
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func appendBoundedContent(sb *strings.Builder, h *handler, node *store.Node, maxBytes int) {
	// Try indexed section chunk first (avoids live file I/O, TOCTOU-safe).
	if st := h.getStoreForResolvedNode(node); st != nil {
		if chunk, ok, err := st.GetSectionChunk(node.ID); err == nil && ok {
			text := strings.TrimRight(chunk.Text, "\n")
			if text == "" {
				return
			}
			// Enforce the caller's maxBytes contract (chunks are bounded at ~10KB by the indexer).
			if len(text) > maxBytes {
				text = text[:maxBytes] + fmt.Sprintf("\n[content truncated at %d bytes, use Read tool for full text]", maxBytes)
			}
			var rangeStr string
			if chunk.StartLine != -1 {
				rangeStr = fmt.Sprintf(", indexed lines %d-%d", chunk.StartLine, chunk.EndLine)
			}
			fmt.Fprintf(sb, "\n#### Content (indexed snapshot%s, max %d bytes)\n", rangeStr, maxBytes)
			sb.WriteString("```markdown\n")
			sb.WriteString(text)
			sb.WriteString("\n```\n")
			return
		}
	}

	// Fallback: live file read (chunk not yet indexed).
	root := h.getProjectRootForResolvedNode(node)
	if root == "" {
		sb.WriteString("\n#### Content\n[content unavailable: project root not available]\n")
		return
	}
	content, err := store.ReadSectionContent(node.FilePath, node.StartLine, node.EndLine, root, maxBytes)
	if err != nil {
		fmt.Fprintf(sb, "\n#### Content\n[content unavailable: %v]\n", err)
		return
	}
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return
	}
	fmt.Fprintf(sb, "\n#### Content (indexed lines %d-%d, max %d bytes)\n", node.StartLine, node.EndLine, maxBytes)
	sb.WriteString("```markdown\n")
	sb.WriteString(content)
	sb.WriteString("\n```\n")
	sb.WriteString("[live read — chunk not yet indexed; run docgraph index --force]\n")
}

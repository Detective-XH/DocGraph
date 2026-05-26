package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

var contextTool = mcp.NewTool("docgraph_context",
	mcp.WithDescription("PRIMARY TOOL. Build relevant documentation context for a task or topic. Composes governance-aware search + node details + cross-references + bounded source content in one call. For a single known document, use docgraph_node instead."),
	mcp.WithString("task", mcp.Required(), mcp.Description("Description of the task/topic to find context for")),
	mcp.WithString("format", mcp.Description("Output format: summary (default) or context_pack for a reviewable evidence pack.")),
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
)

var nodeTool = mcp.NewTool("docgraph_node",
	mcp.WithDescription("Get a single document or heading's full details: metadata, structure, and cross-references. Use 'section' to read the full content of a specific heading section from the source file. For multiple documents, use docgraph_explore instead."),
	mcp.WithString("document", mcp.Required(), mcp.Description("Document name, path, or heading qualified name")),
	mcp.WithBoolean("includeBody", mcp.Description("Include body excerpt (default true)")),
	mcp.WithString("section", mcp.Description("Return full content of a specific heading section (by name)")),
)

var exploreTool = mcp.NewTool("docgraph_explore",
	mcp.WithDescription("Survey several related documents and their cross-references in one call. More efficient than multiple docgraph_node calls. For a single known document, use docgraph_node instead."),
	mcp.WithString("query", mcp.Required(), mcp.Description("Search terms to find related documents")),
	mcp.WithNumber("maxDocs", mcp.Description("Max documents (default 5)")),
)

func (h *handler) getStoreForNode(nodeID string) *store.Store {
	if h.workspace != nil {
		for _, p := range h.workspace.Projects {
			if n, err := p.Store.GetNodeByID(nodeID); err == nil && n != nil {
				return p.Store
			}
		}
		return nil
	}
	return h.store
}

func (h *handler) getStoreForResolvedNode(node *store.Node) *store.Store {
	if node == nil {
		return nil
	}
	if h.workspace != nil && node.ProjectName != "" {
		if p := h.workspace.FindProject(node.ProjectName); p != nil {
			return p.Store
		}
	}
	return h.getStoreForNode(node.ID)
}

func (h *handler) getProjectRootForResolvedNode(node *store.Node) string {
	if node == nil {
		return ""
	}
	if h.workspace != nil && node.ProjectName != "" {
		if p := h.workspace.FindProject(node.ProjectName); p != nil {
			return p.Path
		}
	}
	if h.workspace != nil {
		for _, p := range h.workspace.Projects {
			if n, err := p.Store.GetNodeByID(node.ID); err == nil && n != nil {
				return p.Path
			}
		}
		return ""
	}
	return h.projectRoot
}

func (h *handler) getHeadings(node *store.Node) []store.Node {
	if h.workspace != nil {
		if node.ProjectName != "" {
			if p := h.workspace.FindProject(node.ProjectName); p != nil {
				hs, _ := p.Store.GetChildHeadings(node.FilePath)
				for i := range hs {
					hs[i].ProjectName = p.Name
					if hs[i].QualifiedName != "" && !strings.HasPrefix(hs[i].QualifiedName, "[") {
						hs[i].QualifiedName = "[" + p.Name + "] " + hs[i].QualifiedName
					}
				}
				return hs
			}
		}
		for _, p := range h.workspace.Projects {
			if hs, err := p.Store.GetChildHeadings(node.FilePath); err == nil && len(hs) > 0 {
				for i := range hs {
					hs[i].ProjectName = p.Name
				}
				return hs
			}
		}
		return nil
	}
	hs, _ := h.store.GetChildHeadings(node.FilePath)
	return hs
}

func (h *handler) getEdgeCounts(node *store.Node) (inCount, outCount int) {
	if h.workspace != nil {
		if st := h.getStoreForResolvedNode(node); st != nil {
			if es, err := st.GetIncomingEdges(node.ID); err == nil {
				inCount = len(es)
			}
			if es, err := st.GetOutgoingEdges(node.ID); err == nil {
				outCount = len(es)
			}
			return
		}
		for _, p := range h.workspace.Projects {
			if es, err := p.Store.GetIncomingEdges(node.ID); err == nil {
				inCount += len(es)
			}
			if es, err := p.Store.GetOutgoingEdges(node.ID); err == nil {
				outCount += len(es)
			}
		}
	} else {
		if es, err := h.store.GetIncomingEdges(node.ID); err == nil {
			inCount = len(es)
		}
		if es, err := h.store.GetOutgoingEdges(node.ID); err == nil {
			outCount = len(es)
		}
	}
	return
}

func formatHeadingOutline(headings []store.Node) string {
	var sb strings.Builder
	for _, h := range headings {
		indent := strings.Repeat("  ", h.Level-1)
		sb.WriteString(fmt.Sprintf("%s- H%d: %s\n", indent, h.Level, h.Name))
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

func (h *handler) handleContext(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	task := getStringArg(args, "task", "")
	if task == "" {
		return mcp.NewToolResultError("task parameter is required"), nil
	}
	task = sanitizeArg(task, maxArgLength)
	maxNodes := getIntArg(args, "maxNodes", 10)
	includeContent := getBoolArg(args, "includeContent", true)
	maxContentBytes := getIntArg(args, "maxContentBytes", 2000)
	if maxContentBytes <= 0 {
		maxContentBytes = 2000
	}
	if maxContentBytes > 6000 {
		maxContentBytes = 6000
	}
	opts := store.SearchOptions{
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
	}

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

	format := strings.ToLower(strings.TrimSpace(getStringArg(args, "format", "")))
	if format == "context_pack" || format == "evidence_pack" {
		impactDepth := getIntArg(args, "impactDepth", 1)
		referenceLimit := getIntArg(args, "referenceLimit", 5)
		return mcp.NewToolResultText(h.renderContextPack(task, results, contextPackOptions{
			IncludeContent:  includeContent,
			MaxContentBytes: maxContentBytes,
			ImpactDepth:     impactDepth,
			ReferenceLimit:  referenceLimit,
		})), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Context for %q\n\nFound %d relevant documents.\n", task, len(results)))

	for i, sr := range results {
		node := sr.Node
		headings := h.getHeadings(&node)
		inCount, outCount := h.getEdgeCounts(&node)

		sb.WriteString(fmt.Sprintf("\n### %d. %s\n", i+1, node.Name))
		sb.WriteString(fmt.Sprintf("**Path:** %s | **Headings:** %d | **Refs in:** %d | **Refs out:** %d\n",
			node.FilePath, len(headings), inCount, outCount))

		if len(headings) > 0 {
			sb.WriteString("\n#### Structure\n")
			sb.WriteString(formatHeadingOutline(headings))
		}

		if node.BodyExcerpt != "" {
			sb.WriteString("\n")
			for _, line := range strings.Split(strings.TrimRight(node.BodyExcerpt, "\n"), "\n") {
				sb.WriteString(fmt.Sprintf("> %s\n", line))
			}
		}

		if includeContent {
			appendBoundedContent(&sb, h, &node, maxContentBytes)
		}

		// Governance metadata — appended when available.
		if st := h.getStoreForResolvedNode(&node); st != nil {
			docID := contextPackDocID(node)
			if gov, err := st.GetGovernanceMetadata(docID); err == nil && !store.IsGovernanceEmpty(gov) {
				sb.WriteString(appendGovernanceSection(gov))
			}
			if research, err := st.GetResearchMetadata(docID); err == nil && !store.IsResearchEmpty(research) {
				sb.WriteString(appendResearchSection(research))
			}
			if quality, err := st.GetMetadataQuality(docID, time.Time{}); err == nil && quality != nil {
				sb.WriteString(appendMetadataQualitySection(quality))
			}
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
			// Enforce the caller's maxBytes contract (chunk is bounded by H-19 ~10KB).
			if len(text) > maxBytes {
				text = text[:maxBytes] + fmt.Sprintf("\n[content truncated at %d bytes, use Read tool for full text]", maxBytes)
			}
			var rangeStr string
			if chunk.StartLine != -1 {
				rangeStr = fmt.Sprintf(", indexed lines %d-%d", chunk.StartLine, chunk.EndLine)
			}
			sb.WriteString(fmt.Sprintf("\n#### Content (indexed snapshot%s, max %d bytes)\n", rangeStr, maxBytes))
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
		sb.WriteString(fmt.Sprintf("\n#### Content\n[content unavailable: %v]\n", err))
		return
	}
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return
	}
	sb.WriteString(fmt.Sprintf("\n#### Content (indexed lines %d-%d, max %d bytes)\n", node.StartLine, node.EndLine, maxBytes))
	sb.WriteString("```markdown\n")
	sb.WriteString(content)
	sb.WriteString("\n```\n")
	sb.WriteString("[live read — chunk not yet indexed; run docgraph index --force]\n")
}

func (h *handler) handleNode(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	document := getStringArg(args, "document", "")
	if document == "" {
		return mcp.NewToolResultError("document parameter is required"), nil
	}
	document = sanitizeArg(document, maxArgLength)
	includeBody := true // default
	if v, ok := args["includeBody"]; ok {
		if b, ok := v.(bool); ok {
			includeBody = b
		}
	}
	section := getStringArg(args, "section", "")

	node, err := h.resolveDoc(document)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve document failed: %v", err)), nil
	}
	if node == nil {
		return mcp.NewToolResultError(fmt.Sprintf("document not found: %s — try docgraph_search to find the correct name or path", document)), nil
	}

	headings := h.getHeadings(node)

	var inEdges, outEdges []store.Edge
	if s := h.getStoreForResolvedNode(node); s != nil {
		inEdges, _ = s.GetIncomingEdges(node.ID)
		outEdges, _ = s.GetOutgoingEdges(node.ID)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## %s\n\n", node.Name))
	sb.WriteString(fmt.Sprintf("**Path:** %s\n", node.FilePath))
	sb.WriteString(fmt.Sprintf("**Kind:** %s\n", node.Kind))
	sb.WriteString(fmt.Sprintf("**Lines:** %d-%d\n", node.StartLine, node.EndLine))
	if node.Metadata != "" {
		sb.WriteString(fmt.Sprintf("**Metadata:** %s\n", node.Metadata))
	}

	if len(headings) > 0 {
		sb.WriteString("\n### Structure\n")
		sb.WriteString(formatHeadingOutline(headings))
	}

	if len(inEdges) > 0 {
		sb.WriteString(fmt.Sprintf("\n### Incoming References (%d)\n", len(inEdges)))
		for _, e := range inEdges {
			if src := h.getNodeByIDForNode(node, e.Source); src != nil {
				sb.WriteString(fmt.Sprintf("- %s -> (%s)\n", src.Name, e.Kind))
			} else {
				sb.WriteString(fmt.Sprintf("- %s -> (%s)\n", e.Source, e.Kind))
			}
		}
	}
	if len(outEdges) > 0 {
		sb.WriteString(fmt.Sprintf("\n### Outgoing Links (%d)\n", len(outEdges)))
		for _, e := range outEdges {
			if e.Kind == "links_external" {
				sb.WriteString(fmt.Sprintf("- %s -> (%s)\n", extractURL(e.Metadata), e.Kind))
			} else if tgt := h.getNodeByIDForNode(node, e.Target); tgt != nil {
				sb.WriteString(fmt.Sprintf("- %s -> (%s)\n", tgt.Name, e.Kind))
			} else {
				sb.WriteString(fmt.Sprintf("- %s -> (%s)\n", e.Target, e.Kind))
			}
		}
	}

	if includeBody && node.BodyExcerpt != "" {
		sb.WriteString("\n### Body Excerpt\n")
		for _, line := range strings.Split(strings.TrimRight(node.BodyExcerpt, "\n"), "\n") {
			sb.WriteString(fmt.Sprintf("> %s\n", line))
		}
	}

	// Governance metadata section.
	if s := h.getStoreForResolvedNode(node); s != nil {
		docID := contextPackDocID(*node)
		if gov, err := s.GetGovernanceMetadata(docID); err == nil && !store.IsGovernanceEmpty(gov) {
			sb.WriteString(appendGovernanceSection(gov))
		}
		if research, err := s.GetResearchMetadata(docID); err == nil && !store.IsResearchEmpty(research) {
			sb.WriteString(appendResearchSection(research))
		}
		if quality, err := s.GetMetadataQuality(docID, time.Time{}); err == nil && quality != nil {
			sb.WriteString(appendMetadataQualitySection(quality))
		}
	}

	if s := h.getStoreForResolvedNode(node); s != nil {
		if hist, err := s.GetFileHistory(node.FilePath); err == nil && hist != nil && hist.CommitCount > 0 {
			sb.WriteString("\n### History\n")
			amendWord := "time"
			if hist.CommitCount != 1 {
				amendWord = "times"
			}
			sb.WriteString(fmt.Sprintf("**Amended:** %d %s", hist.CommitCount, amendWord))
			if hist.AuthorCount > 0 {
				authorWord := "author"
				if hist.AuthorCount != 1 {
					authorWord = "authors"
				}
				sb.WriteString(fmt.Sprintf(" by %d %s", hist.AuthorCount, authorWord))
			}
			sb.WriteString("\n")
			if hist.LastSubject != "" {
				sb.WriteString(fmt.Sprintf("**Last commit:** %s\n", hist.LastSubject))
			}
			if hist.LastCommitAt > 0 {
				sb.WriteString(fmt.Sprintf("**Last changed:** %s\n", time.Unix(hist.LastCommitAt, 0).UTC().Format("2006-01-02")))
			}
		}
	}

	// Read full section content from source file when section is specified.
	if section != "" {
		var target *store.Node
		for i := range headings {
			if headings[i].Name == section {
				target = &headings[i]
				break
			}
		}
		if target == nil {
			return mcp.NewToolResultError(fmt.Sprintf("section %q not found in %s — available headings: %s",
				section, node.Name, headingNames(headings))), nil
		}
		// Try indexed section chunk first (TOCTOU-safe).
		const sectionMaxBytes = 2000
		if st := h.getStoreForResolvedNode(target); st != nil {
			if chunk, ok, err := st.GetSectionChunk(target.ID); err == nil && ok {
				var rangeStr string
				if chunk.StartLine != -1 {
					rangeStr = fmt.Sprintf(", lines %d-%d", chunk.StartLine, chunk.EndLine)
				}
				text := chunk.Text
				if len(text) > sectionMaxBytes {
					text = text[:sectionMaxBytes] + fmt.Sprintf("\n[content truncated at %d bytes]", sectionMaxBytes)
				}
				sb.WriteString(fmt.Sprintf("\n### Content (section %q, indexed snapshot%s)\n", section, rangeStr))
				sb.WriteString(text)
				sb.WriteString("\n")
				return mcp.NewToolResultText(sb.String()), nil
			}
		}

		// Fallback: live file read (chunk not yet indexed).
		root := h.getProjectRootForResolvedNode(node)
		if root == "" {
			return mcp.NewToolResultError("cannot read section content: project root not available"), nil
		}
		content, err := store.ReadSectionContent(target.FilePath, target.StartLine, target.EndLine, root, sectionMaxBytes)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("read section content: %v", err)), nil
		}
		sb.WriteString(fmt.Sprintf("\n### Content (section %q, lines %d-%d)\n", section, target.StartLine, target.EndLine))
		sb.WriteString(content)
		sb.WriteString("\n")
		sb.WriteString("[live read — chunk not yet indexed; run docgraph index --force]\n")
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (h *handler) handleExplore(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	query := getStringArg(args, "query", "")
	if query == "" {
		return mcp.NewToolResultError("query parameter is required"), nil
	}
	query = sanitizeArg(query, maxArgLength)
	maxDocs := getIntArg(args, "maxDocs", 5)

	var results []store.SearchResult
	var err error
	if h.workspace != nil {
		results, err = h.workspace.Search(query, "", maxDocs)
	} else {
		results, err = h.store.Search(query, "", maxDocs)
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Explore: %q\n\n", query))

	for i, sr := range results {
		node := sr.Node
		headings := h.getHeadings(&node)
		inCount, outCount := h.getEdgeCounts(&node)

		headingNames := make([]string, len(headings))
		for j, hd := range headings {
			headingNames[j] = hd.Name
		}

		sb.WriteString(fmt.Sprintf("### %d. %s (%s)\n", i+1, node.Name, node.FilePath))
		if len(headingNames) > 0 {
			sb.WriteString(fmt.Sprintf("Headings: %s\n", strings.Join(headingNames, ", ")))
		}
		sb.WriteString(fmt.Sprintf("Links out: %d | Links in: %d\n", outCount, inCount))

		if node.BodyExcerpt != "" {
			for _, line := range strings.Split(strings.TrimRight(node.BodyExcerpt, "\n"), "\n") {
				sb.WriteString(fmt.Sprintf("> %s\n", line))
			}
		}
		sb.WriteString("\n")
	}

	return mcp.NewToolResultText(sb.String()), nil
}

var similarTool = mcp.NewTool("docgraph_similar",
	mcp.WithDescription("Find documents that are topically similar to a given document, even without explicit links. Uses TF-IDF text similarity + shared references + tag overlap. If neural embeddings have been stored via docgraph_embeddings_store, results also include neural similarity scores (engine: neural). For explicit link tracking, use docgraph_references instead."),
	mcp.WithString("document", mcp.Required(), mcp.Description("Document name or path")),
	mcp.WithNumber("limit", mcp.Description("Max results (default 10)")),
)

func (h *handler) handleSimilar(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	document := getStringArg(args, "document", "")
	if document == "" {
		return mcp.NewToolResultError("document parameter is required"), nil
	}
	document = sanitizeArg(document, maxArgLength)
	limit := getIntArg(args, "limit", 10)

	node, err := h.resolveDoc(document)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve failed: %v", err)), nil
	}
	if node == nil {
		return mcp.NewToolResultError(fmt.Sprintf("document not found: %s — try docgraph_search to find the correct name or path", document)), nil
	}

	// Query similar_to edges (both directions) for this document.
	var edges []store.Edge
	if h.workspace != nil {
		for _, p := range h.workspace.Projects {
			if es, err := p.Store.GetSimilarEdgesForDoc(node.ID); err == nil {
				edges = append(edges, es...)
			}
		}
	} else {
		var err error
		edges, err = h.store.GetSimilarEdgesForDoc(node.ID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("get similar edges: %v", err)), nil
		}
	}

	// Deduplicate: same pair can have both TF-IDF and neural edges.
	// Keep one edge per pair, preferring engine=neural over tfidf.
	type pairKey struct{ a, b string }
	best := make(map[pairKey]store.Edge)
	for _, e := range edges {
		src, tgt := e.Source, e.Target
		if src > tgt {
			src, tgt = tgt, src
		}
		k := pairKey{src, tgt}
		existing, ok := best[k]
		if !ok {
			best[k] = e
			continue
		}
		// Prefer neural engine.
		var existingEng, newEng string
		var m map[string]interface{}
		if json.Unmarshal([]byte(existing.Metadata), &m) == nil {
			existingEng, _ = m["engine"].(string)
		}
		if json.Unmarshal([]byte(e.Metadata), &m) == nil {
			newEng, _ = m["engine"].(string)
		}
		if newEng == "neural" && existingEng != "neural" {
			best[k] = e
		}
	}

	deduped := make([]store.Edge, 0, len(best))
	for _, e := range best {
		deduped = append(deduped, e)
	}
	if limit > 0 && len(deduped) > limit {
		deduped = deduped[:limit]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Documents similar to %q\n\nFound %d similar documents.\n", node.Name, len(deduped)))

	for i, e := range deduped {
		otherID := e.Target
		if otherID == node.ID {
			otherID = e.Source
		}
		other := h.getNodeByID(otherID)
		if other == nil {
			continue
		}
		score := ""
		if e.Metadata != "" {
			var m map[string]interface{}
			if json.Unmarshal([]byte(e.Metadata), &m) == nil {
				if s, ok := m["score"].(float64); ok {
					score = fmt.Sprintf(" (score: %.2f", s)
					if eng, ok := m["engine"].(string); ok {
						score += ", engine: " + eng
						if mid, ok := m["model_id"].(string); ok {
							score += ", model: " + mid
						}
					}
					score += ")"
				}
			}
		}
		sb.WriteString(fmt.Sprintf("\n%d. **%s** %s%s\n", i+1, other.Name, other.FilePath, score))
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// appendGovernanceSection formats governance metadata as a Markdown section string.
func appendGovernanceSection(g *store.GovernanceRecord) string {
	if store.IsGovernanceEmpty(g) {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n### Governance\n")
	if g.Status != "" {
		sb.WriteString(fmt.Sprintf("**Status:** %s\n", g.Status))
	}
	if g.Sensitivity != "" {
		sb.WriteString(fmt.Sprintf("**Sensitivity:** %s\n", g.Sensitivity))
	}
	if g.Owner != "" {
		sb.WriteString(fmt.Sprintf("**Owner:** %s\n", g.Owner))
	}
	if g.Approver != "" {
		sb.WriteString(fmt.Sprintf("**Approver:** %s\n", g.Approver))
	}
	if g.Department != "" {
		sb.WriteString(fmt.Sprintf("**Department:** %s\n", g.Department))
	}
	if g.EffectiveDate != "" {
		sb.WriteString(fmt.Sprintf("**Effective:** %s\n", g.EffectiveDate))
	}
	if g.ReviewDue != "" {
		sb.WriteString(fmt.Sprintf("**Review due:** %s\n", g.ReviewDue))
	}
	if g.Supersedes != "" {
		sb.WriteString(fmt.Sprintf("**Supersedes:** %s\n", g.Supersedes))
	}
	if g.SupersededBy != "" {
		sb.WriteString(fmt.Sprintf("**Superseded by:** %s\n", g.SupersededBy))
	}
	if g.CanonicalSource != "" {
		sb.WriteString(fmt.Sprintf("**Canonical source:** %s\n", g.CanonicalSource))
	}
	if g.AllowedAudience != "" {
		sb.WriteString(fmt.Sprintf("**Audience:** %s\n", g.AllowedAudience))
	}
	return sb.String()
}

// appendResearchSection formats research provenance metadata as a Markdown section string.
func appendResearchSection(r *store.ResearchRecord) string {
	if store.IsResearchEmpty(r) {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n### Research Provenance\n")
	if r.ClaimID != "" {
		sb.WriteString(fmt.Sprintf("**Claim ID:** %s\n", r.ClaimID))
	}
	if r.Confidence != "" {
		sb.WriteString(fmt.Sprintf("**Confidence:** %s\n", r.Confidence))
	}
	if r.SourceType != "" {
		sb.WriteString(fmt.Sprintf("**Source type:** %s\n", r.SourceType))
	}
	if r.AnalystStatus != "" {
		sb.WriteString(fmt.Sprintf("**Analyst status:** %s\n", r.AnalystStatus))
	}
	if r.EventDate != "" {
		sb.WriteString(fmt.Sprintf("**Event date:** %s\n", r.EventDate))
	}
	if r.AssessmentDate != "" {
		sb.WriteString(fmt.Sprintf("**Assessment date:** %s\n", r.AssessmentDate))
	}
	if r.LastVerified != "" {
		sb.WriteString(fmt.Sprintf("**Last verified:** %s\n", r.LastVerified))
	}
	if r.ValidUntil != "" {
		sb.WriteString(fmt.Sprintf("**Valid until:** %s\n", r.ValidUntil))
	}
	if r.Client != "" {
		sb.WriteString(fmt.Sprintf("**Client:** %s\n", r.Client))
	}
	if r.DeliverableID != "" {
		sb.WriteString(fmt.Sprintf("**Deliverable ID:** %s\n", r.DeliverableID))
	}
	if r.Evidence != "" {
		sb.WriteString(fmt.Sprintf("**Evidence:** %s\n", r.Evidence))
	}
	return sb.String()
}

// appendMetadataQualitySection formats advisory F-27 metadata quality signals.
func appendMetadataQualitySection(q *store.MetadataQualityRecord) string {
	if q == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n### Metadata Quality\n")
	sb.WriteString(fmt.Sprintf("**Score:** %d/100 (%s)\n", q.Score, q.Level))
	sb.WriteString(fmt.Sprintf("**References:** %d incoming, %d outgoing\n", q.IncomingReferences, q.OutgoingReferences))
	if len(q.Issues) == 0 {
		sb.WriteString("**Issues:** none\n")
		return sb.String()
	}
	sb.WriteString("**Issues:**\n")
	limit := len(q.Issues)
	if limit > 6 {
		limit = 6
	}
	for _, issue := range q.Issues[:limit] {
		sb.WriteString(fmt.Sprintf("- `%s` (%s, -%d): %s\n", issue.Code, issue.Severity, issue.Penalty, issue.Message))
	}
	if len(q.Issues) > limit {
		sb.WriteString(fmt.Sprintf("- ... %d more issues omitted\n", len(q.Issues)-limit))
	}
	return sb.String()
}

package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Detective-XH/docgraph/internal/parser"
	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

var nodeTool = mcp.NewTool("docgraph_node",
	mcp.WithDescription("Get a single document or heading's full details: metadata, structure, and cross-references. Use 'section' to read the full content of a specific heading section from the source file. For multiple documents, use docgraph_explore instead."),
	mcp.WithString("document", mcp.Required(), mcp.Description("Document name, path, or heading qualified name (e.g. 'docs/guide.md' or 'guide.md#Installation')")),
	mcp.WithBoolean("includeBody", mcp.Description("Include body excerpt (default true)")),
	mcp.WithString("section", mcp.Description("Return full content of a specific heading section. Accepts the exact heading text OR the anchor slug shown in search results (e.g. 'Neural Embeddings (agent-driven)' or 'neural-embeddings-agent-driven').")),
)

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

	st := h.getStoreForResolvedNode(node)
	var inEdges, outEdges []store.Edge
	if st != nil {
		inEdges, _ = st.GetIncomingEdges(node.ID)
		outEdges, _ = st.GetOutgoingEdges(node.ID)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## %s\n\n", node.Name)
	fmt.Fprintf(&sb, "**Path:** %s\n", node.FilePath)
	fmt.Fprintf(&sb, "**Kind:** %s\n", node.Kind)
	fmt.Fprintf(&sb, "**Lines:** %d-%d\n", node.StartLine, node.EndLine)
	if node.Metadata != "" {
		fmt.Fprintf(&sb, "**Metadata:** %s\n", node.Metadata)
	}

	if len(headings) > 0 {
		sb.WriteString("\n### Structure\n")
		sb.WriteString(formatHeadingOutline(headings))
	}

	if len(inEdges) > 0 {
		fmt.Fprintf(&sb, "\n### Incoming References (%d)\n", len(inEdges))
		// Emit the same derived-count summary as docgraph_graph operation=incoming.
		// handleNode never truncates inEdges, so the counts are honest over the full set.
		inTotal, inDistinctOther, inSameDoc := h.incomingEdgeSummary(node, st, inEdges)
		fmt.Fprintf(&sb, incomingSummaryFmt, inTotal, inDistinctOther, inSameDoc)
		for _, e := range inEdges {
			// Show the source's path (and flag same-document references) so a
			// self-reference can be told apart from a cross-document citation
			// without a follow-up docgraph_graph call — parity with the facade.
			if src := h.getNodeByIDForNode(node, e.Source); src != nil {
				if src.FilePath == node.FilePath {
					fmt.Fprintf(&sb, "- %s -> (%s) [same-document]\n", src.Name, e.Kind)
				} else {
					fmt.Fprintf(&sb, "- %s -> (%s) [%s]\n", src.Name, e.Kind, src.FilePath)
				}
			} else {
				fmt.Fprintf(&sb, "- %s -> (%s)\n", e.Source, e.Kind)
			}
		}
	}
	if len(outEdges) > 0 {
		fmt.Fprintf(&sb, "\n### Outgoing Links (%d)\n", len(outEdges))
		// Emit the same derived-count summary as docgraph_graph operation=outgoing.
		// handleNode never truncates outEdges, so the counts are honest over the full set.
		outTotal, outDistinctOther, outSameDoc, outExternal := h.outgoingEdgeSummary(node, st, outEdges)
		fmt.Fprintf(&sb, outgoingSummaryFmt, outTotal, outDistinctOther, outSameDoc, outExternal)
		for _, e := range outEdges {
			if e.Kind == "links_external" {
				fmt.Fprintf(&sb, "- %s -> (%s)\n", extractURL(e.Metadata), e.Kind)
			} else if tgt := h.getNodeByIDForNode(node, e.Target); tgt != nil {
				if tgt.FilePath == node.FilePath {
					fmt.Fprintf(&sb, "- %s -> (%s) [same-document]\n", tgt.Name, e.Kind)
				} else {
					fmt.Fprintf(&sb, "- %s -> (%s) [%s]\n", tgt.Name, e.Kind, tgt.FilePath)
				}
			} else {
				fmt.Fprintf(&sb, "- %s -> (%s)\n", e.Target, e.Kind)
			}
		}
	}

	if includeBody && node.BodyExcerpt != "" {
		sb.WriteString("\n### Body Excerpt\n")
		for line := range strings.SplitSeq(strings.TrimRight(node.BodyExcerpt, "\n"), "\n") {
			fmt.Fprintf(&sb, "> %s\n", line)
		}
	}

	// Governance metadata section.
	if s := h.getStoreForResolvedNode(node); s != nil {
		docID := contextPackDocID(*node)
		if summary, err := s.GetAISummary(docID); err == nil && summary != nil {
			sb.WriteString(appendAISummarySection(summary))
		}
		if gov, err := s.GetGovernanceMetadata(docID); err == nil && !store.IsGovernanceEmpty(gov) {
			sb.WriteString(appendGovernanceSection(gov))
		}
		if research, err := s.GetResearchMetadata(docID); err == nil && !store.IsResearchEmpty(research) {
			sb.WriteString(appendResearchSection(research))
		}
		if quality, err := s.GetMetadataQuality(docID, time.Time{}); err == nil && quality != nil {
			sb.WriteString(appendMetadataQualitySection(quality))
		}
		if mentions, err := s.Entity.GetEntityMentions(node.ID); err == nil {
			sb.WriteString(appendEntitySection(s.Entity, mentions))
		}
	}

	if s := h.getStoreForResolvedNode(node); s != nil {
		if hist, err := s.GetFileHistory(node.FilePath); err == nil && hist != nil && hist.CommitCount > 0 {
			sb.WriteString("\n### History\n")
			amendWord := "time"
			if hist.CommitCount != 1 {
				amendWord = "times"
			}
			fmt.Fprintf(&sb, "**Amended:** %d %s", hist.CommitCount, amendWord)
			if hist.AuthorCount > 0 {
				authorWord := "author"
				if hist.AuthorCount != 1 {
					authorWord = "authors"
				}
				fmt.Fprintf(&sb, " by %d %s", hist.AuthorCount, authorWord)
			}
			sb.WriteString("\n")
			if hist.LastSubject != "" {
				fmt.Fprintf(&sb, "**Last commit:** %s\n", hist.LastSubject)
			}
			if hist.LastCommitAt > 0 {
				fmt.Fprintf(&sb, "**Last changed:** %s\n", time.Unix(hist.LastCommitAt, 0).UTC().Format("2006-01-02"))
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
			// Fall back to the anchor slug shown in search results. Heading node
			// IDs are relPath#slug (parser.Slugify of the heading text), so an
			// agent that pastes the "#slug" suffix from a search hit — or the raw
			// heading text in any casing — resolves via the same slug algorithm.
			want := parser.Slugify(strings.TrimPrefix(section, "#"))
			for i := range headings {
				if _, slug, ok := strings.Cut(headings[i].ID, "#"); ok && slug == want {
					target = &headings[i]
					break
				}
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
				fmt.Fprintf(&sb, "\n### Content (section %q, indexed snapshot%s)\n", section, rangeStr)
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
		fmt.Fprintf(&sb, "\n### Content (section %q, lines %d-%d)\n", section, target.StartLine, target.EndLine)
		sb.WriteString(content)
		sb.WriteString("\n")
		sb.WriteString("[live read — chunk not yet indexed; run docgraph index --force]\n")
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
		fmt.Fprintf(&sb, "**Status:** %s\n", g.Status)
	}
	if g.Sensitivity != "" {
		fmt.Fprintf(&sb, "**Sensitivity:** %s\n", g.Sensitivity)
	}
	if g.Owner != "" {
		fmt.Fprintf(&sb, "**Owner:** %s\n", g.Owner)
	}
	if g.Approver != "" {
		fmt.Fprintf(&sb, "**Approver:** %s\n", g.Approver)
	}
	if g.Department != "" {
		fmt.Fprintf(&sb, "**Department:** %s\n", g.Department)
	}
	if g.EffectiveDate != "" {
		fmt.Fprintf(&sb, "**Effective:** %s\n", g.EffectiveDate)
	}
	if g.ReviewDue != "" {
		fmt.Fprintf(&sb, "**Review due:** %s\n", g.ReviewDue)
	}
	if g.Supersedes != "" {
		fmt.Fprintf(&sb, "**Supersedes:** %s\n", g.Supersedes)
	}
	if g.SupersededBy != "" {
		fmt.Fprintf(&sb, "**Superseded by:** %s\n", g.SupersededBy)
	}
	if g.CanonicalSource != "" {
		fmt.Fprintf(&sb, "**Canonical source:** %s\n", g.CanonicalSource)
	}
	if g.AllowedAudience != "" {
		fmt.Fprintf(&sb, "**Audience:** %s\n", g.AllowedAudience)
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
		fmt.Fprintf(&sb, "**Claim ID:** %s\n", r.ClaimID)
	}
	if r.Confidence != "" {
		fmt.Fprintf(&sb, "**Confidence:** %s\n", r.Confidence)
	}
	if r.SourceType != "" {
		fmt.Fprintf(&sb, "**Source type:** %s\n", r.SourceType)
	}
	if r.AnalystStatus != "" {
		fmt.Fprintf(&sb, "**Analyst status:** %s\n", r.AnalystStatus)
	}
	if r.EventDate != "" {
		fmt.Fprintf(&sb, "**Event date:** %s\n", r.EventDate)
	}
	if r.AssessmentDate != "" {
		fmt.Fprintf(&sb, "**Assessment date:** %s\n", r.AssessmentDate)
	}
	if r.LastVerified != "" {
		fmt.Fprintf(&sb, "**Last verified:** %s\n", r.LastVerified)
	}
	if r.ValidUntil != "" {
		fmt.Fprintf(&sb, "**Valid until:** %s\n", r.ValidUntil)
	}
	if r.Client != "" {
		fmt.Fprintf(&sb, "**Client:** %s\n", r.Client)
	}
	if r.DeliverableID != "" {
		fmt.Fprintf(&sb, "**Deliverable ID:** %s\n", r.DeliverableID)
	}
	if r.Evidence != "" {
		fmt.Fprintf(&sb, "**Evidence:** %s\n", r.Evidence)
	}
	return sb.String()
}

// appendMetadataQualitySection formats advisory metadata quality signals.
func appendMetadataQualitySection(q *store.MetadataQualityRecord) string {
	if q == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n### Metadata Quality\n")
	fmt.Fprintf(&sb, "**Score:** %d/100 (%s)\n", q.Score, q.Level)
	fmt.Fprintf(&sb, "**References:** %d incoming, %d outgoing\n", q.IncomingReferences, q.OutgoingReferences)
	if len(q.Issues) == 0 {
		sb.WriteString("**Issues:** none\n")
		return sb.String()
	}
	sb.WriteString("**Issues:**\n")
	limit := min(len(q.Issues), 6)
	for _, issue := range q.Issues[:limit] {
		fmt.Fprintf(&sb, "- `%s` (%s, -%d): %s\n", issue.Code, issue.Severity, issue.Penalty, issue.Message)
	}
	if len(q.Issues) > limit {
		fmt.Fprintf(&sb, "- ... %d more issues omitted\n", len(q.Issues)-limit)
	}
	return sb.String()
}

package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

var nodeTool = mcp.NewTool("docgraph_node",
	mcp.WithDescription("Get a single document or heading's full details: metadata, structure, and cross-references. Use 'section' to read the full content of a specific heading section from the source file. For multiple documents, use docgraph_explore instead."),
	mcp.WithString("document", mcp.Required(), mcp.Description("Document name, path, or heading qualified name")),
	mcp.WithBoolean("includeBody", mcp.Description("Include body excerpt (default true)")),
	mcp.WithString("section", mcp.Description("Return full content of a specific heading section (by name)")),
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
		if mentions, err := s.GetEntityMentions(node.ID); err == nil {
			sb.WriteString(appendEntitySection(mentions))
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

// appendMetadataQualitySection formats advisory metadata quality signals.
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

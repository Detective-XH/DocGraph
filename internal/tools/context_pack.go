package tools

import (
	"fmt"
	"strings"
	"time"

	"github.com/Detective-XH/docgraph/internal/store"
)

type contextPackOptions struct {
	IncludeContent  bool
	MaxContentBytes int
	ImpactDepth     int
	ReferenceLimit  int
}

func (opts contextPackOptions) normalized() contextPackOptions {
	if opts.MaxContentBytes <= 0 {
		opts.MaxContentBytes = 2000
	}
	if opts.MaxContentBytes > 6000 {
		opts.MaxContentBytes = 6000
	}
	if opts.ImpactDepth < 1 {
		opts.ImpactDepth = 1
	}
	if opts.ImpactDepth > 3 {
		opts.ImpactDepth = 3
	}
	if opts.ReferenceLimit <= 0 {
		opts.ReferenceLimit = 5
	}
	if opts.ReferenceLimit > 20 {
		opts.ReferenceLimit = 20
	}
	return opts
}

func (h *handler) renderContextPack(task string, results []store.SearchResult, opts contextPackOptions) string {
	opts = opts.normalized()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Context Pack: %s\n\n", task))
	sb.WriteString("## Manifest\n")
	sb.WriteString("- **Format:** docgraph.context_pack.v1\n")
	sb.WriteString(fmt.Sprintf("- **Query:** %s\n", task))
	sb.WriteString(fmt.Sprintf("- **Items:** %d\n", len(results)))
	sb.WriteString("- **Snapshot source:** indexed `section_chunks` when available; live file reads are not used in context packs.\n")
	sb.WriteString("- **Compatibility:** pack fields are additive; consumers should ignore unknown future fields.\n")

	for i, sr := range results {
		h.appendContextPackItem(&sb, i+1, sr.Node, opts)
	}

	return sb.String()
}

func (h *handler) appendContextPackItem(sb *strings.Builder, index int, node store.Node, opts contextPackOptions) {
	st := h.getStoreForResolvedNode(&node)
	docID := contextPackDocID(node)
	var docNode *store.Node
	if st != nil {
		docNode = h.getNodeByIDFromStore(st, docID)
	}
	if docNode == nil && node.Kind == "document" {
		docNode = &node
	}

	sb.WriteString(fmt.Sprintf("\n## Evidence %d: %s\n", index, node.Name))
	sb.WriteString("### Identity\n")
	sb.WriteString(fmt.Sprintf("- **Node ID:** %s\n", node.ID))
	sb.WriteString(fmt.Sprintf("- **Document ID:** %s\n", docID))
	sb.WriteString(fmt.Sprintf("- **Kind:** %s\n", node.Kind))
	sb.WriteString(fmt.Sprintf("- **Path:** %s\n", formatNodePath(node)))
	if node.QualifiedName != "" {
		sb.WriteString(fmt.Sprintf("- **Qualified name:** %s\n", node.QualifiedName))
	}
	if node.StartLine > 0 {
		sb.WriteString(fmt.Sprintf("- **Lines:** %d-%d\n", node.StartLine, node.EndLine))
	}

	h.appendContextPackSnapshot(sb, st, &node, opts)
	h.appendContextPackMetadata(sb, st, docID)
	h.appendContextPackQuality(sb, st, docID)
	h.appendContextPackReferences(sb, st, docNode, opts.ReferenceLimit)
	h.appendContextPackImpact(sb, st, docID, opts.ImpactDepth, opts.ReferenceLimit)
}

func (h *handler) appendContextPackSnapshot(sb *strings.Builder, st *store.Store, node *store.Node, opts contextPackOptions) {
	sb.WriteString("\n### Evidence Snapshot\n")
	if st == nil {
		sb.WriteString("- **Status:** unavailable; store could not be resolved.\n")
		return
	}
	chunk, ok, err := st.GetSectionChunk(node.ID)
	if err != nil {
		sb.WriteString(fmt.Sprintf("- **Status:** unavailable; %v.\n", err))
		return
	}
	if !ok {
		if fileHash, err := st.GetFileHash(node.FilePath); err == nil && fileHash != "" {
			sb.WriteString(fmt.Sprintf("- **Content hash:** %s\n", fileHash))
		}
		sb.WriteString("- **Section hash:** unavailable; indexed section snapshot missing.\n")
		sb.WriteString("- **Snapshot note:** run `docgraph index --force` to rebuild section chunks.\n")
		return
	}

	sb.WriteString(fmt.Sprintf("- **Source path:** %s\n", chunk.FilePath))
	if chunk.StartLine > 0 {
		sb.WriteString(fmt.Sprintf("- **Indexed lines:** %d-%d\n", chunk.StartLine, chunk.EndLine))
	}
	if chunk.HeadingPath != "" {
		sb.WriteString(fmt.Sprintf("- **Heading path:** %s\n", chunk.HeadingPath))
	}
	sb.WriteString(fmt.Sprintf("- **Content hash:** %s\n", chunk.ContentHash))
	sb.WriteString(fmt.Sprintf("- **Section hash:** %s\n", chunk.SectionHash))

	if !opts.IncludeContent {
		return
	}
	text := strings.TrimRight(chunk.Text, "\n")
	if text == "" {
		return
	}
	if len(text) > opts.MaxContentBytes {
		text = text[:opts.MaxContentBytes] + fmt.Sprintf("\n[content truncated at %d bytes]", opts.MaxContentBytes)
	}
	sb.WriteString("\n#### Section Content\n")
	sb.WriteString("```markdown\n")
	sb.WriteString(text)
	sb.WriteString("\n```\n")
}

func (h *handler) appendContextPackMetadata(sb *strings.Builder, st *store.Store, docID string) {
	if st == nil {
		return
	}
	gov, _ := st.GetGovernanceMetadata(docID)
	research, _ := st.GetResearchMetadata(docID)
	if store.IsGovernanceEmpty(gov) && store.IsResearchEmpty(research) {
		return
	}
	sb.WriteString("\n### Retrieval Metadata\n")
	if !store.IsGovernanceEmpty(gov) {
		sb.WriteString("#### Governance\n")
		writeContextPackField(sb, "Status", gov.Status)
		writeContextPackField(sb, "Sensitivity", gov.Sensitivity)
		writeContextPackField(sb, "Owner", gov.Owner)
		writeContextPackField(sb, "Approver", gov.Approver)
		writeContextPackField(sb, "Department", gov.Department)
		writeContextPackField(sb, "Effective date", gov.EffectiveDate)
		writeContextPackField(sb, "Review due", gov.ReviewDue)
		writeContextPackField(sb, "Supersedes", gov.Supersedes)
		writeContextPackField(sb, "Superseded by", gov.SupersededBy)
		writeContextPackField(sb, "Allowed audience", gov.AllowedAudience)
		writeContextPackField(sb, "Canonical source", gov.CanonicalSource)
	}
	if !store.IsResearchEmpty(research) {
		sb.WriteString("#### Research\n")
		writeContextPackField(sb, "Claim ID", research.ClaimID)
		writeContextPackField(sb, "Evidence", research.Evidence)
		writeContextPackField(sb, "Source type", research.SourceType)
		writeContextPackField(sb, "Confidence", research.Confidence)
		writeContextPackField(sb, "Event date", research.EventDate)
		writeContextPackField(sb, "Assessment date", research.AssessmentDate)
		writeContextPackField(sb, "Last verified", research.LastVerified)
		writeContextPackField(sb, "Valid until", research.ValidUntil)
		writeContextPackField(sb, "Analyst status", research.AnalystStatus)
		writeContextPackField(sb, "Client", research.Client)
		writeContextPackField(sb, "Deliverable ID", research.DeliverableID)
	}
}

func (h *handler) appendContextPackQuality(sb *strings.Builder, st *store.Store, docID string) {
	if st == nil || docID == "" {
		return
	}
	quality, err := st.GetMetadataQuality(docID, time.Time{})
	if err != nil || quality == nil {
		return
	}
	sb.WriteString("\n### Metadata Quality\n")
	sb.WriteString(fmt.Sprintf("- **Score:** %d/100\n", quality.Score))
	sb.WriteString(fmt.Sprintf("- **Level:** %s\n", quality.Level))
	sb.WriteString(fmt.Sprintf("- **As of:** %s\n", quality.AsOf))
	sb.WriteString(fmt.Sprintf("- **Incoming references:** %d\n", quality.IncomingReferences))
	sb.WriteString(fmt.Sprintf("- **Outgoing references:** %d\n", quality.OutgoingReferences))
	if len(quality.Issues) == 0 {
		sb.WriteString("- **Issues:** none\n")
		return
	}
	sb.WriteString("- **Issues:**\n")
	limit := len(quality.Issues)
	if limit > 8 {
		limit = 8
	}
	for _, issue := range quality.Issues[:limit] {
		sb.WriteString(fmt.Sprintf("  - `%s` (%s, -%d): %s\n", issue.Code, issue.Severity, issue.Penalty, issue.Message))
	}
	if len(quality.Issues) > limit {
		sb.WriteString(fmt.Sprintf("  - ... %d more quality issues omitted\n", len(quality.Issues)-limit))
	}
}

func (h *handler) appendContextPackReferences(sb *strings.Builder, st *store.Store, docNode *store.Node, limit int) {
	if st == nil || docNode == nil {
		return
	}
	incoming, _ := st.GetIncomingEdges(docNode.ID)
	outgoing, _ := st.GetOutgoingEdges(docNode.ID)

	sb.WriteString("\n### Citation Paths\n")
	sb.WriteString(fmt.Sprintf("- **Incoming references:** %d\n", len(incoming)))
	for _, edge := range limitEdges(incoming, limit) {
		src := h.getNodeByIDFromStore(st, edge.Source)
		sb.WriteString(fmt.Sprintf("  - %s --%s--> %s%s\n",
			contextPackNodeLabel(src, edge.Source), edge.Kind, contextPackNodeLabel(docNode, docNode.ID), contextPackLineSuffix(edge.Line)))
	}
	if len(incoming) > limit {
		sb.WriteString(fmt.Sprintf("  - ... %d more incoming references omitted\n", len(incoming)-limit))
	}

	sb.WriteString(fmt.Sprintf("- **Outgoing references:** %d\n", len(outgoing)))
	for _, edge := range limitEdges(outgoing, limit) {
		if edge.Kind == "links_external" {
			sb.WriteString(fmt.Sprintf("  - %s --%s--> %s%s\n",
				contextPackNodeLabel(docNode, docNode.ID), edge.Kind, extractURL(edge.Metadata), contextPackLineSuffix(edge.Line)))
			continue
		}
		tgt := h.getNodeByIDFromStore(st, edge.Target)
		sb.WriteString(fmt.Sprintf("  - %s --%s--> %s%s\n",
			contextPackNodeLabel(docNode, docNode.ID), edge.Kind, contextPackNodeLabel(tgt, edge.Target), contextPackLineSuffix(edge.Line)))
	}
	if len(outgoing) > limit {
		sb.WriteString(fmt.Sprintf("  - ... %d more outgoing references omitted\n", len(outgoing)-limit))
	}
}

func (h *handler) appendContextPackImpact(sb *strings.Builder, st *store.Store, docID string, depth, limit int) {
	if st == nil || docID == "" {
		return
	}
	levels := h.contextPackImpactLevels(st, docID, depth)
	sb.WriteString("\n### Impacted Documents\n")
	total := 0
	for level := 1; level <= depth; level++ {
		entries := levels[level]
		total += len(entries)
		sb.WriteString(fmt.Sprintf("- **Depth %d:** %d documents\n", level, len(entries)))
		shown := entries
		if len(shown) > limit {
			shown = shown[:limit]
		}
		for _, entry := range shown {
			n := h.getNodeByIDFromStore(st, entry.docID)
			if entry.via != "" {
				via := h.getNodeByIDFromStore(st, entry.via)
				sb.WriteString(fmt.Sprintf("  - %s via %s through %s\n",
					contextPackNodeLabel(n, entry.docID), entry.kind, contextPackNodeLabel(via, entry.via)))
			} else {
				sb.WriteString(fmt.Sprintf("  - %s via %s\n", contextPackNodeLabel(n, entry.docID), entry.kind))
			}
		}
		if len(entries) > limit {
			sb.WriteString(fmt.Sprintf("  - ... %d more impacted documents omitted\n", len(entries)-limit))
		}
	}
	sb.WriteString(fmt.Sprintf("- **Total impacted:** %d\n", total))
}

func (h *handler) contextPackImpactLevels(st *store.Store, startID string, depth int) map[int][]impactEntry {
	visited := map[string]bool{startID: true}
	queue := []string{startID}
	levels := make(map[int][]impactEntry)
	for level := 1; level <= depth && len(queue) > 0; level++ {
		var next []string
		for _, id := range queue {
			edges, err := st.GetIncomingEdges(id)
			if err != nil {
				continue
			}
			for _, edge := range edges {
				src := contextPackDocIDFromEdgeSource(st, edge.Source)
				if visited[src] {
					continue
				}
				visited[src] = true
				next = append(next, src)
				via := ""
				if level > 1 {
					via = id
				}
				levels[level] = append(levels[level], impactEntry{docID: src, kind: edge.Kind, via: via})
			}
		}
		queue = next
	}
	return levels
}

func contextPackDocID(node store.Node) string {
	if node.Kind == "document" {
		return node.ID
	}
	if node.FilePath != "" {
		return node.FilePath
	}
	return node.ID
}

func contextPackDocIDFromEdgeSource(st *store.Store, nodeID string) string {
	if st == nil {
		return nodeID
	}
	n, err := st.GetNodeByID(nodeID)
	if err != nil || n == nil {
		return nodeID
	}
	return contextPackDocID(*n)
}

func contextPackNodeLabel(node *store.Node, fallback string) string {
	if node == nil {
		return fallback
	}
	if node.FilePath == "" {
		return node.Name
	}
	return fmt.Sprintf("%s (%s)", node.Name, node.FilePath)
}

func contextPackLineSuffix(line int) string {
	if line <= 0 {
		return ""
	}
	return fmt.Sprintf(" [line %d]", line)
}

func limitEdges(edges []store.Edge, limit int) []store.Edge {
	if limit > 0 && len(edges) > limit {
		return edges[:limit]
	}
	return edges
}

func writeContextPackField(sb *strings.Builder, label, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	sb.WriteString(fmt.Sprintf("- **%s:** %s\n", label, value))
}

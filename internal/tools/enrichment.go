package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Detective-XH/docgraph/internal/callout"
	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultEnrichmentLimit = 20
	maxAgentMetadataFields = 50
)

var agentMetadataKeyRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

var enrichmentTool = mcp.NewTool("docgraph_enrichment",
	mcp.WithDescription("Agent-driven metadata enrichment facade. Requires --enable-enrichment server flag. Workflow: (1) action=pending — shows scope + cost + sensitivity, generates a CONFIRMATION_TOKEN; relay the output to the user and wait for consent. (2) action=process — requires confirmation_token from step 1; stores the inferred summary and metadata. DocGraph never calls an LLM itself."),
	mcp.WithString("action", mcp.Required(), mcp.Description("Action to run: pending or process")),
	mcp.WithString("confirmation_token", mcp.Description("Token from action=pending ACTION section; required for action=process")),
	mcp.WithNumber("limit", mcp.Description("pending: max documents to return (default 20)")),
	mcp.WithString("content_mode", mcp.Description("pending: 'full' (default) reads full document content from disk; 'excerpt' uses the stored body excerpt")),
	mcp.WithString("doc_id", mcp.Description("process: document ID from action=pending")),
	mcp.WithString("content_hash", mcp.Description("process: content_hash from action=pending")),
	mcp.WithString("summary", mcp.Description("Concise agent-inferred summary, max 4000 bytes")),
	mcp.WithString("metadata", mcp.Description("JSON object of inferred metadata fields. Values may be strings, numbers, booleans, or arrays of scalar values.")),
	mcp.WithNumber("confidence", mcp.Description("Optional confidence from 0.0 to 1.0 applied to all metadata fields")),
	mcp.WithString("model_id", mcp.Description("process: required model identifier that produced this enrichment, for example gpt-5.4 or claude-sonnet-4.6")),
	mcp.WithString("provider", mcp.Description("process: optional provider identifier, for example openai, anthropic, ollama")),
	mcp.WithString("agent_id", mcp.Description("process: optional calling agent or workflow identifier")),
)

func (h *handler) handleEnrichment(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	action := strings.ToLower(strings.TrimSpace(getStringArg(request.GetArguments(), "action", "")))
	switch action {
	case "pending":
		return h.handleEnrichmentPending(ctx, request)
	case "process":
		return h.handleEnrichmentProcess(ctx, request)
	default:
		return mcp.NewToolResultError("action parameter must be one of: pending, process"), nil
	}
}

func (h *handler) handleEnrichmentPending(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	limit := getIntArgClamped(args, "limit", defaultEnrichmentLimit, 1, maxListLimit)
	contentMode := getStringArg(args, "content_mode", "full")
	if contentMode != "full" && contentMode != "excerpt" {
		contentMode = "full"
	}

	type pendingResult struct {
		docs        []store.EnrichmentCandidate
		projectName string
		projectRoot string
	}

	var results []pendingResult
	if h.workspace != nil {
		for _, p := range h.workspace.Projects {
			docs, err := p.Store.GetPendingEnrichments(limit)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("get pending enrichment for %s: %v", p.Name, err)), nil
			}
			results = append(results, pendingResult{docs: docs, projectName: p.Name, projectRoot: p.Path})
		}
	} else {
		docs, err := h.store.GetPendingEnrichments(limit)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("get pending enrichment: %v", err)), nil
		}
		results = append(results, pendingResult{docs: docs, projectRoot: h.projectRoot})
	}

	// Build pending docs for impact graph.
	var pendingDocs []callout.PendingDoc
	for _, r := range results {
		for _, doc := range r.docs {
			pendingDocs = append(pendingDocs, callout.PendingDoc{
				FilePath:    doc.FilePath,
				BodyExcerpt: doc.BodyExcerpt,
			})
		}
	}

	// Generate token only when N>0 and not all-sensitive.
	paths := make([]string, len(pendingDocs))
	for i, d := range pendingDocs {
		paths[i] = d.FilePath
	}
	var token string
	if len(pendingDocs) > 0 && !callout.IsAllSensitive(paths) {
		// Collect doc_ids across all projects — the token authorizes exactly
		// the documents shown to the user in this pending call. process must
		// reject any doc_id not in this set.
		docIDs := make(map[string]struct{}, len(pendingDocs))
		for _, r := range results {
			for _, doc := range r.docs {
				docIDs[doc.DocID] = struct{}{}
			}
		}
		token = h.newConfirmationToken()
		h.enrichmentPendingTokens.Range(func(k, v any) bool {
			if v.(*pendingToken).expiresAt.Before(time.Now()) {
				h.enrichmentPendingTokens.Delete(k)
			}
			return true
		})
		h.enrichmentPendingTokens.Store(token, &pendingToken{
			expiresAt: time.Now().Add(30 * time.Minute),
			docIDs:    docIDs,
		})
	}

	workspaceDir := h.projectRoot
	if h.workspace != nil {
		workspaceDir = h.workspace.Root
	}

	graph := callout.BuildImpactGraph(pendingDocs, callout.ImpactOpts{
		ToolName:          "docgraph_enrichment",
		WorkspaceDir:      workspaceDir,
		Rates:             callout.DefaultRates(),
		ConfirmationToken: token,
	})

	// Append document list after impact graph for agent reference.
	if len(pendingDocs) == 0 {
		return mcp.NewToolResultText(graph), nil
	}

	var sb strings.Builder
	sb.WriteString(graph)
	sb.WriteString("\n## Document List\n\n")
	i := 0
	for _, r := range results {
		for _, doc := range r.docs {
			i++
			prefix := ""
			if r.projectName != "" {
				prefix = "[" + r.projectName + "] "
			}
			fmt.Fprintf(&sb, "### %d. %s%s\n", i, prefix, doc.Name)
			fmt.Fprintf(&sb, "- **doc_id:** `%s`\n", doc.DocID)
			fmt.Fprintf(&sb, "- **path:** %s\n", doc.FilePath)
			fmt.Fprintf(&sb, "- **content_hash:** `%s`\n", doc.ContentHash)
			sb.WriteString("- **frontmatter:** absent\n")

			content := doc.BodyExcerpt
			if contentMode == "full" && r.projectRoot != "" {
				if c, err := store.ReadSectionContent(doc.FilePath, doc.StartLine, doc.EndLine, r.projectRoot, 8000); err == nil {
					content = c
				}
			}
			if content != "" {
				sb.WriteString("- **content:**\n\n```text\n")
				sb.WriteString(content)
				sb.WriteString("\n```\n")
			}
			sb.WriteString("\n")
		}
	}
	return mcp.NewToolResultText(sb.String()), nil
}

func (h *handler) handleEnrichmentProcess(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	// Validate confirmation token. The token authorizes a batch of doc_ids
	// (bound at action=pending time) and is reusable for every doc in that
	// batch until the set is exhausted or the token expires. We use Load (not
	// LoadAndDelete) so a single user consent can drive a multi-doc batch.
	token := sanitizeArg(getStringArg(args, "confirmation_token", ""), 64)
	if token == "" {
		return mcp.NewToolResultError("confirmation_token required. Call action=pending first to review scope — the output includes the token and a pre-written user message."), nil
	}
	raw, loaded := h.enrichmentPendingTokens.Load(token)
	if !loaded {
		return mcp.NewToolResultError("Invalid confirmation_token. Call action=pending again to generate a new token."), nil
	}
	pt := raw.(*pendingToken)
	if pt.expiresAt.Before(time.Now()) {
		h.enrichmentPendingTokens.Delete(token)
		return mcp.NewToolResultError("Confirmation token expired (30-minute limit). Call action=pending again to review scope and get a new token."), nil
	}

	docID := sanitizeArg(getStringArg(args, "doc_id", ""), maxArgLength)
	if docID == "" {
		return mcp.NewToolResultError("doc_id parameter is required"), nil
	}
	contentHash := sanitizeArg(getStringArg(args, "content_hash", ""), maxArgLength)
	if contentHash == "" {
		return mcp.NewToolResultError("content_hash parameter is required"), nil
	}
	summary := sanitizeArg(getStringArg(args, "summary", ""), maxArgLength)
	metadataRaw := getStringArg(args, "metadata", "")
	if len(metadataRaw) > 1024*1024 {
		return mcp.NewToolResultError("metadata parameter exceeds 1 MB limit"), nil
	}
	modelID := sanitizeArg(getStringArg(args, "model_id", ""), 200)
	if modelID == "" {
		return mcp.NewToolResultError("model_id parameter is required"), nil
	}
	provider := sanitizeArg(getStringArg(args, "provider", ""), 100)
	agentID := sanitizeArg(getStringArg(args, "agent_id", ""), 200)

	confidence, err := getOptionalConfidence(args, "confidence")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	tuples, err := agentMetadataTuplesFromJSON(metadataRaw, confidence)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid metadata: %v", err)), nil
	}
	if summary == "" && len(tuples) == 0 {
		return mcp.NewToolResultError("summary or metadata parameter is required"), nil
	}

	targetStore := h.store
	if h.workspace != nil {
		targetStore = nil
		for _, p := range h.workspace.Projects {
			n, _ := p.Store.GetNodeByID(docID)
			if n != nil {
				targetStore = p.Store
				break
			}
		}
		if targetStore == nil {
			return mcp.NewToolResultError(fmt.Sprintf("doc_id not found in any project: %s", docID)), nil
		}
	}

	// Authorize doc_id against the batch the token was issued for, then store,
	// then consume the doc_id from the set. We hold pt.mu across the store
	// call so that membership check, store, and set mutation form one
	// critical section per token — the cost is that two processes against the
	// same token serialize on disk I/O, which is acceptable given MCP stdio
	// already serializes handler calls today.
	pt.mu.Lock()
	if _, ok := pt.docIDs[docID]; !ok {
		pt.mu.Unlock()
		return mcp.NewToolResultError(fmt.Sprintf(
			"doc_id %q was not in the authorized batch for this confirmation_token. Call action=pending again to authorize this document.",
			docID)), nil
	}
	err = targetStore.UpsertAgentEnrichment(store.AgentEnrichment{
		DocID:       docID,
		Summary:     summary,
		Provider:    provider,
		ModelID:     modelID,
		AgentID:     agentID,
		ContentHash: contentHash,
		Metadata:    tuples,
	})
	if err != nil {
		pt.mu.Unlock()
		return mcp.NewToolResultError(fmt.Sprintf("store enrichment: %v", err)), nil
	}
	delete(pt.docIDs, docID)
	batchExhausted := len(pt.docIDs) == 0
	pt.mu.Unlock()
	if batchExhausted {
		h.enrichmentPendingTokens.Delete(token)
	}

	return mcp.NewToolResultText(fmt.Sprintf(
		"Stored current agent enrichment for doc %q: summary=%t, metadata_fields=%d, source=agent_inferred, authority=advisory, model_id=%q.",
		docID, summary != "", len(tuples), modelID)), nil
}

func getOptionalConfidence(args map[string]any, key string) (*float64, error) {
	v, ok := args[key]
	if !ok || v == nil {
		return nil, nil
	}
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case int:
		f = float64(n)
	default:
		return nil, fmt.Errorf("%s must be a number", key)
	}
	if f < 0 || f > 1 {
		return nil, fmt.Errorf("%s must be between 0.0 and 1.0", key)
	}
	return &f, nil
}

func agentMetadataTuplesFromJSON(raw string, confidence *float64) ([]store.MetadataTuple, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}
	if len(obj) > maxAgentMetadataFields {
		return nil, fmt.Errorf("field count %d exceeds cap of %d", len(obj), maxAgentMetadataFields)
	}
	out := make([]store.MetadataTuple, 0, len(obj))
	for key, value := range obj {
		if !agentMetadataKeyRe.MatchString(key) {
			return nil, fmt.Errorf("invalid key %q", key)
		}
		tuple, ok, err := agentMetadataTuple(key, value, confidence)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, tuple)
		}
	}
	return out, nil
}

func agentMetadataTuple(key string, value any, confidence *float64) (store.MetadataTuple, bool, error) {
	tuple := store.MetadataTuple{Key: key, Source: "agent_inferred", Confidence: confidence}
	switch v := value.(type) {
	case nil:
		return tuple, false, nil
	case string:
		tuple.Value = truncateBytes(v, 2000)
		tuple.ValueType = inferAgentStringValueType(tuple.Value)
	case bool:
		tuple.Value = strconv.FormatBool(v)
		tuple.ValueType = "bool"
	case json.Number:
		tuple.Value = v.String()
		if _, err := v.Float64(); err != nil {
			return tuple, false, fmt.Errorf("field %q has invalid number", key)
		}
		tuple.ValueType = "number"
	case []any:
		items := make([]string, 0, len(v))
		for _, item := range v {
			switch x := item.(type) {
			case string:
				items = append(items, x)
			case bool:
				items = append(items, strconv.FormatBool(x))
			case json.Number:
				if _, err := x.Float64(); err != nil {
					return tuple, false, fmt.Errorf("field %q has invalid number in list", key)
				}
				items = append(items, x.String())
			default:
				return tuple, false, fmt.Errorf("field %q list contains unsupported value", key)
			}
		}
		encoded, _ := json.Marshal(items)
		tuple.Value = truncateBytes(string(encoded), 2000)
		tuple.ValueType = "list"
	default:
		return tuple, false, fmt.Errorf("field %q has unsupported object value", key)
	}
	return tuple, true, nil
}

func inferAgentStringValueType(s string) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) >= len("2006-01-02") {
		if _, err := time.Parse("2006-01-02", trimmed[:10]); err == nil && len(trimmed) == 10 {
			return "date"
		}
	}
	if strings.HasPrefix(trimmed, "[[") && strings.HasSuffix(trimmed, "]]") {
		return "ref"
	}
	if _, err := strconv.ParseFloat(trimmed, 64); err == nil && trimmed != "" {
		return "number"
	}
	return "string"
}

func truncateBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func appendAISummarySection(summary *store.AISummary) string {
	if summary == nil || summary.Summary == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n### Agent-Inferred Summary\n")
	for line := range strings.SplitSeq(strings.TrimRight(summary.Summary, "\n"), "\n") {
		fmt.Fprintf(&sb, "> %s\n", line)
	}
	sb.WriteString("**Source:** agent_inferred | **Authority:** advisory, non-authoritative")
	if summary.ModelID != "" {
		fmt.Fprintf(&sb, " | **Model:** %s", summary.ModelID)
	}
	if summary.AgentID != "" {
		fmt.Fprintf(&sb, " | **Agent:** %s", summary.AgentID)
	}
	if summary.RunID != "" {
		fmt.Fprintf(&sb, " | **Run:** `%s`", summary.RunID)
	}
	if summary.ContentHash != "" {
		fmt.Fprintf(&sb, " | **Content hash:** `%s`", summary.ContentHash)
	}
	sb.WriteString("\n")
	return sb.String()
}

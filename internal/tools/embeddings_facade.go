package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Detective-XH/docgraph/internal/callout"
	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var embeddingsTool = mcp.NewTool("docgraph_embeddings",
	mcp.WithDescription("Neural embedding workflow facade. Requires --enable-embeddings server flag. Actions: pending, store, clear. Workflow: (1) action=pending — shows scope + cost + sensitivity, generates a CONFIRMATION_TOKEN; relay the output to the user and wait for consent. (2) action=store — requires confirmation_token from step 1; stores one vector and recomputes neural similarity. (3) action=clear — removes all vectors for a model. DocGraph never calls an LLM itself. Different model_id values are partitioned and never compared."),
	mcp.WithString("action", mcp.Required(), mcp.Description("Embedding action: pending, store, or clear")),
	mcp.WithString("model_id", mcp.Description("Embedding model identifier, e.g. 'text-embedding-3-small'")),
	mcp.WithString("doc_id", mcp.Description("Document ID from action=pending; required for action=store")),
	mcp.WithString("vector", mcp.Description("JSON array of float64 values for action=store, e.g. \"[0.12, -0.34]\"")),
	mcp.WithString("content_hash", mcp.Description("content_hash from action=pending; required for action=store")),
	mcp.WithString("confirmation_token", mcp.Description("Token from action=pending ACTION section; required for action=store")),
	mcp.WithNumber("limit", mcp.Description("Max documents to return for action=pending (default 50)")),
	mcp.WithString("content_mode", mcp.Description("For action=pending: 'full' (default) reads full section from disk; 'excerpt' uses the stored body excerpt")),
)

func registerEmbeddingsFacadeTool(s *server.MCPServer, h *handler) {
	s.AddTool(embeddingsTool, h.guardIndexing(h.handleEmbeddingsFacade))
}

func (h *handler) handleEmbeddingsFacade(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	action := strings.ToLower(strings.TrimSpace(sanitizeArg(getStringArg(args, "action", ""), 100)))

	switch action {
	case "pending":
		return h.handleEmbeddingsFacadePending(args)
	case "store":
		return h.handleEmbeddingsFacadeStore(args)
	case "clear":
		return h.handleEmbeddingsFacadeClear(args)
	default:
		return mcp.NewToolResultError("action parameter must be one of: pending, store, clear"), nil
	}
}

func (h *handler) handleEmbeddingsFacadePending(args map[string]any) (*mcp.CallToolResult, error) {
	modelID := sanitizeArg(getStringArg(args, "model_id", ""), maxArgLength)
	if modelID == "" {
		return mcp.NewToolResultError("model_id parameter is required"), nil
	}
	contentMode := getStringArg(args, "content_mode", "full")
	if contentMode != "full" && contentMode != "excerpt" {
		return mcp.NewToolResultError("content_mode parameter must be either full or excerpt"), nil
	}

	// Collect pending docs for impact graph.
	var rawDocs []store.PendingDoc
	if h.workspace != nil {
		for _, p := range h.workspace.Projects {
			docs, err := p.Store.GetPendingEmbeddings(modelID, getIntArg(args, "limit", 50))
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("get pending for %s: %v", p.Name, err)), nil
			}
			rawDocs = append(rawDocs, docs...)
		}
	} else {
		docs, err := h.store.GetPendingEmbeddings(modelID, getIntArg(args, "limit", 50))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("get pending embeddings: %v", err)), nil
		}
		rawDocs = append(rawDocs, docs...)
	}

	pendingDocs := make([]callout.PendingDoc, len(rawDocs))
	for i, d := range rawDocs {
		pendingDocs[i] = callout.PendingDoc{FilePath: d.FilePath, BodyExcerpt: d.BodyExcerpt}
	}

	// Determine token: generate only when N>0 and not all-sensitive.
	paths := make([]string, len(pendingDocs))
	for i, d := range pendingDocs {
		paths[i] = d.FilePath
	}
	var token string
	if len(pendingDocs) > 0 && !callout.IsAllSensitive(paths) {
		token = h.newConfirmationToken()
		// Sweep expired tokens before inserting.
		h.embeddingsPendingTokens.Range(func(k, v any) bool {
			if v.(pendingToken).expiresAt.Before(time.Now()) {
				h.embeddingsPendingTokens.Delete(k)
			}
			return true
		})
		h.embeddingsPendingTokens.Store(token, pendingToken{expiresAt: time.Now().Add(30 * time.Minute)})
	}

	workspaceDir := h.projectRoot
	if h.workspace != nil {
		workspaceDir = h.workspace.Root
	}

	graph := callout.BuildImpactGraph(pendingDocs, callout.ImpactOpts{
		ToolName:          "docgraph_embeddings",
		ModelHint:         modelID,
		WorkspaceDir:      workspaceDir,
		Rates:             callout.DefaultRates(),
		ConfirmationToken: token,
	})
	return mcp.NewToolResultText(graph), nil
}

func (h *handler) handleEmbeddingsFacadeStore(args map[string]any) (*mcp.CallToolResult, error) {
	// Validate confirmation token (single-use via LoadAndDelete).
	token := sanitizeArg(getStringArg(args, "confirmation_token", ""), 64)
	if token == "" {
		return mcp.NewToolResultError("confirmation_token required. Call action=pending first to review scope — the output includes the token and a pre-written user message."), nil
	}
	raw, loaded := h.embeddingsPendingTokens.LoadAndDelete(token)
	if !loaded {
		return mcp.NewToolResultError("Invalid confirmation_token. Call action=pending again to generate a new token."), nil
	}
	if raw.(pendingToken).expiresAt.Before(time.Now()) {
		return mcp.NewToolResultError("Confirmation token expired (30-minute limit). Call action=pending again to review scope and get a new token."), nil
	}

	docID := sanitizeArg(getStringArg(args, "doc_id", ""), maxArgLength)
	if docID == "" {
		return mcp.NewToolResultError("doc_id parameter is required"), nil
	}
	modelID := sanitizeArg(getStringArg(args, "model_id", ""), maxArgLength)
	if modelID == "" {
		return mcp.NewToolResultError("model_id parameter is required"), nil
	}
	vectorStr := getStringArg(args, "vector", "")
	if vectorStr == "" {
		return mcp.NewToolResultError("vector parameter is required"), nil
	}
	if len(vectorStr) > 2*1024*1024 {
		return mcp.NewToolResultError("vector parameter exceeds 2 MB limit"), nil
	}
	contentHash := sanitizeArg(getStringArg(args, "content_hash", ""), maxArgLength)
	if contentHash == "" {
		return mcp.NewToolResultError("content_hash parameter is required"), nil
	}
	return h.storeEmbedding(docID, modelID, vectorStr, contentHash)
}

func (h *handler) handleEmbeddingsFacadeClear(args map[string]any) (*mcp.CallToolResult, error) {
	if err := rejectNonEmptyEmbeddingClearArgs(args); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	modelID := sanitizeArg(getStringArg(args, "model_id", ""), maxArgLength)
	if modelID == "" {
		return mcp.NewToolResultError("model_id parameter is required"), nil
	}
	return h.clearEmbeddings(modelID)
}

func rejectNonEmptyEmbeddingClearArgs(args map[string]any) error {
	for _, key := range []string{"doc_id", "vector", "content_hash", "content_mode"} {
		if strings.TrimSpace(getStringArg(args, key, "")) != "" {
			return fmt.Errorf("%s parameter is not valid for action=clear", key)
		}
	}
	if v, ok := args["limit"]; ok && v != nil && getIntArg(args, "limit", 0) != 0 {
		return fmt.Errorf("limit parameter is not valid for action=clear")
	}
	return nil
}

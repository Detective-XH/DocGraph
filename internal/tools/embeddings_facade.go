package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var embeddingsTool = mcp.NewTool("docgraph_embeddings",
	mcp.WithDescription("Neural embedding workflow facade. Actions: pending, store, clear. PRIVACY: action=pending returns document content that may be sent to an external embedding provider; only proceed with user consent. Pull-then-push workflow: pending returns doc_id/content_hash/content, your agent computes vectors with its provider, store saves one vector and recomputes neural similarity, clear deletes one model's vectors. DocGraph never calls an LLM itself. Different model_id values are partitioned and never compared with each other."),
	mcp.WithString("action", mcp.Required(), mcp.Description("Embedding action: pending, store, or clear")),
	mcp.WithString("model_id", mcp.Description("Embedding model identifier, e.g. 'text-embedding-3-small'")),
	mcp.WithString("doc_id", mcp.Description("Document ID from action=pending; required for action=store")),
	mcp.WithString("vector", mcp.Description("JSON array of float64 values for action=store, e.g. \"[0.12, -0.34]\"")),
	mcp.WithString("content_hash", mcp.Description("content_hash from action=pending; required for action=store")),
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
	return h.renderEmbeddingsPending(modelID, getIntArg(args, "limit", 50), contentMode)
}

func (h *handler) handleEmbeddingsFacadeStore(args map[string]any) (*mcp.CallToolResult, error) {
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

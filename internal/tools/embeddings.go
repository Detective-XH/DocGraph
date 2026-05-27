package tools

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

// ---------------------------------------------------------------------------
// Tool definitions
// ---------------------------------------------------------------------------

var embeddingsPendingTool = mcp.NewTool("docgraph_embeddings_pending",
	mcp.WithDescription("Return documents that need neural embeddings (missing or stale for model_id). PRIVACY: content will be sent to an external LLM provider — only proceed with user consent. Workflow: (1) call this with model_id to get pending docs + content_hash; (2) generate vectors via your LLM provider; (3) call docgraph_embeddings_store with vector + content_hash; (4) docgraph_similar returns neural results (engine: neural) alongside TF-IDF; (5) call docgraph_embeddings_clear to remove a model. DocGraph never calls an LLM itself. model_id is arbitrary ('text-embedding-3-small', 'nomic-embed-text', Ollama, etc). Different model_id vectors are never compared with each other."),
	mcp.WithString("model_id", mcp.Required(), mcp.Description("Embedding model identifier, e.g. 'text-embedding-3-small'")),
	mcp.WithNumber("limit", mcp.Description("Max documents to return (default 50)")),
	mcp.WithString("content_mode", mcp.Description("'full' (default) reads full section from disk; 'excerpt' uses the stored body excerpt")),
)

var embeddingsStoreTool = mcp.NewTool("docgraph_embeddings_store",
	mcp.WithDescription("Store a neural embedding vector for a document and recompute neural similarity edges. Call after computing embeddings via your LLM provider. Pass the content_hash exactly as returned by docgraph_embeddings_pending."),
	mcp.WithString("doc_id", mcp.Required(), mcp.Description("Document ID from docgraph_embeddings_pending")),
	mcp.WithString("model_id", mcp.Required(), mcp.Description("Embedding model identifier")),
	mcp.WithString("vector", mcp.Required(), mcp.Description("JSON array of float64 values, e.g. \"[0.12, -0.34, ...]\" ")),
	mcp.WithString("content_hash", mcp.Required(), mcp.Description("content_hash from docgraph_embeddings_pending")),
)

var embeddingsClearTool = mcp.NewTool("docgraph_embeddings_clear",
	mcp.WithDescription("Delete all stored embeddings for a model_id and their associated neural similarity edges. Use when switching embedding models to reclaim space."),
	mcp.WithString("model_id", mcp.Required(), mcp.Description("Embedding model identifier to clear")),
)

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (h *handler) handleEmbeddingsPending(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	modelID := getStringArg(args, "model_id", "")
	if modelID == "" {
		return mcp.NewToolResultError("model_id parameter is required"), nil
	}
	modelID = sanitizeArg(modelID, maxArgLength)
	limit := getIntArg(args, "limit", 50)
	contentMode := getStringArg(args, "content_mode", "full")
	if contentMode != "full" && contentMode != "excerpt" {
		contentMode = "full"
	}
	return h.renderEmbeddingsPending(modelID, limit, contentMode)
}

func (h *handler) handleEmbeddingsStore(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	docID := getStringArg(args, "doc_id", "")
	if docID == "" {
		return mcp.NewToolResultError("doc_id parameter is required"), nil
	}
	modelID := getStringArg(args, "model_id", "")
	if modelID == "" {
		return mcp.NewToolResultError("model_id parameter is required"), nil
	}
	vectorStr := getStringArg(args, "vector", "")
	if vectorStr == "" {
		return mcp.NewToolResultError("vector parameter is required"), nil
	}
	contentHash := getStringArg(args, "content_hash", "")
	if contentHash == "" {
		return mcp.NewToolResultError("content_hash parameter is required"), nil
	}

	docID = sanitizeArg(docID, maxArgLength)
	modelID = sanitizeArg(modelID, maxArgLength)
	contentHash = sanitizeArg(contentHash, maxArgLength)
	return h.storeEmbedding(docID, modelID, vectorStr, contentHash)
}

func (h *handler) handleEmbeddingsClear(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	modelID := getStringArg(args, "model_id", "")
	if modelID == "" {
		return mcp.NewToolResultError("model_id parameter is required"), nil
	}
	modelID = sanitizeArg(modelID, maxArgLength)
	return h.clearEmbeddings(modelID)
}

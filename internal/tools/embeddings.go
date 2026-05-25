package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Detective-XH/docgraph/internal/similarity"
	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

// ---------------------------------------------------------------------------
// Tool definitions
// ---------------------------------------------------------------------------

var embeddingsPendingTool = mcp.NewTool("docgraph_embeddings_pending",
	mcp.WithDescription("Return documents that need neural embeddings: either no embedding exists for model_id, or the stored content has changed since last embedding. PRIVACY: document content returned by this tool will be sent to an external LLM embedding provider — only proceed with user consent."),
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

	type pendingResult struct {
		docs        []store.PendingDoc
		projectName string
		projectRoot string
	}

	var results []pendingResult

	if h.workspace != nil {
		for _, p := range h.workspace.Projects {
			docs, err := p.Store.GetPendingEmbeddings(modelID, limit)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("get pending for %s: %v", p.Name, err)), nil
			}
			results = append(results, pendingResult{docs: docs, projectName: p.Name, projectRoot: p.Path})
		}
	} else {
		docs, err := h.store.GetPendingEmbeddings(modelID, limit)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("get pending embeddings: %v", err)), nil
		}
		results = append(results, pendingResult{docs: docs, projectRoot: h.projectRoot})
	}

	var sb strings.Builder
	total := 0
	for _, r := range results {
		total += len(r.docs)
	}

	sb.WriteString(fmt.Sprintf("## Pending Embeddings for model %q\n\nFound %d documents needing embeddings.\n", modelID, total))
	if total == 0 {
		return mcp.NewToolResultText(sb.String()), nil
	}

	sb.WriteString("\n⚠️  PRIVACY: the content below will be sent to your external embedding provider.\n\n")

	i := 0
	for _, r := range results {
		for _, doc := range r.docs {
			i++
			prefix := ""
			if r.projectName != "" {
				prefix = "[" + r.projectName + "] "
			}
			sb.WriteString(fmt.Sprintf("### %d. %s%s\n", i, prefix, doc.Name))
			sb.WriteString(fmt.Sprintf("- **doc_id:** `%s`\n", doc.DocID))
			sb.WriteString(fmt.Sprintf("- **path:** %s\n", doc.FilePath))
			sb.WriteString(fmt.Sprintf("- **content_hash:** `%s`\n", doc.ContentHash))

			var content string
			if contentMode == "full" && r.projectRoot != "" {
				c, err := store.ReadSectionContent(doc.FilePath, doc.StartLine, doc.EndLine, r.projectRoot, 8000)
				if err == nil {
					content = c
				} else {
					content = doc.BodyExcerpt
				}
			} else {
				content = doc.BodyExcerpt
			}

			if content != "" {
				sb.WriteString("- **content:**\n\n```\n")
				sb.WriteString(content)
				sb.WriteString("\n```\n")
			}
			sb.WriteString("\n")
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
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

	// Parse vector JSON.
	var vec []float64
	if err := json.Unmarshal([]byte(vectorStr), &vec); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid vector JSON: %v", err)), nil
	}
	if len(vec) == 0 {
		return mcp.NewToolResultError("vector must not be empty"), nil
	}

	emb := store.Embedding{
		DocID:       docID,
		ModelID:     modelID,
		Dim:         len(vec),
		Vector:      vec,
		ContentHash: contentHash,
	}

	// Find the right store (workspace or single).
	var targetStore *store.Store
	if h.workspace != nil {
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
	} else {
		targetStore = h.store
	}

	if err := targetStore.UpsertEmbedding(emb); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("store embedding: %v", err)), nil
	}

	if err := similarity.ComputeNeuralSimilarityForDoc(targetStore, docID, modelID, 0); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("compute neural similarity: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Stored embedding for doc %q (model: %s, dim: %d). Neural similarity recomputed.", docID, modelID, len(vec))), nil
}

func (h *handler) handleEmbeddingsClear(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	modelID := getStringArg(args, "model_id", "")
	if modelID == "" {
		return mcp.NewToolResultError("model_id parameter is required"), nil
	}
	modelID = sanitizeArg(modelID, maxArgLength)

	type clearResult struct {
		projectName string
		embDeleted  int64
		edgeDeleted int64
	}

	var results []clearResult

	if h.workspace != nil {
		for _, p := range h.workspace.Projects {
			embN, err := p.Store.DeleteEmbeddingsByModel(modelID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("delete embeddings for %s: %v", p.Name, err)), nil
			}
			edgeN, err := p.Store.DeleteNeuralSimilarityEdgesByModel(modelID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("delete edges for %s: %v", p.Name, err)), nil
			}
			results = append(results, clearResult{projectName: p.Name, embDeleted: embN, edgeDeleted: edgeN})
		}
	} else {
		embN, err := h.store.DeleteEmbeddingsByModel(modelID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("delete embeddings: %v", err)), nil
		}
		edgeN, err := h.store.DeleteNeuralSimilarityEdgesByModel(modelID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("delete edges: %v", err)), nil
		}
		results = append(results, clearResult{embDeleted: embN, edgeDeleted: edgeN})
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Cleared embeddings for model %q\n\n", modelID))
	var totalEmb, totalEdge int64
	for _, r := range results {
		totalEmb += r.embDeleted
		totalEdge += r.edgeDeleted
		if r.projectName != "" {
			sb.WriteString(fmt.Sprintf("- **%s**: %d embeddings, %d neural edges deleted\n", r.projectName, r.embDeleted, r.edgeDeleted))
		}
	}
	if len(results) == 1 && results[0].projectName == "" {
		sb.WriteString(fmt.Sprintf("Deleted %d embeddings and %d neural similarity edges.\n", totalEmb, totalEdge))
	} else {
		sb.WriteString(fmt.Sprintf("\n**Total:** %d embeddings, %d neural edges deleted.\n", totalEmb, totalEdge))
	}
	return mcp.NewToolResultText(sb.String()), nil
}

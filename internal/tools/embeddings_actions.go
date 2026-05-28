package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Detective-XH/docgraph/internal/similarity"
	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

func (h *handler) storeEmbedding(docID string, modelID string, vectorStr string, contentHash string) (*mcp.CallToolResult, error) {
	// Vectors cross a tool boundary as JSON strings so deterministic clients and
	// agents can preserve the exact schema across MCP transports.
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

	if err := targetStore.UpsertEmbedding(emb); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("store embedding: %v", err)), nil
	}

	if err := similarity.ComputeNeuralSimilarityForDoc(targetStore, docID, modelID, 0); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("compute neural similarity: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Stored embedding for doc %q (model: %s, dim: %d). Neural similarity recomputed.", docID, modelID, len(vec))), nil
}

type clearEmbeddingsResult struct {
	projectName string
	embDeleted  int64
	edgeDeleted int64
}

func (h *handler) clearEmbeddings(modelID string) (*mcp.CallToolResult, error) {
	var results []clearEmbeddingsResult

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
			results = append(results, clearEmbeddingsResult{projectName: p.Name, embDeleted: embN, edgeDeleted: edgeN})
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
		results = append(results, clearEmbeddingsResult{embDeleted: embN, edgeDeleted: edgeN})
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Cleared embeddings for model %q\n\n", modelID)
	var totalEmb, totalEdge int64
	for _, r := range results {
		totalEmb += r.embDeleted
		totalEdge += r.edgeDeleted
		if r.projectName != "" {
			fmt.Fprintf(&sb, "- **%s**: %d embeddings, %d neural edges deleted\n", r.projectName, r.embDeleted, r.edgeDeleted)
		}
	}
	if len(results) == 1 && results[0].projectName == "" {
		fmt.Fprintf(&sb, "Deleted %d embeddings and %d neural similarity edges.\n", totalEmb, totalEdge)
	} else {
		fmt.Fprintf(&sb, "\n**Total:** %d embeddings, %d neural edges deleted.\n", totalEmb, totalEdge)
	}
	return mcp.NewToolResultText(sb.String()), nil
}

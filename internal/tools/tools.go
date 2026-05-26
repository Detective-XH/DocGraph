package tools

import (
	"context"
	"sync/atomic"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/Detective-XH/docgraph/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Register registers all tools and returns a func(bool) to set the indexing
// flag. Callers should pass true before a cold-start background index and
// false (via defer) when it finishes.
func Register(s *server.MCPServer, st *store.Store, projectRoot string) func(bool) {
	h := &handler{store: st, projectRoot: projectRoot}
	registerTools(s, h)
	return h.indexing.Store
}

// RegisterWorkspace registers all tools for a workspace and returns the same
// indexing-flag setter.
func RegisterWorkspace(s *server.MCPServer, w *workspace.Workspace) func(bool) {
	h := &handler{workspace: w}
	registerTools(s, h)
	return h.indexing.Store
}

func registerTools(s *server.MCPServer, h *handler) {
	g := h.guardIndexing
	s.AddTool(searchTool, g(h.handleSearch))
	s.AddTool(filesTool, g(h.handleFiles))
	s.AddTool(statusTool, h.handleStatus) // not guarded — only diagnostic during cold start
	s.AddTool(referencesTool, g(h.handleReferences))
	s.AddTool(linksTool, g(h.handleLinks))
	s.AddTool(contextTool, g(h.handleContext))
	s.AddTool(nodeTool, g(h.handleNode))
	s.AddTool(exploreTool, g(h.handleExplore))
	s.AddTool(impactTool, g(h.handleImpact))
	s.AddTool(traceTool, g(h.handleTrace))
	s.AddTool(similarTool, g(h.handleSimilar))
	s.AddTool(tagsTool, g(h.handleTags))
	s.AddTool(historyTool, g(h.handleHistory))
	s.AddTool(embeddingsPendingTool, g(h.handleEmbeddingsPending))
	s.AddTool(embeddingsStoreTool, g(h.handleEmbeddingsStore))
	s.AddTool(embeddingsClearTool, g(h.handleEmbeddingsClear))
}

type handler struct {
	store       *store.Store
	workspace   *workspace.Workspace
	projectRoot string
	indexing    atomic.Bool
}

func (h *handler) guardIndexing(fn server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if h.indexing.Load() {
			return mcp.NewToolResultText("Indexing in progress — please retry in a moment."), nil
		}
		return fn(ctx, req)
	}
}

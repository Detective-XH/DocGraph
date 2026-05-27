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
	return RegisterWithProfile(s, st, projectRoot, ToolProfileFull)
}

// RegisterWithProfile registers the MCP tools selected by profile and returns
// a func(bool) to set the indexing flag.
func RegisterWithProfile(s *server.MCPServer, st *store.Store, projectRoot string, profile ToolProfile) func(bool) {
	h := &handler{store: st, projectRoot: projectRoot}
	registerTools(s, h, profile)
	return h.indexing.Store
}

// RegisterWorkspace registers all tools for a workspace and returns the same
// indexing-flag setter.
func RegisterWorkspace(s *server.MCPServer, w *workspace.Workspace) func(bool) {
	return RegisterWorkspaceWithProfile(s, w, ToolProfileFull)
}

// RegisterWorkspaceWithProfile registers the MCP tools selected by profile for
// a workspace and returns the same indexing-flag setter.
func RegisterWorkspaceWithProfile(s *server.MCPServer, w *workspace.Workspace, profile ToolProfile) func(bool) {
	h := &handler{workspace: w}
	registerTools(s, h, profile)
	return h.indexing.Store
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

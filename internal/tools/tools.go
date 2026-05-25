package tools

import (
	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/Detective-XH/docgraph/internal/workspace"
	"github.com/mark3labs/mcp-go/server"
)

func Register(s *server.MCPServer, st *store.Store, projectRoot string) {
	h := &handler{store: st, projectRoot: projectRoot}
	registerTools(s, h)
}

func RegisterWorkspace(s *server.MCPServer, w *workspace.Workspace) {
	h := &handler{workspace: w}
	registerTools(s, h)
}

func registerTools(s *server.MCPServer, h *handler) {
	s.AddTool(searchTool, h.handleSearch)
	s.AddTool(filesTool, h.handleFiles)
	s.AddTool(statusTool, h.handleStatus)
	s.AddTool(referencesTool, h.handleReferences)
	s.AddTool(linksTool, h.handleLinks)
	s.AddTool(contextTool, h.handleContext)
	s.AddTool(nodeTool, h.handleNode)
	s.AddTool(exploreTool, h.handleExplore)
	s.AddTool(impactTool, h.handleImpact)
	s.AddTool(traceTool, h.handleTrace)
	s.AddTool(similarTool, h.handleSimilar)
}

type handler struct {
	store       *store.Store
	workspace   *workspace.Workspace
	projectRoot string
}

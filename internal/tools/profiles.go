package tools

import (
	"github.com/mark3labs/mcp-go/server"
)

func registerTools(s *server.MCPServer, h *handler, opts RegisterOpts) {
	registerCompactTools(s, h, opts)
}

func registerCompactTools(s *server.MCPServer, h *handler, opts RegisterOpts) {
	g := h.guardIndexing
	s.AddTool(searchTool, g(h.handleSearch))
	s.AddTool(filesTool, g(h.handleFiles))
	s.AddTool(statusTool, h.handleStatus) // not guarded: status remains available during cold start.
	s.AddTool(contextTool, g(h.handleContext))
	s.AddTool(nodeTool, g(h.handleNode))
	s.AddTool(exploreTool, g(h.handleExplore))
	s.AddTool(similarTool, g(h.handleSimilar))
	s.AddTool(tagsTool, g(h.handleTags))
	s.AddTool(historyTool, g(h.handleHistory))
	registerGraphFacadeTool(s, h)
	if opts.EnableEmbeddings {
		registerEmbeddingsFacadeTool(s, h)
	}
	if opts.EnableEnrichment {
		s.AddTool(enrichmentTool, g(h.handleEnrichment))
	}
}

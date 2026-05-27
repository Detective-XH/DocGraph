package tools

import (
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/server"
)

// ToolProfile selects the MCP tool surface exposed to an agent.
type ToolProfile string

const (
	ToolProfileCompact ToolProfile = "compact"
)

// ParseToolProfile normalizes user input to ToolProfileCompact.
func ParseToolProfile(raw string) (ToolProfile, error) {
	switch ToolProfile(strings.ToLower(strings.TrimSpace(raw))) {
	case "", "compact":
		return ToolProfileCompact, nil
	case "full", "dual":
		fmt.Fprintf(os.Stderr, "warning: --tool-profile %q is deprecated; using compact\n", raw)
		return ToolProfileCompact, nil
	default:
		return "", fmt.Errorf("invalid tool profile %q: valid profile is compact", raw)
	}
}

func registerTools(s *server.MCPServer, h *handler, profile ToolProfile) {
	registerCompactTools(s, h)
}

func registerFullTools(s *server.MCPServer, h *handler) {
	g := h.guardIndexing
	s.AddTool(searchTool, g(h.handleSearch))
	s.AddTool(filesTool, g(h.handleFiles))
	s.AddTool(statusTool, h.handleStatus) // not guarded: status remains available during cold start.
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
	s.AddTool(enrichmentTool, g(h.handleEnrichment))
}

func registerCompactTools(s *server.MCPServer, h *handler) {
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
	registerEmbeddingsFacadeTool(s, h)
	s.AddTool(enrichmentTool, g(h.handleEnrichment))
	registerGraphFacadeTool(s, h)
}

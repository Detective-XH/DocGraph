package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/Detective-XH/docgraph/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Register registers all tools and returns a func(bool) to set the indexing
// flag. Callers should pass true before a cold-start background index and
// false (via defer) when it finishes.
func Register(s *server.MCPServer, st *store.Store, projectRoot string) func(bool) {
	return RegisterWithProfile(s, st, projectRoot, ToolProfileCompact)
}

// RegisterOpts configures which opt-in LLM callout tools are registered.
// Both default to false — tools are not registered unless explicitly enabled.
type RegisterOpts struct {
	EnableEmbeddings bool
	EnableEnrichment bool
}

// RegisterWithProfile registers the MCP tools selected by profile and returns
// a func(bool) to set the indexing flag.
func RegisterWithProfile(s *server.MCPServer, st *store.Store, projectRoot string, profile ToolProfile) func(bool) {
	return RegisterWithProfileOpts(s, st, projectRoot, profile, RegisterOpts{})
}

// RegisterWithProfileOpts is like RegisterWithProfile but accepts opt-in tool flags.
func RegisterWithProfileOpts(s *server.MCPServer, st *store.Store, projectRoot string, profile ToolProfile, opts RegisterOpts) func(bool) {
	h := &handler{
		store:            st,
		projectRoot:      projectRoot,
		enableEmbeddings: opts.EnableEmbeddings,
		enableEnrichment: opts.EnableEnrichment,
	}
	registerTools(s, h, profile, opts)
	return h.indexing.Store
}

// RegisterWorkspace registers all tools for a workspace and returns the same
// indexing-flag setter.
func RegisterWorkspace(s *server.MCPServer, w *workspace.Workspace) func(bool) {
	return RegisterWorkspaceWithProfile(s, w, ToolProfileCompact)
}

// RegisterWorkspaceWithProfile registers the MCP tools selected by profile for
// a workspace and returns the same indexing-flag setter.
func RegisterWorkspaceWithProfile(s *server.MCPServer, w *workspace.Workspace, profile ToolProfile) func(bool) {
	return RegisterWorkspaceWithProfileOpts(s, w, profile, RegisterOpts{})
}

// RegisterWorkspaceWithProfileOpts is like RegisterWorkspaceWithProfile but accepts opt-in tool flags.
func RegisterWorkspaceWithProfileOpts(s *server.MCPServer, w *workspace.Workspace, profile ToolProfile, opts RegisterOpts) func(bool) {
	h := &handler{
		workspace:        w,
		enableEmbeddings: opts.EnableEmbeddings,
		enableEnrichment: opts.EnableEnrichment,
	}
	registerTools(s, h, profile, opts)
	return h.indexing.Store
}

// pendingToken holds an expiring confirmation token for the two-step LLM callout workflow.
type pendingToken struct {
	expiresAt time.Time
}

type handler struct {
	store       *store.Store
	workspace   *workspace.Workspace
	projectRoot string
	indexing    atomic.Bool

	enableEmbeddings bool
	enableEnrichment bool

	// Separate maps prevent cross-tool token reuse — a shared map would allow a token from one tool's pending to authorize the other's processing step.
	embeddingsPendingTokens sync.Map // map[string]pendingToken
	enrichmentPendingTokens sync.Map // map[string]pendingToken
}

// newConfirmationToken generates a single-use 32-char hex token via crypto/rand.
func (h *handler) newConfirmationToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *handler) guardIndexing(fn server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if h.indexing.Load() {
			return mcp.NewToolResultText("Indexing in progress — please retry in a moment."), nil
		}
		return fn(ctx, req)
	}
}

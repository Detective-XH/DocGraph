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
	return RegisterWithOpts(s, st, projectRoot, RegisterOpts{})
}

// RegisterOpts configures which opt-in LLM callout tools are registered.
// EnableEmbeddings/EnableEnrichment default to false — tools are not registered
// unless explicitly enabled. NoGitignore mirrors the serve flag so docgraph_status
// can report which ignore sources are active.
type RegisterOpts struct {
	EnableEmbeddings bool
	EnableEnrichment bool
	NoGitignore      bool
}

// RegisterWithOpts registers the MCP tools with opt-in LLM callout flags and
// returns a func(bool) to set the indexing flag.
func RegisterWithOpts(s *server.MCPServer, st *store.Store, projectRoot string, opts RegisterOpts) func(bool) {
	h := &handler{
		store:            st,
		projectRoot:      projectRoot,
		enableEmbeddings: opts.EnableEmbeddings,
		enableEnrichment: opts.EnableEnrichment,
		noGitignore:      opts.NoGitignore,
	}
	registerTools(s, h, opts)
	return h.indexing.Store
}

// RegisterWorkspaceWithOpts registers the MCP tools for a workspace, with opt-in
// LLM callout flags, and returns the indexing-flag setter.
func RegisterWorkspaceWithOpts(s *server.MCPServer, w *workspace.Workspace, opts RegisterOpts) func(bool) {
	h := &handler{
		workspace:        w,
		enableEmbeddings: opts.EnableEmbeddings,
		enableEnrichment: opts.EnableEnrichment,
		noGitignore:      opts.NoGitignore,
	}
	registerTools(s, h, opts)
	return h.indexing.Store
}

// pendingToken holds an expiring confirmation token for the two-step LLM callout workflow.
//
// docIDs is the set of doc_ids the originating action=pending call authorized.
// process must reject any submitted doc_id not in this set, and remove the
// doc_id from the set on a successful store. The token is deleted only when
// the set is empty (batch fully processed) or the token has expired.
//
// docIDs may be nil for tools that have not yet been migrated to batch-bound
// authorization (currently embeddings); a nil set means the token is treated
// as single-use and consumed via LoadAndDelete on first use, preserving the
// pre-existing behavior for that path.
//
// mu guards docIDs. We chose a per-token mutex stored alongside the value via
// a pointer in the sync.Map rather than a handler-wide mutex because (a) it
// keeps lock scope aligned with the data it protects, (b) it does not
// serialize unrelated tokens' processing, and (c) it matches the per-entry
// concurrency style already implied by sync.Map. MCP stdio currently
// serializes handler calls, but we do not rely on that invariant silently.
type pendingToken struct {
	expiresAt time.Time
	mu        sync.Mutex
	docIDs    map[string]struct{}
}

type handler struct {
	store       *store.Store
	workspace   *workspace.Workspace
	projectRoot string
	indexing    atomic.Bool

	enableEmbeddings bool
	enableEnrichment bool
	noGitignore      bool // mirrors the serve --no-gitignore flag (for docgraph_status)

	// Separate maps prevent cross-tool token reuse — a shared map would allow a token from one tool's pending to authorize the other's processing step.
	embeddingsPendingTokens sync.Map // map[string]*pendingToken
	enrichmentPendingTokens sync.Map // map[string]*pendingToken
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

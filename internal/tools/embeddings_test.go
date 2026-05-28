package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

// newTestHandler creates a handler backed by a temporary in-memory store.
func newTestHandler(t *testing.T) (*handler, *store.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &handler{store: st, projectRoot: t.TempDir()}, st
}

// injectEmbeddingsToken injects a valid embeddings token for test use.
func injectEmbeddingsToken(h *handler, token string) {
	h.embeddingsPendingTokens.Store(token, pendingToken{expiresAt: timeNowForTest().Add(30 * time.Minute)})
}

func timeNowForTest() time.Time { return time.Now() }

func callTool(_ *handler, toolFn func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) (*mcp.CallToolResult, error) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return toolFn(context.Background(), req)
}

// ---------------------------------------------------------------------------
// handleEmbeddingsPending
// ---------------------------------------------------------------------------

func TestHandleEmbeddingsPending_MissingModelID(t *testing.T) {
	h, _ := newTestHandler(t)
	res, err := callTool(h, h.handleEmbeddingsPending, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("expected error result when model_id is missing")
	}
}

func TestHandleEmbeddingsPending_NoDocs(t *testing.T) {
	h, _ := newTestHandler(t)
	res, err := callTool(h, h.handleEmbeddingsPending, map[string]any{
		"model_id": "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)
	if !strings.Contains(text, "0 documents") {
		t.Errorf("expected '0 documents' in output, got: %s", text)
	}
}

func TestHandleEmbeddingsPending_DocAppears(t *testing.T) {
	h, st := newTestHandler(t)
	nodes := []store.Node{
		{ID: "a.md", Kind: "document", Name: "A", QualifiedName: "a.md", FilePath: "a.md", StartLine: 1, EndLine: 5, BodyExcerpt: "some text", UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	res, err := callTool(h, h.handleEmbeddingsPending, map[string]any{
		"model_id":     "test-model",
		"content_mode": "excerpt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)
	if !strings.Contains(text, "a.md") {
		t.Errorf("expected doc a.md in pending output, got: %s", text)
	}
	if !strings.Contains(text, "PRIVACY") {
		t.Error("expected PRIVACY warning in output")
	}
}

// ---------------------------------------------------------------------------
// handleEmbeddingsStore
// ---------------------------------------------------------------------------

func TestHandleEmbeddingsStore_MissingDocID(t *testing.T) {
	h, _ := newTestHandler(t)
	res, err := callTool(h, h.handleEmbeddingsStore, map[string]any{
		"model_id":     "m",
		"vector":       "[0.1,0.2]",
		"content_hash": "h",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("expected error when doc_id missing")
	}
}

func TestHandleEmbeddingsStore_BadVectorJSON(t *testing.T) {
	h, st := newTestHandler(t)
	st.InsertNodes([]store.Node{
		{ID: "doc.md", Kind: "document", Name: "Doc", QualifiedName: "doc.md", FilePath: "doc.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
	})
	res, err := callTool(h, h.handleEmbeddingsStore, map[string]any{
		"doc_id":       "doc.md",
		"model_id":     "m",
		"vector":       "not-json",
		"content_hash": "h",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("expected error for invalid vector JSON")
	}
}

func TestHandleEmbeddingsStore_EmptyVector(t *testing.T) {
	h, st := newTestHandler(t)
	st.InsertNodes([]store.Node{
		{ID: "doc.md", Kind: "document", Name: "Doc", QualifiedName: "doc.md", FilePath: "doc.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
	})
	res, err := callTool(h, h.handleEmbeddingsStore, map[string]any{
		"doc_id":       "doc.md",
		"model_id":     "m",
		"vector":       "[]",
		"content_hash": "h",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("expected error for empty vector")
	}
}

func TestHandleEmbeddingsStore_Success(t *testing.T) {
	h, st := newTestHandler(t)
	st.InsertNodes([]store.Node{
		{ID: "doc.md", Kind: "document", Name: "Doc", QualifiedName: "doc.md", FilePath: "doc.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
	})
	res, err := callTool(h, h.handleEmbeddingsStore, map[string]any{
		"doc_id":       "doc.md",
		"model_id":     "m",
		"vector":       "[0.1, 0.2, 0.3]",
		"content_hash": "hash1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)
	if !strings.Contains(text, "doc.md") {
		t.Errorf("expected doc_id in success message, got: %s", text)
	}

	// Verify the embedding was actually stored.
	emb, err := st.GetEmbedding("doc.md", "m")
	if err != nil {
		t.Fatal(err)
	}
	if emb == nil {
		t.Error("embedding not found in store after successful store")
	}
}

// ---------------------------------------------------------------------------
// handleEmbeddingsClear
// ---------------------------------------------------------------------------

func TestHandleEmbeddingsClear_MissingModelID(t *testing.T) {
	h, _ := newTestHandler(t)
	res, err := callTool(h, h.handleEmbeddingsClear, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("expected error when model_id missing")
	}
}

func TestHandleEmbeddingsClear_DeletesEmbeddings(t *testing.T) {
	h, st := newTestHandler(t)
	nodes := []store.Node{
		{ID: "a.md", Kind: "document", Name: "A", QualifiedName: "a.md", FilePath: "a.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
		{ID: "b.md", Kind: "document", Name: "B", QualifiedName: "b.md", FilePath: "b.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
	}
	st.InsertNodes(nodes)
	st.UpsertEmbedding(store.Embedding{DocID: "a.md", ModelID: "m", Dim: 2, Vector: []float64{1, 0}, ContentHash: "h"})
	st.UpsertEmbedding(store.Embedding{DocID: "b.md", ModelID: "m", Dim: 2, Vector: []float64{0, 1}, ContentHash: "h"})

	res, err := callTool(h, h.handleEmbeddingsClear, map[string]any{
		"model_id": "m",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)
	if !strings.Contains(text, "2") {
		t.Errorf("expected deletion count 2 in output, got: %s", text)
	}

	// Confirm embeddings are gone.
	emb, _ := st.GetEmbedding("a.md", "m")
	if emb != nil {
		t.Error("embedding for a.md should be deleted")
	}
}

func TestHandleEmbeddingsClear_EmptyStore(t *testing.T) {
	h, _ := newTestHandler(t)
	res, err := callTool(h, h.handleEmbeddingsClear, map[string]any{
		"model_id": "nonexistent-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error on empty store clear: %v", res.Content)
	}
}

// ---------------------------------------------------------------------------
// docgraph_embeddings facade
// ---------------------------------------------------------------------------

func TestEmbeddingsFacadePendingReturnsImpactGraph(t *testing.T) {
	h, st := newTestHandler(t)
	nodes := []store.Node{
		{ID: "a.md", Kind: "document", Name: "A", QualifiedName: "a.md", FilePath: "a.md", StartLine: 1, EndLine: 5, BodyExcerpt: "some text", UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{
		"action":       "pending",
		"model_id":     "test-model",
		"content_mode": "excerpt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)
	if !strings.Contains(text, "RELAY") {
		t.Fatalf("pending output must contain RELAY section, got:\n%s", text)
	}
	if !strings.Contains(text, "CONFIRMATION_TOKEN") {
		t.Fatalf("pending output must contain CONFIRMATION_TOKEN, got:\n%s", text)
	}
	if !strings.Contains(text, "1 documents") {
		t.Fatalf("pending output must state document count, got:\n%s", text)
	}
}

func TestEmbeddingsFacadeStoreRequiresToken(t *testing.T) {
	h, _ := newTestHandler(t)
	res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{
		"action":       "store",
		"doc_id":       "doc.md",
		"model_id":     "m",
		"vector":       "[0.1, 0.2, 0.3]",
		"content_hash": "hash1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(extractText(res), "confirmation_token required") {
		t.Fatalf("expected token-required error, got: %s", extractText(res))
	}
}

func TestEmbeddingsFacadeStoreSucceedsWithToken(t *testing.T) {
	h, st := newTestHandler(t)
	if err := st.InsertNodes([]store.Node{
		{ID: "doc.md", Kind: "document", Name: "Doc", QualifiedName: "doc.md", FilePath: "doc.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
	}); err != nil {
		t.Fatal(err)
	}
	injectEmbeddingsToken(h, "tok-store-001")
	res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{
		"action":             "store",
		"confirmation_token": "tok-store-001",
		"doc_id":             "doc.md",
		"model_id":           "m",
		"vector":             "[0.1, 0.2, 0.3]",
		"content_hash":       "hash1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", extractText(res))
	}
}

func TestEmbeddingsFacadeStoreTokenIsConsumedOnce(t *testing.T) {
	h, st := newTestHandler(t)
	if err := st.InsertNodes([]store.Node{
		{ID: "doc.md", Kind: "document", Name: "Doc", QualifiedName: "doc.md", FilePath: "doc.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
	}); err != nil {
		t.Fatal(err)
	}
	injectEmbeddingsToken(h, "single-use-emb")
	args := map[string]any{
		"action":             "store",
		"confirmation_token": "single-use-emb",
		"doc_id":             "doc.md",
		"model_id":           "m",
		"vector":             "[0.1]",
		"content_hash":       "h",
	}
	if res, _ := callTool(h, h.handleEmbeddingsFacade, args); res.IsError {
		t.Fatalf("first call failed: %s", extractText(res))
	}
	res2, _ := callTool(h, h.handleEmbeddingsFacade, args)
	if !res2.IsError {
		t.Fatal("second use of same token must be rejected")
	}
}

func TestEmbeddingsFacadeClearMatchesLegacy(t *testing.T) {
	h, st := newTestHandler(t)
	nodes := []store.Node{
		{ID: "a.md", Kind: "document", Name: "A", QualifiedName: "a.md", FilePath: "a.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
		{ID: "b.md", Kind: "document", Name: "B", QualifiedName: "b.md", FilePath: "b.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	for _, docID := range []string{"a.md", "b.md"} {
		if err := st.UpsertEmbedding(store.Embedding{DocID: docID, ModelID: "m", Dim: 2, Vector: []float64{1, 0}, ContentHash: "h"}); err != nil {
			t.Fatal(err)
		}
	}
	legacy, err := callTool(h, h.handleEmbeddingsClear, map[string]any{"model_id": "m"})
	if err != nil {
		t.Fatal(err)
	}
	for _, docID := range []string{"a.md", "b.md"} {
		if err := st.UpsertEmbedding(store.Embedding{DocID: docID, ModelID: "m", Dim: 2, Vector: []float64{1, 0}, ContentHash: "h"}); err != nil {
			t.Fatal(err)
		}
	}
	facade, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{"action": "clear", "model_id": "m"})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.IsError || facade.IsError {
		t.Fatalf("unexpected error: legacy=%v facade=%v", legacy.IsError, facade.IsError)
	}
	if extractText(facade) != extractText(legacy) {
		t.Fatalf("facade clear output did not match legacy.\nlegacy:\n%s\nfacade:\n%s", extractText(legacy), extractText(facade))
	}
}

func TestEmbeddingsFacadeRejectsUnknownAction(t *testing.T) {
	h, _ := newTestHandler(t)
	res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{"action": "delete"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(extractText(res), "pending, store, clear") {
		t.Fatalf("expected valid action error, got: %#v", res)
	}
}

func TestEmbeddingsFacadePendingRequiresModelID(t *testing.T) {
	h, _ := newTestHandler(t)
	res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{"action": "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error when model_id is missing")
	}
}

func TestEmbeddingsFacadePendingRejectsInvalidContentMode(t *testing.T) {
	h, _ := newTestHandler(t)
	res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{
		"action":       "pending",
		"model_id":     "m",
		"content_mode": "summary",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(extractText(res), "content_mode") {
		t.Fatalf("expected content_mode error, got: %#v", res)
	}
}

func TestEmbeddingsFacadeStoreRequiresDocID(t *testing.T) {
	h, _ := newTestHandler(t)
	res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{
		"action":       "store",
		"model_id":     "m",
		"vector":       "[0.1]",
		"content_hash": "h",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error when doc_id is missing")
	}
}

func TestEmbeddingsFacadeStoreRequiresVector(t *testing.T) {
	h, _ := newTestHandler(t)
	res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{
		"action":       "store",
		"doc_id":       "doc.md",
		"model_id":     "m",
		"content_hash": "h",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error when vector is missing")
	}
}

func TestEmbeddingsFacadeStoreRejectsInvalidVectorJSON(t *testing.T) {
	h, st := newTestHandler(t)
	if err := st.InsertNodes([]store.Node{
		{ID: "doc.md", Kind: "document", Name: "Doc", QualifiedName: "doc.md", FilePath: "doc.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
	}); err != nil {
		t.Fatal(err)
	}
	injectEmbeddingsToken(h, "tok-invalid-vec-json")
	res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{
		"action":             "store",
		"doc_id":             "doc.md",
		"model_id":           "m",
		"vector":             "not-json",
		"content_hash":       "h",
		"confirmation_token": "tok-invalid-vec-json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(extractText(res), "invalid vector JSON") {
		t.Fatalf("expected vector JSON error, got: %#v", res)
	}
}

func TestEmbeddingsFacadeStoreRejectsEmptyVector(t *testing.T) {
	h, st := newTestHandler(t)
	if err := st.InsertNodes([]store.Node{
		{ID: "doc.md", Kind: "document", Name: "Doc", QualifiedName: "doc.md", FilePath: "doc.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
	}); err != nil {
		t.Fatal(err)
	}
	injectEmbeddingsToken(h, "tok-empty-vec")
	res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{
		"action":             "store",
		"doc_id":             "doc.md",
		"model_id":           "m",
		"vector":             "[]",
		"content_hash":       "h",
		"confirmation_token": "tok-empty-vec",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(extractText(res), "must not be empty") {
		t.Fatalf("expected empty vector error, got: %#v", res)
	}
}

func TestEmbeddingsFacadeClearRequiresModelID(t *testing.T) {
	h, _ := newTestHandler(t)
	res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{"action": "clear"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error when model_id is missing")
	}
}

func TestEmbeddingsFacadeClearRejectsStoreArgs(t *testing.T) {
	h, _ := newTestHandler(t)
	res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{
		"action":   "clear",
		"model_id": "m",
		"doc_id":   "doc.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(extractText(res), "not valid for action=clear") {
		t.Fatalf("expected invalid clear argument error, got: %#v", res)
	}
}

func TestEmbeddingsFacadeClearRejectsPendingArgs(t *testing.T) {
	h, _ := newTestHandler(t)
	res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{
		"action":   "clear",
		"model_id": "m",
		"limit":    10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(extractText(res), "not valid for action=clear") {
		t.Fatalf("expected invalid clear argument error, got: %#v", res)
	}
}

// ---------------------------------------------------------------------------
// handleSimilar deduplication
// ---------------------------------------------------------------------------

func TestHandleSimilar_Deduplication(t *testing.T) {
	h, st := newTestHandler(t)
	nodes := []store.Node{
		{ID: "a.md", Kind: "document", Name: "A", QualifiedName: "a.md", FilePath: "a.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
		{ID: "b.md", Kind: "document", Name: "B", QualifiedName: "b.md", FilePath: "b.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
	}
	st.InsertNodes(nodes)

	// Insert both TF-IDF and neural similar_to edges for the same pair.
	edges := []store.Edge{
		{Source: "a.md", Target: "b.md", Kind: "similar_to", Metadata: `{"score":0.5,"engine":"tfidf"}`},
		{Source: "a.md", Target: "b.md", Kind: "similar_to", Metadata: `{"score":0.9,"engine":"neural","model_id":"m"}`},
	}
	if err := st.InsertEdges(edges); err != nil {
		t.Fatal(err)
	}

	res, err := callTool(h, h.handleSimilar, map[string]any{
		"document": "a.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}

	text := extractText(res)
	// Should appear exactly once, not twice.
	count := strings.Count(text, "B")
	if count != 1 {
		t.Errorf("expected doc B to appear once (deduplicated), appeared %d times in:\n%s", count, text)
	}
	// Neural should be preferred.
	if !strings.Contains(text, "neural") {
		t.Errorf("expected neural engine to be preferred, got:\n%s", text)
	}
}

// ---------------------------------------------------------------------------
// LLM UX audit verification
// ---------------------------------------------------------------------------

func TestSimilarNeuralRequiresFlagError(t *testing.T) {
	h, st := newTestHandler(t)
	st.InsertNodes([]store.Node{
		{ID: "a.md", Kind: "document", Name: "A", QualifiedName: "a.md", FilePath: "a.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
	})
	res, err := callTool(h, h.handleSimilar, map[string]any{
		"document": "a.md",
		"engine":   "neural",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected error for neural engine without --enable-embeddings, got: %v", res.Content)
	}
	if !strings.Contains(extractText(res), "Neural similarity requires --enable-embeddings") {
		t.Errorf("expected flag hint in error message, got: %s", extractText(res))
	}
}

func TestStatusLLMCalloutDisabled(t *testing.T) {
	h, _ := newTestHandler(t)
	// enableEmbeddings and enableEnrichment are false by default in newTestHandler.
	res, err := callTool(h, h.handleStatus, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)
	if !strings.Contains(text, "--enable-embeddings to activate") {
		t.Errorf("expected --enable-embeddings hint in status output, got:\n%s", text)
	}
	if !strings.Contains(text, "--enable-enrichment to activate") {
		t.Errorf("expected --enable-enrichment hint in status output, got:\n%s", text)
	}
}

func TestStatusLLMCalloutEnabled(t *testing.T) {
	h, st := newTestHandler(t)
	h.enableEmbeddings = true
	st.InsertNodes([]store.Node{
		{ID: "a.md", Kind: "document", Name: "A", QualifiedName: "a.md", FilePath: "a.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
	})
	if err := st.UpsertEmbedding(store.Embedding{DocID: "a.md", ModelID: "m", Dim: 2, Vector: []float64{0.1, 0.2}, ContentHash: "h"}); err != nil {
		t.Fatalf("UpsertEmbedding: %v", err)
	}
	res, err := callTool(h, h.handleStatus, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)
	if !strings.Contains(text, "enabled") {
		t.Errorf("expected 'enabled' in status output when embeddings on, got:\n%s", text)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func extractText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if txt, ok := c.(mcp.TextContent); ok {
			return txt.Text
		}
	}
	return fmt.Sprintf("%v", res.Content)
}

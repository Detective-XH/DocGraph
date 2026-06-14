package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
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
// docIDs are the doc_ids this token authorizes for action=store; pass the same
// doc_id values the test will later submit. Empty list yields a token whose
// batch is already empty (rejects every doc_id).
func injectEmbeddingsToken(h *handler, token string, docIDs ...string) {
	set := make(map[string]struct{}, len(docIDs))
	for _, id := range docIDs {
		set[id] = struct{}{}
	}
	h.embeddingsPendingTokens.Store(token, &pendingToken{
		expiresAt: timeNowForTest().Add(30 * time.Minute),
		docIDs:    set,
	})
}

func timeNowForTest() time.Time { return time.Now() }

func callTool(_ *handler, toolFn func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) (*mcp.CallToolResult, error) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return toolFn(context.Background(), req)
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
	injectEmbeddingsToken(h, "tok-store-001", "doc.md")
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
	injectEmbeddingsToken(h, "single-use-emb", "doc.md")
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

func TestEmbeddingsFacadeStoreTokenAuthorizesEntireBatch(t *testing.T) {
	h, st := newTestHandler(t)
	for _, id := range []string{"a.md", "b.md", "c.md"} {
		if err := st.InsertNodes([]store.Node{
			{ID: id, Kind: "document", Name: id, QualifiedName: id, FilePath: id, StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
		}); err != nil {
			t.Fatal(err)
		}
	}
	injectEmbeddingsToken(h, "batch-emb", "a.md", "b.md", "c.md")

	for _, id := range []string{"a.md", "b.md", "c.md"} {
		res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{
			"action":             "store",
			"confirmation_token": "batch-emb",
			"doc_id":             id,
			"model_id":           "m",
			"vector":             "[0.1, 0.2]",
			"content_hash":       "h",
		})
		if err != nil || res.IsError {
			t.Fatalf("doc %s should succeed under batch token: err=%v res=%+v", id, err, res)
		}
	}
}

func TestEmbeddingsFacadeStoreTokenDeletedOnlyAfterLastDoc(t *testing.T) {
	h, st := newTestHandler(t)
	for _, id := range []string{"a.md", "b.md"} {
		if err := st.InsertNodes([]store.Node{
			{ID: id, Kind: "document", Name: id, QualifiedName: id, FilePath: id, StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
		}); err != nil {
			t.Fatal(err)
		}
	}
	injectEmbeddingsToken(h, "two-doc-emb", "a.md", "b.md")

	res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{
		"action":             "store",
		"confirmation_token": "two-doc-emb",
		"doc_id":             "a.md",
		"model_id":           "m",
		"vector":             "[0.1]",
		"content_hash":       "h",
	})
	if err != nil || res.IsError {
		t.Fatalf("first doc must succeed: err=%v res=%+v", err, res)
	}
	if _, ok := h.embeddingsPendingTokens.Load("two-doc-emb"); !ok {
		t.Fatal("token must survive between docs in the same batch")
	}

	res, err = callTool(h, h.handleEmbeddingsFacade, map[string]any{
		"action":             "store",
		"confirmation_token": "two-doc-emb",
		"doc_id":             "b.md",
		"model_id":           "m",
		"vector":             "[0.2]",
		"content_hash":       "h",
	})
	if err != nil || res.IsError {
		t.Fatalf("second doc must succeed: err=%v res=%+v", err, res)
	}
	if _, ok := h.embeddingsPendingTokens.Load("two-doc-emb"); ok {
		t.Fatal("token must be deleted after the last authorized doc is processed")
	}
}

func TestEmbeddingsFacadeStoreRejectsUnauthorizedDocAndKeepsToken(t *testing.T) {
	h, st := newTestHandler(t)
	for _, id := range []string{"a.md", "rogue.md"} {
		if err := st.InsertNodes([]store.Node{
			{ID: id, Kind: "document", Name: id, QualifiedName: id, FilePath: id, StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
		}); err != nil {
			t.Fatal(err)
		}
	}
	injectEmbeddingsToken(h, "scoped-emb", "a.md")

	res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{
		"action":             "store",
		"confirmation_token": "scoped-emb",
		"doc_id":             "rogue.md",
		"model_id":           "m",
		"vector":             "[0.1]",
		"content_hash":       "h",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for doc_id not in authorized batch")
	}
	if !strings.Contains(extractText(res), "not in the authorized batch") {
		t.Fatalf("expected unauthorized-batch message, got: %s", extractText(res))
	}
	if _, ok := h.embeddingsPendingTokens.Load("scoped-emb"); !ok {
		t.Fatal("token must survive a rejected unauthorized doc_id")
	}

	res, err = callTool(h, h.handleEmbeddingsFacade, map[string]any{
		"action":             "store",
		"confirmation_token": "scoped-emb",
		"doc_id":             "a.md",
		"model_id":           "m",
		"vector":             "[0.1]",
		"content_hash":       "h",
	})
	if err != nil || res.IsError {
		t.Fatalf("authorized doc must succeed after rejected sibling: err=%v res=%+v", err, res)
	}
}

func TestEmbeddingsFacadeStoreConcurrentBatchIsRaceFree(t *testing.T) {
	// Exercises pt.mu by running two concurrent store calls against the same
	// token with two distinct authorized doc_ids.
	h, st := newTestHandler(t)
	for _, id := range []string{"a.md", "b.md"} {
		if err := st.InsertNodes([]store.Node{
			{ID: id, Kind: "document", Name: id, QualifiedName: id, FilePath: id, StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1},
		}); err != nil {
			t.Fatal(err)
		}
	}
	injectEmbeddingsToken(h, "race-emb", "a.md", "b.md")

	var wg sync.WaitGroup
	results := make([]*mcp.CallToolResult, 2)
	docs := []string{"a.md", "b.md"}
	for i, id := range docs {
		wg.Add(1)
		go func(idx int, docID string) {
			defer wg.Done()
			res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{
				"action":             "store",
				"confirmation_token": "race-emb",
				"doc_id":             docID,
				"model_id":           "m",
				"vector":             "[0.1]",
				"content_hash":       "h",
			})
			if err != nil {
				t.Errorf("concurrent store for %s returned err: %v", docID, err)
				return
			}
			results[idx] = res
		}(i, id)
	}
	wg.Wait()
	for i, res := range results {
		if res == nil || res.IsError {
			t.Fatalf("concurrent store %d failed: %+v", i, res)
		}
	}
	if _, ok := h.embeddingsPendingTokens.Load("race-emb"); ok {
		t.Fatal("token must be deleted after both authorized docs are processed")
	}
}

func TestEmbeddingsFacadeClearActuallyDeletes(t *testing.T) {
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

	res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{"action": "clear", "model_id": "m"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", extractText(res))
	}
	for _, docID := range []string{"a.md", "b.md"} {
		emb, _ := st.GetEmbedding(docID, "m")
		if emb != nil {
			t.Errorf("embedding for %s should be deleted after action=clear", docID)
		}
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
	injectEmbeddingsToken(h, "tok-invalid-vec-json", "doc.md")
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
	injectEmbeddingsToken(h, "tok-empty-vec", "doc.md")
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
// Characterization tests: pending token binds exactly the shown docIDs
// and Document List render includes doc_id / path / content_hash.
// Written before refactoring handleEmbeddingsFacadePending to satisfy GUARDRAIL-8.
// ---------------------------------------------------------------------------

func TestEmbeddingsFacadePending_TokenBindsExactDocIDs(t *testing.T) {
	h, st := newTestHandler(t)
	// Two non-sensitive docs (file watcher marks them all as pending for the
	// given model_id since no embedding has been stored yet).
	nodes := []store.Node{
		{ID: "p.md", Kind: "document", Name: "P", QualifiedName: "p.md", FilePath: "p.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body p", UpdatedAt: 1},
		{ID: "q.md", Kind: "document", Name: "Q", QualifiedName: "q.md", FilePath: "q.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body q", UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}

	res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{
		"action":       "pending",
		"model_id":     "test-char-model",
		"content_mode": "excerpt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}

	// Exactly one token must be stored.
	var storedToken *pendingToken
	var tokenCount int
	h.embeddingsPendingTokens.Range(func(_, v any) bool {
		storedToken = v.(*pendingToken)
		tokenCount++
		return true
	})
	if tokenCount != 1 {
		t.Fatalf("expected exactly 1 pending token, got %d", tokenCount)
	}

	// The token's docIDs set must contain exactly p.md and q.md.
	storedToken.mu.Lock()
	ids := make(map[string]struct{}, len(storedToken.docIDs))
	for k, v := range storedToken.docIDs {
		ids[k] = v
	}
	storedToken.mu.Unlock()

	for _, want := range []string{"p.md", "q.md"} {
		if _, ok := ids[want]; !ok {
			t.Errorf("token docIDs missing %q; got %v", want, ids)
		}
	}
	if len(ids) != 2 {
		t.Errorf("token docIDs has %d entries, want 2; got %v", len(ids), ids)
	}
}

func TestEmbeddingsFacadePending_DocListRendersFields(t *testing.T) {
	h, st := newTestHandler(t)
	nodes := []store.Node{
		{ID: "alpha.md", Kind: "document", Name: "Alpha", QualifiedName: "alpha.md", FilePath: "alpha.md", StartLine: 1, EndLine: 5, BodyExcerpt: "excerpt alpha", UpdatedAt: 1},
		{ID: "beta.md", Kind: "document", Name: "Beta", QualifiedName: "beta.md", FilePath: "beta.md", StartLine: 1, EndLine: 5, BodyExcerpt: "excerpt beta", UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}

	res, err := callTool(h, h.handleEmbeddingsFacade, map[string]any{
		"action":       "pending",
		"model_id":     "test-char-model-2",
		"content_mode": "excerpt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)

	// Each doc must appear in Document List with its doc_id, path, content_hash.
	for _, doc := range []struct{ id, path string }{
		{"alpha.md", "alpha.md"},
		{"beta.md", "beta.md"},
	} {
		if !strings.Contains(text, "`"+doc.id+"`") {
			t.Errorf("doc_id %q not found in output:\n%s", doc.id, text)
		}
		if !strings.Contains(text, doc.path) {
			t.Errorf("path %q not found in output:\n%s", doc.path, text)
		}
	}
	if !strings.Contains(text, "## Document List") {
		t.Errorf("Document List section header missing from output:\n%s", text)
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

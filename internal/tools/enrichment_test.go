package tools

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

func insertToolEnrichmentDoc(t *testing.T, st *store.Store, id, hash string, hasFrontmatter bool) {
	t.Helper()
	if err := st.InsertNodes([]store.Node{
		{ID: id, Kind: "document", Name: id, QualifiedName: id, FilePath: id, StartLine: 1, EndLine: 3, BodyExcerpt: "Document body", UpdatedAt: 1},
	}); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	if err := st.UpsertFile(store.FileInfo{
		Path:           id,
		ContentHash:    hash,
		Size:           100,
		ModifiedAt:     time.Now().Unix(),
		IndexedAt:      time.Now().Unix(),
		NodeCount:      1,
		HasFrontmatter: hasFrontmatter,
	}); err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
}

// injectEnrichmentToken injects a valid token directly into the handler for test use.
// docIDs are the doc_ids this token authorizes; pass the same doc_id values the
// test will later submit via action=process. An empty list yields a token whose
// batch is already empty (rejects every doc_id).
func injectEnrichmentToken(h *handler, token string, docIDs ...string) {
	set := make(map[string]struct{}, len(docIDs))
	for _, id := range docIDs {
		set[id] = struct{}{}
	}
	h.enrichmentPendingTokens.Store(token, &pendingToken{
		expiresAt: time.Now().Add(30 * time.Minute),
		docIDs:    set,
	})
}

func TestHandleEnrichmentPending_ReturnsFrontmatterlessDocs(t *testing.T) {
	h, st := newTestHandler(t)
	insertToolEnrichmentDoc(t, st, "a.pdf", "hash-a", false)
	insertToolEnrichmentDoc(t, st, "b.md", "hash-b", true)

	res, err := callTool(h, h.handleEnrichmentPending, map[string]any{
		"content_mode": "excerpt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)
	if !strings.Contains(text, "a.pdf") {
		t.Fatalf("expected a.pdf in output, got: %s", text)
	}
	if strings.Contains(text, "b.md") {
		t.Fatalf("frontmatter document should not be pending, got: %s", text)
	}
	if !strings.Contains(text, "RELAY") {
		t.Fatalf("expected RELAY section in pending output, got: %s", text)
	}
	if !strings.Contains(text, "CONFIRMATION_TOKEN") {
		t.Fatalf("expected CONFIRMATION_TOKEN in pending output, got: %s", text)
	}
}

func TestHandleEnrichmentProcess_StoresSummaryAndAgentMetadata(t *testing.T) {
	h, st := newTestHandler(t)
	insertToolEnrichmentDoc(t, st, "a.pdf", "hash-a", false)
	injectEnrichmentToken(h, "test-tok-001", "a.pdf")

	res, err := callTool(h, h.handleEnrichmentProcess, map[string]any{
		"confirmation_token": "test-tok-001",
		"doc_id":             "a.pdf",
		"content_hash":       "hash-a",
		"summary":            "Agent summary.",
		"metadata":           `{"status":"draft","confidence":"medium","review_due":"2026-12-31","tags":["policy","pdf"]}`,
		"confidence":         0.8,
		"model_id":           "test-model",
		"agent_id":           "test-agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}

	summary, err := st.GetAISummary("a.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if summary == nil || summary.Summary != "Agent summary." {
		t.Fatalf("summary was not stored: %+v", summary)
	}
	if summary.ModelID != "test-model" || summary.AgentID != "test-agent" {
		t.Fatalf("summary lineage was not stored: %+v", summary)
	}

	tuples, err := st.GetDocumentMetadata("a.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if len(tuples) != 4 {
		t.Fatalf("expected 4 metadata tuples, got %d: %+v", len(tuples), tuples)
	}
	for _, tuple := range tuples {
		if tuple.Source != "agent_inferred" {
			t.Fatalf("expected source=agent_inferred, got %+v", tuple)
		}
		if tuple.Confidence == nil || *tuple.Confidence != 0.8 {
			t.Fatalf("expected confidence 0.8, got %+v", tuple.Confidence)
		}
	}
}

func TestHandleEnrichmentProcess_RejectsUnsupportedMetadata(t *testing.T) {
	h, st := newTestHandler(t)
	insertToolEnrichmentDoc(t, st, "a.pdf", "hash-a", false)
	injectEnrichmentToken(h, "test-tok-002", "a.pdf")

	res, err := callTool(h, h.handleEnrichmentProcess, map[string]any{
		"confirmation_token": "test-tok-002",
		"doc_id":             "a.pdf",
		"content_hash":       "hash-a",
		"metadata":           `{"nested":{"unsupported":true}}`,
		"model_id":           "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for nested metadata object")
	}
}

func TestHandleEnrichmentProcess_RequiresModelID(t *testing.T) {
	h, st := newTestHandler(t)
	insertToolEnrichmentDoc(t, st, "a.pdf", "hash-a", false)
	injectEnrichmentToken(h, "test-tok-003", "a.pdf")

	res, err := callTool(h, h.handleEnrichmentProcess, map[string]any{
		"confirmation_token": "test-tok-003",
		"doc_id":             "a.pdf",
		"content_hash":       "hash-a",
		"summary":            "Agent summary.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(extractText(res), "model_id") {
		t.Fatalf("expected model_id error, got: %+v", res)
	}
}

func TestHandleEnrichmentProcess_RequiresToken(t *testing.T) {
	h, st := newTestHandler(t)
	insertToolEnrichmentDoc(t, st, "a.pdf", "hash-a", false)

	res, err := callTool(h, h.handleEnrichmentProcess, map[string]any{
		"doc_id":       "a.pdf",
		"content_hash": "hash-a",
		"summary":      "Agent summary.",
		"model_id":     "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error when confirmation_token is missing")
	}
	if !strings.Contains(extractText(res), "confirmation_token required") {
		t.Fatalf("expected token-required message, got: %s", extractText(res))
	}
}

func TestHandleEnrichmentProcess_RejectsInvalidToken(t *testing.T) {
	h, _ := newTestHandler(t)
	res, err := callTool(h, h.handleEnrichmentProcess, map[string]any{
		"confirmation_token": "bad-token",
		"doc_id":             "a.pdf",
		"content_hash":       "hash-a",
		"summary":            "summary",
		"model_id":           "model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for invalid token")
	}
	if !strings.Contains(extractText(res), "Invalid confirmation_token") {
		t.Fatalf("expected invalid-token message, got: %s", extractText(res))
	}
}

func TestHandleEnrichmentProcess_RejectsExpiredToken(t *testing.T) {
	h, _ := newTestHandler(t)
	h.enrichmentPendingTokens.Store("expired-tok", &pendingToken{
		expiresAt: time.Now().Add(-1 * time.Minute),
		docIDs:    map[string]struct{}{"a.pdf": {}},
	})

	res, err := callTool(h, h.handleEnrichmentProcess, map[string]any{
		"confirmation_token": "expired-tok",
		"doc_id":             "a.pdf",
		"content_hash":       "hash-a",
		"summary":            "summary",
		"model_id":           "model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for expired token")
	}
	if !strings.Contains(extractText(res), "expired") {
		t.Fatalf("expected expiry message, got: %s", extractText(res))
	}
}

func TestHandleEnrichmentProcess_TokenIsConsumedOnce(t *testing.T) {
	h, st := newTestHandler(t)
	insertToolEnrichmentDoc(t, st, "a.pdf", "hash-a", false)
	injectEnrichmentToken(h, "single-use-tok", "a.pdf")

	args := map[string]any{
		"confirmation_token": "single-use-tok",
		"doc_id":             "a.pdf",
		"content_hash":       "hash-a",
		"summary":            "ok",
		"model_id":           "model",
	}
	res, err := callTool(h, h.handleEnrichmentProcess, args)
	if err != nil || res.IsError {
		t.Fatalf("first call failed: %v / %v", err, res)
	}
	// Second use of the same token must fail.
	res2, err := callTool(h, h.handleEnrichmentProcess, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res2.IsError {
		t.Fatal("second use of same token must be rejected")
	}
}

func TestHandleEnrichmentProcess_TokenAuthorizesEntireBatch(t *testing.T) {
	h, st := newTestHandler(t)
	insertToolEnrichmentDoc(t, st, "a.pdf", "hash-a", false)
	insertToolEnrichmentDoc(t, st, "b.pdf", "hash-b", false)
	insertToolEnrichmentDoc(t, st, "c.pdf", "hash-c", false)
	injectEnrichmentToken(h, "batch-tok", "a.pdf", "b.pdf", "c.pdf")

	// All three docs must succeed on the same token.
	for _, doc := range []struct{ id, hash string }{
		{"a.pdf", "hash-a"},
		{"b.pdf", "hash-b"},
		{"c.pdf", "hash-c"},
	} {
		res, err := callTool(h, h.handleEnrichmentProcess, map[string]any{
			"confirmation_token": "batch-tok",
			"doc_id":             doc.id,
			"content_hash":       doc.hash,
			"summary":            "ok",
			"model_id":           "model",
		})
		if err != nil || res.IsError {
			t.Fatalf("doc %s should succeed under batch token, got: err=%v res=%+v", doc.id, err, res)
		}
	}
}

func TestHandleEnrichmentProcess_TokenDeletedOnlyAfterLastDoc(t *testing.T) {
	h, st := newTestHandler(t)
	insertToolEnrichmentDoc(t, st, "a.pdf", "hash-a", false)
	insertToolEnrichmentDoc(t, st, "b.pdf", "hash-b", false)
	injectEnrichmentToken(h, "two-doc-tok", "a.pdf", "b.pdf")

	res, err := callTool(h, h.handleEnrichmentProcess, map[string]any{
		"confirmation_token": "two-doc-tok",
		"doc_id":             "a.pdf",
		"content_hash":       "hash-a",
		"summary":            "ok",
		"model_id":           "model",
	})
	if err != nil || res.IsError {
		t.Fatalf("first doc must succeed: err=%v res=%+v", err, res)
	}
	if _, ok := h.enrichmentPendingTokens.Load("two-doc-tok"); !ok {
		t.Fatal("token must survive between docs in the same batch")
	}

	res, err = callTool(h, h.handleEnrichmentProcess, map[string]any{
		"confirmation_token": "two-doc-tok",
		"doc_id":             "b.pdf",
		"content_hash":       "hash-b",
		"summary":            "ok",
		"model_id":           "model",
	})
	if err != nil || res.IsError {
		t.Fatalf("second doc must succeed: err=%v res=%+v", err, res)
	}
	if _, ok := h.enrichmentPendingTokens.Load("two-doc-tok"); ok {
		t.Fatal("token must be deleted after the last authorized doc is processed")
	}
}

func TestHandleEnrichmentProcess_RejectsUnauthorizedDocAndKeepsToken(t *testing.T) {
	h, st := newTestHandler(t)
	insertToolEnrichmentDoc(t, st, "a.pdf", "hash-a", false)
	insertToolEnrichmentDoc(t, st, "rogue.pdf", "hash-rogue", false)
	injectEnrichmentToken(h, "scoped-tok", "a.pdf")

	res, err := callTool(h, h.handleEnrichmentProcess, map[string]any{
		"confirmation_token": "scoped-tok",
		"doc_id":             "rogue.pdf",
		"content_hash":       "hash-rogue",
		"summary":            "should not store",
		"model_id":           "model",
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
	if _, ok := h.enrichmentPendingTokens.Load("scoped-tok"); !ok {
		t.Fatal("token must survive a rejected unauthorized doc_id")
	}

	// The originally authorized doc must still be processable on the same token.
	res, err = callTool(h, h.handleEnrichmentProcess, map[string]any{
		"confirmation_token": "scoped-tok",
		"doc_id":             "a.pdf",
		"content_hash":       "hash-a",
		"summary":            "ok",
		"model_id":           "model",
	})
	if err != nil || res.IsError {
		t.Fatalf("authorized doc must succeed after rejected sibling: err=%v res=%+v", err, res)
	}
}

func TestHandleEnrichmentPending_AllSensitiveYieldsNoToken(t *testing.T) {
	h, st := newTestHandler(t)
	// Both paths sit under a sensitive-keyword folder ("secret" is in the
	// callout package's sensitiveKeywords list), so IsAllSensitive is true
	// and pending must not generate a confirmation token.
	insertToolEnrichmentDoc(t, st, "secret/a.pdf", "hash-a", false)
	insertToolEnrichmentDoc(t, st, "secret/b.pdf", "hash-b", false)

	res, err := callTool(h, h.handleEnrichmentPending, map[string]any{
		"content_mode": "excerpt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tokenCount := 0
	h.enrichmentPendingTokens.Range(func(_, _ any) bool { tokenCount++; return true })
	if tokenCount != 0 {
		t.Fatalf("expected no token to be stored when all docs are sensitive, got %d", tokenCount)
	}
}

func TestHandleEnrichmentProcess_ConcurrentBatchIsRaceFree(t *testing.T) {
	// Exercises pt.mu by running two concurrent process calls against the same
	// token with two distinct authorized doc_ids. Run under `go test -race`
	// to catch any unguarded access to the docIDs set.
	h, st := newTestHandler(t)
	insertToolEnrichmentDoc(t, st, "a.pdf", "hash-a", false)
	insertToolEnrichmentDoc(t, st, "b.pdf", "hash-b", false)
	injectEnrichmentToken(h, "race-tok", "a.pdf", "b.pdf")

	var wg sync.WaitGroup
	results := make([]*mcp.CallToolResult, 2)
	docs := []struct{ id, hash string }{
		{"a.pdf", "hash-a"},
		{"b.pdf", "hash-b"},
	}
	for i, d := range docs {
		wg.Add(1)
		go func(idx int, id, hash string) {
			defer wg.Done()
			res, err := callTool(h, h.handleEnrichmentProcess, map[string]any{
				"confirmation_token": "race-tok",
				"doc_id":             id,
				"content_hash":       hash,
				"summary":            "ok",
				"model_id":           "model",
			})
			if err != nil {
				t.Errorf("concurrent process for %s returned err: %v", id, err)
				return
			}
			results[idx] = res
		}(i, d.id, d.hash)
	}
	wg.Wait()
	for i, res := range results {
		if res == nil || res.IsError {
			t.Fatalf("concurrent process %d failed: %+v", i, res)
		}
	}
	if _, ok := h.enrichmentPendingTokens.Load("race-tok"); ok {
		t.Fatal("token must be deleted after both authorized docs are processed")
	}
}

func TestHandleEnrichmentFacade_RoutesActions(t *testing.T) {
	h, st := newTestHandler(t)
	insertToolEnrichmentDoc(t, st, "a.pdf", "hash-a", false)

	res, err := callTool(h, h.handleEnrichment, map[string]any{
		"action":       "pending",
		"content_mode": "excerpt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected pending error: %v", res.Content)
	}
	if text := extractText(res); !strings.Contains(text, "a.pdf") {
		t.Fatalf("expected pending output to include a.pdf, got: %s", text)
	}
}

func TestHandleEnrichmentFacade_RejectsUnknownAction(t *testing.T) {
	h, _ := newTestHandler(t)
	res, err := callTool(h, h.handleEnrichment, map[string]any{
		"action": "unknown",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for unknown action")
	}
	if !strings.Contains(extractText(res), "pending, process") {
		t.Fatalf("expected valid action list, got: %s", extractText(res))
	}
}

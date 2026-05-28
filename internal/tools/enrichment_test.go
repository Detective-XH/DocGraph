package tools

import (
	"strings"
	"testing"
	"time"

	"github.com/Detective-XH/docgraph/internal/store"
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
func injectEnrichmentToken(h *handler, token string) {
	h.enrichmentPendingTokens.Store(token, pendingToken{expiresAt: time.Now().Add(30 * time.Minute)})
}

func TestHandleEnrichmentPending_ReturnsFrontmatterlessDocs(t *testing.T) {
	h, st := newTestHandler(t)
	insertToolEnrichmentDoc(t, st, "a.pdf", "hash-a", false)
	insertToolEnrichmentDoc(t, st, "b.md", "hash-b", true)

	res, err := callTool(h, h.handleEnrichmentPending, map[string]interface{}{
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
	injectEnrichmentToken(h, "test-tok-001")

	res, err := callTool(h, h.handleEnrichmentProcess, map[string]interface{}{
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
	injectEnrichmentToken(h, "test-tok-002")

	res, err := callTool(h, h.handleEnrichmentProcess, map[string]interface{}{
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
	injectEnrichmentToken(h, "test-tok-003")

	res, err := callTool(h, h.handleEnrichmentProcess, map[string]interface{}{
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

	res, err := callTool(h, h.handleEnrichmentProcess, map[string]interface{}{
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
	res, err := callTool(h, h.handleEnrichmentProcess, map[string]interface{}{
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
	h.enrichmentPendingTokens.Store("expired-tok", pendingToken{expiresAt: time.Now().Add(-1 * time.Minute)})

	res, err := callTool(h, h.handleEnrichmentProcess, map[string]interface{}{
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
	injectEnrichmentToken(h, "single-use-tok")

	args := map[string]interface{}{
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

func TestHandleEnrichmentFacade_RoutesActions(t *testing.T) {
	h, st := newTestHandler(t)
	insertToolEnrichmentDoc(t, st, "a.pdf", "hash-a", false)

	res, err := callTool(h, h.handleEnrichment, map[string]interface{}{
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
	res, err := callTool(h, h.handleEnrichment, map[string]interface{}{
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

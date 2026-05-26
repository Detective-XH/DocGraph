package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

// ---------------------------------------------------------------------------
// appendBoundedContent — indexed chunk path
// ---------------------------------------------------------------------------

func TestAppendBoundedContent_ChunkPresent(t *testing.T) {
	h, st := newTestHandler(t)
	node := store.Node{
		ID: "doc.md", Kind: "document", Name: "Doc", QualifiedName: "doc.md",
		FilePath: "doc.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1,
	}
	if err := st.InsertNodes([]store.Node{node}); err != nil {
		t.Fatal(err)
	}
	chunk := store.SectionChunk{
		NodeID:    "doc.md",
		FilePath:  "doc.md",
		StartLine: 1,
		EndLine:   5,
		Text:      "# Doc\nHello world",
	}
	if err := st.UpsertSectionChunks([]store.SectionChunk{chunk}); err != nil {
		t.Fatal(err)
	}

	var sb strings.Builder
	appendBoundedContent(&sb, h, &node, 2000)

	out := sb.String()
	if !strings.Contains(out, "Hello world") {
		t.Errorf("expected chunk text in output, got: %s", out)
	}
	if !strings.Contains(out, "indexed snapshot") {
		t.Errorf("expected 'indexed snapshot' annotation, got: %s", out)
	}
	// Must NOT contain the live-read annotation.
	if strings.Contains(out, "live read") {
		t.Errorf("unexpected 'live read' annotation in chunk path: %s", out)
	}
}

// ---------------------------------------------------------------------------
// appendBoundedContent — fallback live read path
// ---------------------------------------------------------------------------

func TestAppendBoundedContent_FallbackLiveRead(t *testing.T) {
	h, st := newTestHandler(t)
	// Write a real file under projectRoot so ReadSectionContent can find it.
	mdContent := "# Fallback\nLive content here\n"
	if err := os.WriteFile(filepath.Join(h.projectRoot, "fallback.md"), []byte(mdContent), 0o644); err != nil {
		t.Fatal(err)
	}
	node := store.Node{
		ID: "fallback.md", Kind: "document", Name: "Fallback", QualifiedName: "fallback.md",
		FilePath: "fallback.md", StartLine: 1, EndLine: 2, BodyExcerpt: "body", UpdatedAt: 1,
	}
	if err := st.InsertNodes([]store.Node{node}); err != nil {
		t.Fatal(err)
	}
	// No chunk upserted — triggers fallback path.

	var sb strings.Builder
	appendBoundedContent(&sb, h, &node, 2000)

	out := sb.String()
	if !strings.Contains(out, "Live content here") {
		t.Errorf("expected live file content in fallback output, got: %s", out)
	}
	if !strings.Contains(out, "live read") {
		t.Errorf("expected 'live read' annotation in fallback output, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// appendBoundedContent — maxBytes truncation on chunk path
// ---------------------------------------------------------------------------

func TestAppendBoundedContent_ChunkTruncated(t *testing.T) {
	h, st := newTestHandler(t)
	node := store.Node{
		ID: "long.md", Kind: "document", Name: "Long", QualifiedName: "long.md",
		FilePath: "long.md", StartLine: 1, EndLine: 100, BodyExcerpt: "body", UpdatedAt: 1,
	}
	if err := st.InsertNodes([]store.Node{node}); err != nil {
		t.Fatal(err)
	}
	longText := strings.Repeat("x", 500)
	chunk := store.SectionChunk{
		NodeID:    "long.md",
		FilePath:  "long.md",
		StartLine: 1,
		EndLine:   100,
		Text:      longText,
	}
	if err := st.UpsertSectionChunks([]store.SectionChunk{chunk}); err != nil {
		t.Fatal(err)
	}

	var sb strings.Builder
	appendBoundedContent(&sb, h, &node, 100)

	out := sb.String()
	if !strings.Contains(out, "content truncated at 100 bytes") {
		t.Errorf("expected truncation notice, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// appendBoundedContent — StartLine == -1 (non-line-based source)
// ---------------------------------------------------------------------------

func TestAppendBoundedContent_ChunkNoLineRange(t *testing.T) {
	h, st := newTestHandler(t)
	node := store.Node{
		ID: "noline.md", Kind: "document", Name: "NoLine", QualifiedName: "noline.md",
		FilePath: "noline.md", StartLine: -1, EndLine: -1, BodyExcerpt: "body", UpdatedAt: 1,
	}
	if err := st.InsertNodes([]store.Node{node}); err != nil {
		t.Fatal(err)
	}
	chunk := store.SectionChunk{
		NodeID:    "noline.md",
		FilePath:  "noline.md",
		StartLine: -1,
		EndLine:   -1,
		Text:      "no line range content",
	}
	if err := st.UpsertSectionChunks([]store.SectionChunk{chunk}); err != nil {
		t.Fatal(err)
	}

	var sb strings.Builder
	appendBoundedContent(&sb, h, &node, 2000)

	out := sb.String()
	if strings.Contains(out, "-1") {
		t.Errorf("line -1 should not appear in output, got: %s", out)
	}
	if !strings.Contains(out, "indexed snapshot") {
		t.Errorf("expected 'indexed snapshot' label, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// handleNode --section — indexed chunk path
// ---------------------------------------------------------------------------

func TestHandleNode_SectionChunkPresent(t *testing.T) {
	h, st := newTestHandler(t)

	// Insert document node.
	docNode := store.Node{
		ID: "doc.md", Kind: "document", Name: "Doc", QualifiedName: "doc.md",
		FilePath: "doc.md", StartLine: 1, EndLine: 20, BodyExcerpt: "body", UpdatedAt: 1,
	}
	// Insert heading node (child of doc).
	headingNode := store.Node{
		ID: "doc.md#intro", Kind: "heading", Name: "Intro", QualifiedName: "doc.md#Intro",
		FilePath: "doc.md", StartLine: 3, EndLine: 10, Level: 1, UpdatedAt: 1,
	}
	if err := st.InsertNodes([]store.Node{docNode, headingNode}); err != nil {
		t.Fatal(err)
	}
	// Insert a chunk for the heading.
	chunk := store.SectionChunk{
		NodeID:    "doc.md#intro",
		FilePath:  "doc.md",
		StartLine: 3,
		EndLine:   10,
		Text:      "## Intro\nThis is the intro section.",
	}
	if err := st.UpsertSectionChunks([]store.SectionChunk{chunk}); err != nil {
		t.Fatal(err)
	}

	res, err := callTool(h, h.handleNode, map[string]interface{}{
		"document": "doc.md",
		"section":  "Intro",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)
	if !strings.Contains(text, "intro section") {
		t.Errorf("expected chunk text in output, got: %s", text)
	}
	if !strings.Contains(text, "indexed snapshot") {
		t.Errorf("expected 'indexed snapshot' annotation, got: %s", text)
	}
	if strings.Contains(text, "live read") {
		t.Errorf("unexpected 'live read' annotation in chunk path: %s", text)
	}
}

// ---------------------------------------------------------------------------
// handleNode --section — fallback live read path
// ---------------------------------------------------------------------------

func TestHandleNode_SectionFallbackLiveRead(t *testing.T) {
	h, st := newTestHandler(t)

	// Write real markdown to disk.
	mdContent := "# Doc\n\n## Intro\n\nThis is live content.\n"
	if err := os.WriteFile(filepath.Join(h.projectRoot, "doc2.md"), []byte(mdContent), 0o644); err != nil {
		t.Fatal(err)
	}

	docNode := store.Node{
		ID: "doc2.md", Kind: "document", Name: "Doc2", QualifiedName: "doc2.md",
		FilePath: "doc2.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1,
	}
	headingNode := store.Node{
		ID: "doc2.md#intro", Kind: "heading", Name: "Intro", QualifiedName: "doc2.md#Intro",
		FilePath: "doc2.md", StartLine: 3, EndLine: 5, Level: 2, UpdatedAt: 1,
	}
	if err := st.InsertNodes([]store.Node{docNode, headingNode}); err != nil {
		t.Fatal(err)
	}
	// No chunk upserted — triggers live read fallback.

	res, err := callTool(h, h.handleNode, map[string]interface{}{
		"document": "doc2.md",
		"section":  "Intro",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)
	if !strings.Contains(text, "live read") {
		t.Errorf("expected 'live read' annotation in fallback output, got: %s", text)
	}
}

// ---------------------------------------------------------------------------
// handleStatus — reindex_required shows scope (single store mode)
// ---------------------------------------------------------------------------

func TestHandleStatus_ReindexScopeShown(t *testing.T) {
	h, st := newTestHandler(t)
	if err := st.UpsertProjectMeta(store.MetaKeyReindexRequired, "true"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertProjectMeta(store.MetaKeyReindexScope, "sections"); err != nil {
		t.Fatal(err)
	}

	res, err := callTool(h, h.handleStatus, map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)
	if !strings.Contains(text, "Reindex required: yes") {
		t.Errorf("expected 'Reindex required: yes', got: %s", text)
	}
	if !strings.Contains(text, "Scope: sections") {
		t.Errorf("expected 'Scope: sections', got: %s", text)
	}
}

// ---------------------------------------------------------------------------
// handleStatus — reindex_required with missing scope defaults to "unknown"
// ---------------------------------------------------------------------------

func TestHandleStatus_ReindexScopeDefaultsToUnknown(t *testing.T) {
	h, st := newTestHandler(t)
	if err := st.UpsertProjectMeta(store.MetaKeyReindexRequired, "true"); err != nil {
		t.Fatal(err)
	}
	// Clear any scope that migrations may have set automatically.
	if err := st.DeleteProjectMeta(store.MetaKeyReindexScope, store.MetaKeyReindexReason); err != nil {
		t.Fatal(err)
	}

	res, err := callTool(h, h.handleStatus, map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)
	if !strings.Contains(text, "Reindex required: yes") {
		t.Errorf("expected 'Reindex required: yes', got: %s", text)
	}
	if !strings.Contains(text, "Scope: unknown") {
		t.Errorf("expected 'Scope: unknown' as default, got: %s", text)
	}
}

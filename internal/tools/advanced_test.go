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

func TestHandleContext_ContextPackIncludesEvidenceAndImpact(t *testing.T) {
	h, st := newTestHandler(t)

	nodes := []store.Node{
		{
			ID: "policy.md", Kind: "document", Name: "Incident Response Policy", QualifiedName: "policy.md",
			FilePath: "policy.md", StartLine: 1, EndLine: 20, BodyExcerpt: "incident response policy evidence", UpdatedAt: 1,
		},
		{
			ID: "source.md", Kind: "document", Name: "Primary Source", QualifiedName: "source.md",
			FilePath: "source.md", StartLine: 1, EndLine: 5, BodyExcerpt: "source material", UpdatedAt: 1,
		},
		{
			ID: "dependent.md", Kind: "document", Name: "Dependent Doc", QualifiedName: "dependent.md",
			FilePath: "dependent.md", StartLine: 1, EndLine: 8, BodyExcerpt: "operational dependency", UpdatedAt: 1,
		},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSectionChunks([]store.SectionChunk{{
		NodeID:      "policy.md",
		FilePath:    "policy.md",
		StartLine:   1,
		EndLine:     20,
		ContentHash: "content-hash-policy",
		SectionHash: "section-hash-policy",
		HeadingPath: "",
		Text:        "# Incident Response Policy\n\nincident response policy evidence",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertEdges([]store.Edge{
		{Source: "dependent.md", Target: "policy.md", Kind: "references", Line: 4},
		{Source: "policy.md", Target: "source.md", Kind: "references", Line: 6},
		{Source: "policy.md", Target: "policy.md", Kind: "links_external", Metadata: `{"url":"https://example.test/evidence"}`, Line: 7},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertGovernanceMetadata("policy.md", []store.MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "effective_date", Value: "2025-01-01", ValueType: "date", Source: "frontmatter"},
		{Key: "canonical_source", Value: "true", ValueType: "bool", Source: "frontmatter"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertResearchMetadata("policy.md", []store.MetadataTuple{
		{Key: "confidence", Value: "high", ValueType: "string", Source: "frontmatter"},
		{Key: "source_type", Value: "primary", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatal(err)
	}

	res, err := callTool(h, h.handleContext, map[string]interface{}{
		"task":           "incident response",
		"format":         "context_pack",
		"maxNodes":       float64(1),
		"referenceLimit": float64(5),
		"impactDepth":    float64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}

	text := extractText(res)
	for _, want := range []string{
		"docgraph.context_pack.v1",
		"content-hash-policy",
		"section-hash-policy",
		"**Status:** approved",
		"**Effective date:** 2025-01-01",
		"**Confidence:** high",
		"**Source type:** primary",
		"Dependent Doc (dependent.md) --references--> Incident Response Policy (policy.md) [line 4]",
		"Incident Response Policy (policy.md) --links_external--> https://example.test/evidence [line 7]",
		"### Impacted Documents",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in context pack, got:\n%s", want, text)
		}
	}
	if strings.Contains(text, "live read") {
		t.Errorf("context packs should not use live file reads, got:\n%s", text)
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
// handleNode --section — slug anchor resolves to heading (N-1 friction fix)
// ---------------------------------------------------------------------------

func TestHandleNode_SectionBySlugAnchor(t *testing.T) {
	h, st := newTestHandler(t)

	docNode := store.Node{
		ID: "doc.md", Kind: "document", Name: "Doc", QualifiedName: "doc.md",
		FilePath: "doc.md", StartLine: 1, EndLine: 20, BodyExcerpt: "body", UpdatedAt: 1,
	}
	// Heading whose slug differs from its display name (spaces, casing, punctuation).
	headingNode := store.Node{
		ID: "doc.md#neural-embeddings-agent-driven", Kind: "heading",
		Name: "Neural Embeddings (agent-driven)", QualifiedName: "doc.md#Neural Embeddings (agent-driven)",
		FilePath: "doc.md", StartLine: 3, EndLine: 10, Level: 1, UpdatedAt: 1,
	}
	if err := st.InsertNodes([]store.Node{docNode, headingNode}); err != nil {
		t.Fatal(err)
	}
	chunk := store.SectionChunk{
		NodeID: "doc.md#neural-embeddings-agent-driven", FilePath: "doc.md",
		StartLine: 3, EndLine: 10, Text: "## Neural Embeddings\nVectors pushed back via the tool.",
	}
	if err := st.UpsertSectionChunks([]store.SectionChunk{chunk}); err != nil {
		t.Fatal(err)
	}

	// Pass the slug anchor exactly as it appears in search results — not the raw heading text.
	res, err := callTool(h, h.handleNode, map[string]interface{}{
		"document": "doc.md",
		"section":  "neural-embeddings-agent-driven",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error resolving section by slug: %v", res.Content)
	}
	if text := extractText(res); !strings.Contains(text, "Vectors pushed back") {
		t.Errorf("expected section content resolved via slug anchor, got: %s", text)
	}
}

// ---------------------------------------------------------------------------
// handleSimilar — empty result returns actionable guidance (F-4 expectation gap)
// ---------------------------------------------------------------------------

func TestHandleSimilar_EmptyGivesGuidance(t *testing.T) {
	h, st := newTestHandler(t)

	docNode := store.Node{
		ID: "unique.md", Kind: "document", Name: "Unique", QualifiedName: "unique.md",
		FilePath: "unique.md", StartLine: 1, EndLine: 5, BodyExcerpt: "body", UpdatedAt: 1,
	}
	if err := st.InsertNodes([]store.Node{docNode}); err != nil {
		t.Fatal(err)
	}

	res, err := callTool(h, h.handleSimilar, map[string]interface{}{"document": "unique.md"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)
	if !strings.Contains(text, "Found 0 similar documents") {
		t.Errorf("expected zero-count line, got: %s", text)
	}
	if !strings.Contains(text, "operation=incoming/outgoing") {
		t.Errorf("expected actionable guidance pointing to docgraph_graph, got: %s", text)
	}
}


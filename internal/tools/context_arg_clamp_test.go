package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

// TestHandleContext_ReferenceLimitClampedAtRetrieval pins the H-24 contract:
// handleContext must clamp the user-facing referenceLimit arg at RETRIEVAL
// (getIntArgClamped(args,"referenceLimit",5,1,20)) before handing it to the
// context-pack renderer — not rely solely on the sink's own re-clamp in
// context_pack.go's normalized().
//
// Why referenceLimit == 0 is the discriminating input:
//   - Retrieval floor (getIntArgClamped, this fix): 0 -> 1. The renderer's
//     sink then sees ReferenceLimit=1 and renders at most 1 reference, emitting
//     "... N more incoming references omitted".
//   - If someone reverts to the raw getIntArg, retrieval yields 0, and the
//     sink's normalized() applies "if ReferenceLimit <= 0 { = 5 }", inflating
//     it back to 5 — so a hub with 2 incoming refs renders BOTH, with NO
//     "omitted" line.
//
// The "... more incoming references omitted" line is therefore the only
// order-independent output that distinguishes retrieval-clamping from
// sink-clamping. The count line ("**Incoming references:** 2") is len(incoming)
// and prints identically under both the fix and a revert, so it is used only
// as a sanity check, never as the discriminator.
//
// DELIBERATE, ADVISOR-APPROVED DECISION: an out-of-contract referenceLimit of 0
// is floored to the documented minimum (1), NOT inflated to the default (5).
// Out-of-contract input is clamped into range, not reinterpreted as "unset".
func TestHandleContext_ReferenceLimitClampedAtRetrieval(t *testing.T) {
	h, st := newTestHandler(t)

	// One hub document with exactly two DISTINCT incoming references. The task
	// term lives in BodyExcerpt so FTS (indexed on InsertNodes) returns it. The
	// two referrer nodes (a.md, b.md) must exist because the edges table has a
	// FOREIGN KEY on both source and target -> nodes(id).
	if err := st.InsertNodes([]store.Node{
		{
			ID: "hub.md", Kind: "document", Name: "Hub Doc", QualifiedName: "hub.md",
			FilePath: "hub.md", StartLine: 1, EndLine: 10,
			BodyExcerpt: "clamp contract fixture", UpdatedAt: 1,
		},
		{
			ID: "a.md", Kind: "document", Name: "Referrer A", QualifiedName: "a.md",
			FilePath: "a.md", StartLine: 1, EndLine: 3, BodyExcerpt: "referrer a", UpdatedAt: 1,
		},
		{
			ID: "b.md", Kind: "document", Name: "Referrer B", QualifiedName: "b.md",
			FilePath: "b.md", StartLine: 1, EndLine: 3, BodyExcerpt: "referrer b", UpdatedAt: 1,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertEdges([]store.Edge{
		{Source: "a.md", Target: "hub.md", Kind: "references", Line: 1},
		{Source: "b.md", Target: "hub.md", Kind: "references", Line: 2},
	}); err != nil {
		t.Fatal(err)
	}

	res, err := callTool(h, h.handleContext, map[string]any{
		"task":           "clamp contract fixture",
		"format":         "context_pack",
		"maxNodes":       float64(1),
		"referenceLimit": float64(0), // out-of-contract: must floor to 1, not reset to 5
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)

	// Sanity: the hub really does have 2 incoming references in the fixture.
	if !strings.Contains(text, "**Incoming references:** 2") {
		t.Fatalf("fixture sanity check failed: expected 2 incoming references, got:\n%s", text)
	}

	// Discriminator: with the retrieval floor (0 -> 1) the renderer shows at
	// most 1 reference and reports the remaining one as omitted. If retrieval
	// clamping is removed, the sink resets 0 -> 5, both refs render, and this
	// line disappears.
	if !strings.Contains(text, "more incoming references omitted") {
		t.Errorf("expected referenceLimit to be clamped to 1 at retrieval (one reference omitted), got:\n%s", text)
	}
}

func TestHandleContext_PayloadOverflowNotice(t *testing.T) {
	h, st := newTestHandler(t)

	// Insert 30 documents so that total rendered output exceeds the 20 KB budget.
	// Each doc renders to ~1.1 KB (BodyExcerpt capped at 500 B by store.InsertNodes,
	// no chunk/governance metadata in synthetic nodes); 30 × 1.1 KB ≈ 33 KB > 20 KB threshold.
	bigExcerpt := strings.Repeat("authentication access control token credential session bearer oauth ", 7)
	nodes := make([]store.Node, 30)
	for i := range nodes {
		nodes[i] = store.Node{
			ID:            fmt.Sprintf("overflow%d.md", i),
			Kind:          "document",
			Name:          fmt.Sprintf("Auth Doc %d", i),
			QualifiedName: fmt.Sprintf("overflow%d.md", i),
			FilePath:      fmt.Sprintf("overflow%d.md", i),
			StartLine:     1, EndLine: 50,
			BodyExcerpt: bigExcerpt,
			UpdatedAt:   1,
		}
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}

	res, err := callTool(h, h.handleContext, map[string]any{
		"task":     "authentication access control token credential session bearer oauth",
		"maxNodes": float64(30),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)
	t.Logf("overflow test: output size = %d bytes", len(text))

	if !strings.Contains(text, "Response budget reached") {
		t.Errorf("expected truncation notice, got output of %d bytes:\n%s", len(text), text)
	}
	if !strings.Contains(text, "omitted") {
		t.Errorf("expected omitted count in truncation notice; got:\n%s", text)
	}
	// At least 1 doc must have been rendered before the notice.
	if !strings.Contains(text, "Auth Doc 0") {
		t.Errorf("expected at least the first document to be rendered before truncation; got:\n%s", text)
	}
}

func TestHandleContext_PayloadOverflowNotice_SmallQuery(t *testing.T) {
	h, st := newTestHandler(t)

	bigExcerpt := strings.Repeat("authentication access control token credential session bearer oauth ", 110)
	// Only 3 docs — well below the 20 KB budget.
	nodes := []store.Node{
		{ID: "small0.md", Kind: "document", Name: "Small Doc 0", QualifiedName: "small0.md",
			FilePath: "small0.md", StartLine: 1, EndLine: 10, BodyExcerpt: bigExcerpt, UpdatedAt: 1},
		{ID: "small1.md", Kind: "document", Name: "Small Doc 1", QualifiedName: "small1.md",
			FilePath: "small1.md", StartLine: 1, EndLine: 10, BodyExcerpt: bigExcerpt, UpdatedAt: 1},
		{ID: "small2.md", Kind: "document", Name: "Small Doc 2", QualifiedName: "small2.md",
			FilePath: "small2.md", StartLine: 1, EndLine: 10, BodyExcerpt: bigExcerpt, UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}

	res, err := callTool(h, h.handleContext, map[string]any{
		"task":     "authentication access control token credential session bearer oauth",
		"maxNodes": float64(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)
	t.Logf("small query test: output size = %d bytes", len(text))

	if strings.Contains(text, "Response budget reached") {
		t.Errorf("unexpected truncation notice for small (3-doc) response of %d bytes", len(text))
	}
	// All 3 docs must appear.
	for i := range 3 {
		docName := fmt.Sprintf("Small Doc %d", i)
		if !strings.Contains(text, docName) {
			t.Errorf("expected %q in output; got:\n%s", docName, text)
		}
	}
}

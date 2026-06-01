package tools

import (
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

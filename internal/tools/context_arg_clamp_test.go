package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

// TestHandleContext_ReferenceLimitClampedAtRetrieval pins the reference-limit retrieval-clamp contract:
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
	// Notice now reports head/tail split instead of a bare "omitted" count.
	if !strings.Contains(text, "showing full content for") {
		t.Errorf("expected head/tail count in truncation notice; got:\n%s", text)
	}
	// At least 1 doc must have been rendered before the notice.
	if !strings.Contains(text, "Auth Doc 0") {
		t.Errorf("expected at least the first document to be rendered before truncation; got:\n%s", text)
	}
}

// TestHandleContext_PayloadOverflowListsOmitted pins the "list the tail, don't
// drop it" contract. On payload overflow, handleContext degrades the tail to
// stub lines (path + refs only) instead of dropping the remaining documents.
//
// Mutation check: the assertions on the omitted-tail FilePaths FAIL if Change 1
// is reverted to a bare `break`, because the bare break erases every doc in
// deduped[i+1:] — their paths never reach the output. All 30 docs share the same
// BodyExcerpt (identical FTS score), so which docs land in the tail is rowid /
// tie-break dependent; asserting that ALL 30 FilePaths appear is therefore
// order-independent — under the fix every doc is either head-rendered or
// tail-stubbed, under a bare break the ~dozen tail paths vanish regardless of order.
func TestHandleContext_PayloadOverflowListsOmitted(t *testing.T) {
	h, st := newTestHandler(t)

	// Same fixture as TestHandleContext_PayloadOverflowNotice: 30 docs, broad query,
	// crossing the 20 KB maxContextResponseBytes cap so the tail overflows.
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
	t.Logf("overflow-lists-omitted test: output size = %d bytes", len(text))

	// (a) The overflow notice still fires.
	if !strings.Contains(text, "Response budget reached") {
		t.Errorf("expected truncation notice; got output of %d bytes:\n%s", len(text), text)
	}
	// (b) The stub-tail marker is present.
	if !strings.Contains(text, "path + refs only") {
		t.Errorf("expected stub-tail marker 'path + refs only'; got:\n%s", text)
	}
	// (c) MUTATION CHECK: every matched document's path survives — head-rendered
	// or tail-stubbed. A bare `break` erases the tail paths and this fails.
	for i := range 30 {
		fp := fmt.Sprintf("overflow%d.md", i)
		if !strings.Contains(text, fp) {
			t.Errorf("expected omitted-tail document %q to be listed (path must survive overflow); got:\n%s", fp, text)
		}
	}
}

// TestHandleContext_PayloadOverflowTailBounded pins the Codex/advisor follow-up:
// the stub tail is byte-capped (maxTailStubBytes), so a large maxNodes cannot
// append ~200 stub lines and ~double the payload. Stubs past the cap are NOT
// dropped silently — they are disclosed as a trailing "…and N more not listed"
// count. This bounds the stub LIST, not the whole response (a single huge-heading
// doc can still overshoot the head budget — out of scope here; the fixture uses
// small bodies so the head overshoot is a single ~1 KB doc).
//
// Mutation check: remove the `sb.Len()-tailStart >= maxTailStubBytes` cap and the
// envelope assertion FAILS (200 long-path stubs ≈ 26 KB tail → ~46 KB total).
func TestHandleContext_PayloadOverflowTailBounded(t *testing.T) {
	h, st := newTestHandler(t)

	// 200 docs with long paths AND names so an uncapped tail would blow the
	// payload. BodyExcerpt is capped at 500 B by InsertNodes, so head doc size is
	// bounded and the head overshoot is a single ~1 KB doc.
	bigExcerpt := strings.Repeat("authentication access control token credential session bearer oauth ", 7)
	const n = 200
	nodes := make([]store.Node, n)
	for i := range nodes {
		id := fmt.Sprintf("very/long/nested/workspace/path/segment/for/overflow/document-%03d.md", i)
		nodes[i] = store.Node{
			ID:            id,
			Kind:          "document",
			Name:          fmt.Sprintf("Authentication and Access Control Reference Document Number %03d", i),
			QualifiedName: id,
			FilePath:      id,
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
		"maxNodes": float64(n),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)
	t.Logf("tail-bounded test: output size = %d bytes (%d docs)", len(text), n)

	// Overflow fired and the stub-tail marker is present.
	if !strings.Contains(text, "Response budget reached") {
		t.Errorf("expected overflow notice; got %d bytes", len(text))
	}
	if !strings.Contains(text, "path + refs only") {
		t.Errorf("expected stub-tail marker 'path + refs only'; got %d bytes", len(text))
	}
	// The tail was capped: the trailing disclosure of un-listed docs must appear.
	if !strings.Contains(text, "more not listed") {
		t.Errorf("expected capped-tail disclosure '…and N more not listed' (tail not bounded?); got %d bytes", len(text))
	}
	// ENVELOPE: head (≤ cap + one overshoot doc) + capped tail + notices — bounded,
	// NOT the ~46 KB an uncapped 200-long-path tail would produce.
	const envelope = maxContextResponseBytes + maxTailStubBytes + 8*1024
	if len(text) > envelope {
		t.Errorf("response %d bytes exceeds bounded envelope %d — stub tail not byte-capped", len(text), envelope)
	}
}

// TestHandleContext_PayloadOverflowOversizedStubBounded pins the Codex re-review
// finding: a SINGLE tail stub can exceed maxTailStubBytes if FilePath/Name is long
// (untrusted — a malicious heading). The cap must be enforced BEFORE appending each
// stub, so one oversized stub is counted, not written, and the envelope still holds.
//
// Fixture: 40 strong-match filler docs fill the head; 1 weak-match doc with a 24 KB
// Name (> maxContextResponseBytes) ranks last → lands in the tail. With the
// before-append cap it is skipped (disclosed in the count); with the old
// check-at-start cap it is written whole and the response blows past the envelope.
func TestHandleContext_PayloadOverflowOversizedStubBounded(t *testing.T) {
	h, st := newTestHandler(t)

	bigExcerpt := strings.Repeat("authentication access control token credential session bearer oauth ", 7)
	nodes := make([]store.Node, 41)
	for i := range 40 {
		nodes[i] = store.Node{
			ID:            fmt.Sprintf("filler%02d.md", i),
			Kind:          "document",
			Name:          fmt.Sprintf("Auth Filler %02d", i),
			QualifiedName: fmt.Sprintf("filler%02d.md", i),
			FilePath:      fmt.Sprintf("filler%02d.md", i),
			StartLine:     1, EndLine: 50,
			BodyExcerpt: bigExcerpt,
			UpdatedAt:   1,
		}
	}
	// Weak match (one term, once) so it ranks LAST → tail; Name alone exceeds the cap.
	nodes[40] = store.Node{
		ID:            "oversized-name-doc.md",
		Kind:          "document",
		Name:          strings.Repeat("Z", 24*1024),
		QualifiedName: "oversized-name-doc.md",
		FilePath:      "oversized-name-doc.md",
		StartLine:     1, EndLine: 50,
		BodyExcerpt: "authentication",
		UpdatedAt:   1,
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}

	res, err := callTool(h, h.handleContext, map[string]any{
		"task":     "authentication access control token credential session bearer oauth",
		"maxNodes": float64(41),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	text := extractText(res)
	t.Logf("oversized-stub test: output size = %d bytes", len(text))

	if !strings.Contains(text, "Response budget reached") {
		t.Errorf("expected overflow notice; got %d bytes", len(text))
	}
	// The oversized stub must NOT have been written whole, so the response stays
	// within the envelope. The before-append cap guarantees this; the old
	// check-at-start cap writes the 24 KB stub and FAILS here.
	const envelope = maxContextResponseBytes + maxTailStubBytes + 8*1024
	if len(text) > envelope {
		t.Errorf("response %d bytes exceeds envelope %d — a single oversized stub overshot the tail cap", len(text), envelope)
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

package tools

import (
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/Detective-XH/docgraph/internal/workspace"
)

// ---------------------------------------------------------------------------
// Tag seeding helper
//
// Each (doc, tag) pair needs a unique tag-node ID; the store counts tag nodes
// by name to derive the document count (GetAllTags counts kind='tag' rows).
// Pattern mirrors internal/store/store_test.go:752.
// ---------------------------------------------------------------------------

func seedTaggedDocs(t *testing.T, st *store.Store, docs []store.Node, tagAssignments map[string][]string) {
	t.Helper()
	allNodes := make([]store.Node, 0, len(docs))
	allNodes = append(allNodes, docs...)
	var allEdges []store.Edge

	for _, doc := range docs {
		tags, ok := tagAssignments[doc.ID]
		if !ok {
			continue
		}
		for _, tagName := range tags {
			tagID := doc.ID + "#tag:" + tagName
			allNodes = append(allNodes, store.Node{
				ID:            tagID,
				Kind:          "tag",
				Name:          tagName,
				QualifiedName: tagID,
				FilePath:      doc.FilePath,
				UpdatedAt:     1,
			})
			allEdges = append(allEdges, store.Edge{
				Source: doc.ID,
				Target: tagID,
				Kind:   "tagged",
			})
		}
	}

	if err := st.InsertNodes(allNodes); err != nil {
		t.Fatalf("seedTaggedDocs InsertNodes: %v", err)
	}
	if len(allEdges) > 0 {
		if err := st.InsertEdges(allEdges); err != nil {
			t.Fatalf("seedTaggedDocs InsertEdges: %v", err)
		}
	}
}

// ---------------------------------------------------------------------------
// TestHandleTagList_NoWorkspace_SortsByCount
// ---------------------------------------------------------------------------

// TestHandleTagList_NoWorkspace_SortsByCount seeds two docs tagged "shared"
// and one doc tagged "alpha". Expects "shared" (count 2) before "alpha" (count 1).
func TestHandleTagList_NoWorkspace_SortsByCount(t *testing.T) {
	h, st := newTestHandler(t)

	docs := []store.Node{
		{ID: "p1.md", Kind: "document", Name: "Policy 1", QualifiedName: "p1.md", FilePath: "p1.md", UpdatedAt: 1},
		{ID: "p2.md", Kind: "document", Name: "Policy 2", QualifiedName: "p2.md", FilePath: "p2.md", UpdatedAt: 1},
	}
	seedTaggedDocs(t, st, docs, map[string][]string{
		"p1.md": {"shared", "alpha"},
		"p2.md": {"shared"},
	})

	res, err := callTool(h, h.handleTags, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", extractText(res))
	}
	text := extractText(res)

	if !strings.Contains(text, "## Tags") {
		t.Errorf("missing '## Tags' header, got:\n%s", text)
	}
	idxShared := strings.Index(text, "shared")
	idxAlpha := strings.Index(text, "alpha")
	if idxShared < 0 {
		t.Fatalf("tag 'shared' not found in output:\n%s", text)
	}
	if idxAlpha < 0 {
		t.Fatalf("tag 'alpha' not found in output:\n%s", text)
	}
	if idxShared > idxAlpha {
		t.Errorf("expected 'shared' (count 2) to appear before 'alpha' (count 1):\n%s", text)
	}
	// Check count rendering
	if !strings.Contains(text, "shared") || !strings.Contains(text, "2 docs") {
		t.Errorf("expected 'shared' shown with count 2, got:\n%s", text)
	}
}

// ---------------------------------------------------------------------------
// TestHandleTagList_Empty
// ---------------------------------------------------------------------------

func TestHandleTagList_Empty(t *testing.T) {
	h, _ := newTestHandler(t)

	res, err := callTool(h, h.handleTags, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", extractText(res))
	}
	text := extractText(res)
	if !strings.Contains(text, "No tags found") {
		t.Errorf("expected 'No tags found', got:\n%s", text)
	}
}

// ---------------------------------------------------------------------------
// TestHandleTagList_WorkspaceMergesCounts
// ---------------------------------------------------------------------------

// TestHandleTagList_WorkspaceMergesCounts seeds "shared" into both alpha and
// beta stores (count 1 each). The workspace fan-out branch must merge them to
// a combined count of 2.
func TestHandleTagList_WorkspaceMergesCounts(t *testing.T) {
	_, stA := newTestHandler(t)
	_, stB := newTestHandler(t)

	docsA := []store.Node{
		{ID: "a.md", Kind: "document", Name: "A", QualifiedName: "a.md", FilePath: "a.md", UpdatedAt: 1},
	}
	seedTaggedDocs(t, stA, docsA, map[string][]string{"a.md": {"shared"}})

	docsB := []store.Node{
		{ID: "b.md", Kind: "document", Name: "B", QualifiedName: "b.md", FilePath: "b.md", UpdatedAt: 1},
	}
	seedTaggedDocs(t, stB, docsB, map[string][]string{"b.md": {"shared"}})

	h := &handler{workspace: &workspace.Workspace{Projects: []*workspace.Project{
		{Name: "alpha", Path: t.TempDir(), Store: stA},
		{Name: "beta", Path: t.TempDir(), Store: stB},
	}}}

	res, err := callTool(h, h.handleTags, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", extractText(res))
	}
	text := extractText(res)

	if !strings.Contains(text, "## Tags") {
		t.Errorf("missing '## Tags' header:\n%s", text)
	}
	if !strings.Contains(text, "shared") {
		t.Fatalf("tag 'shared' not found in workspace output:\n%s", text)
	}
	// Merged count must be 2.
	if !strings.Contains(text, "2 docs") {
		t.Errorf("expected merged count '2 docs' for tag 'shared', got:\n%s", text)
	}
}

// ---------------------------------------------------------------------------
// TestHandleTagFilter_ListsDocuments
// ---------------------------------------------------------------------------

func TestHandleTagFilter_ListsDocuments(t *testing.T) {
	h, st := newTestHandler(t)

	docs := []store.Node{
		{ID: "policy.md", Kind: "document", Name: "Policy", QualifiedName: "policy.md", FilePath: "policy.md", UpdatedAt: 1},
	}
	seedTaggedDocs(t, st, docs, map[string][]string{"policy.md": {"shared"}})

	res, err := callTool(h, h.handleTags, map[string]any{"tag": "shared"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", extractText(res))
	}
	text := extractText(res)

	if !strings.Contains(text, `## Tag: "shared"`) {
		t.Errorf("expected tag header, got:\n%s", text)
	}
	if !strings.Contains(text, "Policy") {
		t.Errorf("expected doc name 'Policy', got:\n%s", text)
	}
	if !strings.Contains(text, "policy.md") {
		t.Errorf("expected doc path 'policy.md', got:\n%s", text)
	}
}

// ---------------------------------------------------------------------------
// TestHandleTagFilter_NotFound
// ---------------------------------------------------------------------------

func TestHandleTagFilter_NotFound(t *testing.T) {
	h, _ := newTestHandler(t)

	res, err := callTool(h, h.handleTags, map[string]any{"tag": "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", extractText(res))
	}
	text := extractText(res)
	if !strings.Contains(text, "No documents found with this tag.") {
		t.Errorf("expected not-found message, got:\n%s", text)
	}
}

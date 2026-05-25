package resolver

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

// setupProjectStore creates an in-memory-backed store in a temp dir and
// inserts the provided nodes. Cleanup is registered via t.Cleanup.
func setupProjectStore(t *testing.T, nodes []store.Node) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	return st
}

// TestResolveWorkspaceCrossProject verifies that [[project-b/doc-b]] in
// project-a resolves to a wikilinks_to self-edge carrying cross-project
// metadata pointing at project-b's doc-b node.
func TestResolveWorkspaceCrossProject(t *testing.T) {
	// Project A: has doc-a.md with a [[project-b/doc-b]] wikilink ref
	stA := setupProjectStore(t, []store.Node{
		{
			ID: "doc-a.md", Kind: "document", Name: "Document A",
			QualifiedName: "doc-a.md", FilePath: "doc-a.md",
			StartLine: 1, EndLine: 10, UpdatedAt: 1,
		},
	})

	// Project B: has doc-b.md
	stB := setupProjectStore(t, []store.Node{
		{
			ID: "doc-b.md", Kind: "document", Name: "Document B",
			QualifiedName: "doc-b.md", FilePath: "doc-b.md",
			StartLine: 1, EndLine: 10, UpdatedAt: 1,
		},
	})

	// Insert cross-project wikilink ref into project A's unresolved_refs.
	// (In production this is left unresolved after per-project Resolve()
	// because fuzzyResolve key "project-b/doc-b" has no basename match.)
	refsA := []store.UnresolvedRef{
		{
			FromNodeID:    "doc-a.md",
			ReferenceText: "project-b/doc-b",
			ReferenceKind: "wikilink",
			Line:          3,
			Col:           1,
			FilePath:      "doc-a.md",
		},
	}
	if err := stA.InsertUnresolvedRefs(refsA); err != nil {
		t.Fatal(err)
	}

	projects := []ProjectRef{
		{Name: "project-a", Store: stA},
		{Name: "project-b", Store: stB},
	}

	if err := ResolveWorkspace(projects); err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}

	// Verify project-a has a wikilinks_to self-edge with cross-project metadata
	edges, err := stA.GetOutgoingEdges("doc-a.md")
	if err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, e := range edges {
		if e.Kind != "wikilinks_to" {
			continue
		}
		// Self-edge: source == target
		if e.Source != "doc-a.md" || e.Target != "doc-a.md" {
			continue
		}
		var meta map[string]string
		if err := json.Unmarshal([]byte(e.Metadata), &meta); err != nil {
			t.Fatalf("unmarshal edge metadata: %v", err)
		}
		if meta["cross_project"] != "true" {
			continue
		}
		if meta["target_project"] != "project-b" {
			t.Errorf("expected target_project=project-b, got %q", meta["target_project"])
		}
		if meta["target_node_id"] != "doc-b.md" {
			t.Errorf("expected target_node_id=doc-b.md, got %q", meta["target_node_id"])
		}
		found = true
		break
	}
	if !found {
		t.Error("expected cross-project wikilinks_to self-edge in project-a, not found")
	}

	// No unresolved refs should remain in project-a
	remaining, err := stA.GetUnresolvedRefs()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Errorf("expected 0 unresolved refs in project-a, got %d", len(remaining))
	}
}

// TestResolveWorkspaceSameProjectUnaffected verifies that intra-project wikilinks
// in the unresolved_refs table are NOT consumed by ResolveWorkspace — they remain
// unresolved (or would have been handled by the prior per-project Resolve pass).
func TestResolveWorkspaceSameProjectUnaffected(t *testing.T) {
	stA := setupProjectStore(t, []store.Node{
		{
			ID: "doc-a.md", Kind: "document", Name: "Document A",
			QualifiedName: "doc-a.md", FilePath: "doc-a.md",
			StartLine: 1, EndLine: 10, UpdatedAt: 1,
		},
	})

	// A plain same-project wikilink (no slash) that remains unresolved
	refsA := []store.UnresolvedRef{
		{
			FromNodeID:    "doc-a.md",
			ReferenceText: "other-doc",
			ReferenceKind: "wikilink",
			Line:          2,
			Col:           1,
			FilePath:      "doc-a.md",
		},
	}
	if err := stA.InsertUnresolvedRefs(refsA); err != nil {
		t.Fatal(err)
	}

	projects := []ProjectRef{
		{Name: "project-a", Store: stA},
	}

	if err := ResolveWorkspace(projects); err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}

	// The intra-project ref should still be in unresolved_refs (not consumed)
	remaining, err := stA.GetUnresolvedRefs()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 {
		t.Errorf("expected 1 unresolved ref (intra-project) to remain, got %d", len(remaining))
	}
	if len(remaining) > 0 && remaining[0].ReferenceText != "other-doc" {
		t.Errorf("expected unresolved ref 'other-doc', got %q", remaining[0].ReferenceText)
	}

	// And no spurious edge should have been created
	edges, err := stA.GetOutgoingEdges("doc-a.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range edges {
		if e.Kind == "wikilinks_to" {
			t.Errorf("unexpected wikilinks_to edge created for intra-project ref: %+v", e)
		}
	}
}

// TestResolveWorkspaceNonexistentProject verifies that a ref pointing to a
// project that doesn't exist remains in unresolved_refs.
func TestResolveWorkspaceNonexistentProject(t *testing.T) {
	stA := setupProjectStore(t, []store.Node{
		{
			ID: "doc-a.md", Kind: "document", Name: "Document A",
			QualifiedName: "doc-a.md", FilePath: "doc-a.md",
			StartLine: 1, EndLine: 10, UpdatedAt: 1,
		},
	})

	refsA := []store.UnresolvedRef{
		{
			FromNodeID:    "doc-a.md",
			ReferenceText: "nonexistent/doc",
			ReferenceKind: "wikilink",
			Line:          5,
			Col:           1,
			FilePath:      "doc-a.md",
		},
	}
	if err := stA.InsertUnresolvedRefs(refsA); err != nil {
		t.Fatal(err)
	}

	projects := []ProjectRef{
		{Name: "project-a", Store: stA},
	}

	if err := ResolveWorkspace(projects); err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}

	remaining, err := stA.GetUnresolvedRefs()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 {
		t.Errorf("expected 1 unresolved ref for nonexistent project, got %d", len(remaining))
	}
}

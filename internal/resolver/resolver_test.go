package resolver

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

func setupResolverTest(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	// Insert document nodes
	nodes := []store.Node{
		{ID: "README.md", Kind: "document", Name: "README", QualifiedName: "README.md", FilePath: "README.md", StartLine: 1, EndLine: 10, UpdatedAt: 1},
		{ID: "doc-a.md", Kind: "document", Name: "Document A", QualifiedName: "doc-a.md", FilePath: "doc-a.md", StartLine: 1, EndLine: 10, UpdatedAt: 1},
		{ID: "subdir/nested.md", Kind: "document", Name: "Nested", QualifiedName: "subdir/nested.md", FilePath: "subdir/nested.md", StartLine: 1, EndLine: 10, UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	return st
}

// assertEdgeExists asserts that edges contains at least one edge with the given
// target node ID and kind. Uses t.Helper so failures point to the call site.
func assertEdgeExists(t *testing.T, edges []store.Edge, wantTarget, wantKind string) {
	t.Helper()
	for _, e := range edges {
		if e.Target == wantTarget && e.Kind == wantKind {
			return
		}
	}
	t.Errorf("expected edge with target=%q kind=%q, not found", wantTarget, wantKind)
}

func TestResolveMarkdownLink(t *testing.T) {
	t.Run("resolves markdown links to existing documents", func(t *testing.T) {
		st := setupResolverTest(t)

		refs := []store.UnresolvedRef{
			{
				FromNodeID:    "README.md",
				ReferenceText: "doc-a.md",
				ReferenceKind: "markdown_link",
				Line:          5,
				Col:           1,
				FilePath:      "README.md",
			},
			{
				FromNodeID:    "subdir/nested.md",
				ReferenceText: "../README.md",
				ReferenceKind: "markdown_link",
				Line:          3,
				Col:           1,
				FilePath:      "subdir/nested.md",
			},
		}
		if err := st.InsertUnresolvedRefs(refs); err != nil {
			t.Fatal(err)
		}

		if err := Resolve(st); err != nil {
			t.Fatal(err)
		}

		// Verify edges were created
		edges, err := st.GetOutgoingEdges("README.md")
		if err != nil {
			t.Fatal(err)
		}
		assertEdgeExists(t, edges, "doc-a.md", "references")

		edges2, err := st.GetOutgoingEdges("subdir/nested.md")
		if err != nil {
			t.Fatal(err)
		}
		assertEdgeExists(t, edges2, "README.md", "references")

		// Verify no unresolved refs remain
		remaining, err := st.GetUnresolvedRefs()
		if err != nil {
			t.Fatal(err)
		}
		if len(remaining) != 0 {
			t.Errorf("expected 0 unresolved refs, got %d", len(remaining))
		}
	})
}

func TestResolveWikilink(t *testing.T) {
	t.Run("resolves wikilinks by basename", func(t *testing.T) {
		st := setupResolverTest(t)

		// Add doc-b.md node since the shared helper doesn't include it
		extra := []store.Node{
			{ID: "doc-b.md", Kind: "document", Name: "Document B", QualifiedName: "doc-b.md", FilePath: "doc-b.md", StartLine: 1, EndLine: 10, UpdatedAt: 1},
		}
		if err := st.InsertNodes(extra); err != nil {
			t.Fatal(err)
		}

		refs := []store.UnresolvedRef{
			{
				FromNodeID:    "doc-a.md",
				ReferenceText: "doc-b",
				ReferenceKind: "wikilink",
				Line:          2,
				Col:           1,
				FilePath:      "doc-a.md",
			},
			{
				FromNodeID:    "doc-b.md",
				ReferenceText: "doc-a",
				ReferenceKind: "wikilink",
				Line:          4,
				Col:           1,
				FilePath:      "doc-b.md",
			},
		}
		if err := st.InsertUnresolvedRefs(refs); err != nil {
			t.Fatal(err)
		}

		if err := Resolve(st); err != nil {
			t.Fatal(err)
		}

		// Verify wikilinks_to edges
		edges, err := st.GetOutgoingEdges("doc-a.md")
		if err != nil {
			t.Fatal(err)
		}
		assertEdgeExists(t, edges, "doc-b.md", "wikilinks_to")

		edges2, err := st.GetOutgoingEdges("doc-b.md")
		if err != nil {
			t.Fatal(err)
		}
		assertEdgeExists(t, edges2, "doc-a.md", "wikilinks_to")

		// Verify no unresolved refs remain
		remaining, err := st.GetUnresolvedRefs()
		if err != nil {
			t.Fatal(err)
		}
		if len(remaining) != 0 {
			t.Errorf("expected 0 unresolved refs, got %d", len(remaining))
		}
	})
}

func TestResolveExternal(t *testing.T) {
	t.Run("creates self-edge with URL metadata for external links", func(t *testing.T) {
		st := setupResolverTest(t)

		refs := []store.UnresolvedRef{
			{
				FromNodeID:    "README.md",
				ReferenceText: "https://example.com",
				ReferenceKind: "external",
				Line:          7,
				Col:           1,
				FilePath:      "README.md",
			},
		}
		if err := st.InsertUnresolvedRefs(refs); err != nil {
			t.Fatal(err)
		}

		if err := Resolve(st); err != nil {
			t.Fatal(err)
		}

		// External links produce a self-edge with kind "links_external"
		edges, err := st.GetOutgoingEdges("README.md")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range edges {
			if e.Kind == "links_external" && e.Source == "README.md" && e.Target == "README.md" {
				// Verify metadata contains the URL
				var meta map[string]string
				if err := json.Unmarshal([]byte(e.Metadata), &meta); err != nil {
					t.Fatalf("failed to parse edge metadata: %v", err)
				}
				if meta["url"] != "https://example.com" {
					t.Errorf("expected url=https://example.com, got url=%s", meta["url"])
				}
				found = true
				break
			}
		}
		if !found {
			t.Error("expected links_external self-edge for README.md, not found")
		}

		// Verify no unresolved refs remain
		remaining, err := st.GetUnresolvedRefs()
		if err != nil {
			t.Fatal(err)
		}
		if len(remaining) != 0 {
			t.Errorf("expected 0 unresolved refs, got %d", len(remaining))
		}
	})
}

func TestResolveEmbedSkipsNonMd(t *testing.T) {
	t.Run("embed of non-.md file creates no edge", func(t *testing.T) {
		st := setupResolverTest(t)

		refs := []store.UnresolvedRef{
			{
				FromNodeID:    "README.md",
				ReferenceText: "image.png",
				ReferenceKind: "embed",
				Line:          9,
				Col:           1,
				FilePath:      "README.md",
			},
		}
		if err := st.InsertUnresolvedRefs(refs); err != nil {
			t.Fatal(err)
		}

		if err := Resolve(st); err != nil {
			t.Fatal(err)
		}

		// No embeds edge should be created
		edges, err := st.GetOutgoingEdges("README.md")
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edges {
			if e.Kind == "embeds" {
				t.Errorf("unexpected embeds edge: %s -> %s", e.Source, e.Target)
			}
		}

		// The ref should remain unresolved (resolveEmbed returns nil for non-.md)
		remaining, err := st.GetUnresolvedRefs()
		if err != nil {
			t.Fatal(err)
		}
		if len(remaining) != 1 {
			t.Errorf("expected 1 unresolved ref (image.png embed), got %d", len(remaining))
		}
	})
}

func TestUnresolvedRemains(t *testing.T) {
	t.Run("reference to nonexistent target stays unresolved", func(t *testing.T) {
		st := setupResolverTest(t)

		refs := []store.UnresolvedRef{
			{
				FromNodeID:    "README.md",
				ReferenceText: "nonexistent.md",
				ReferenceKind: "markdown_link",
				Line:          1,
				Col:           1,
				FilePath:      "README.md",
			},
		}
		if err := st.InsertUnresolvedRefs(refs); err != nil {
			t.Fatal(err)
		}

		if err := Resolve(st); err != nil {
			t.Fatal(err)
		}

		remaining, err := st.GetUnresolvedRefs()
		if err != nil {
			t.Fatal(err)
		}
		if len(remaining) != 1 {
			t.Fatalf("expected 1 unresolved ref, got %d", len(remaining))
		}
		if remaining[0].ReferenceText != "nonexistent.md" {
			t.Errorf("expected unresolved ref text 'nonexistent.md', got %q", remaining[0].ReferenceText)
		}
	})
}

func TestResolvePathTraversal(t *testing.T) {
	t.Run("path traversal target stays unresolved", func(t *testing.T) {
		st := setupResolverTest(t)

		refs := []store.UnresolvedRef{
			{
				FromNodeID:    "README.md",
				ReferenceText: "../../../../etc/passwd",
				ReferenceKind: "markdown_link",
				Line:          1,
				Col:           1,
				FilePath:      "README.md",
			},
		}
		if err := st.InsertUnresolvedRefs(refs); err != nil {
			t.Fatal(err)
		}

		if err := Resolve(st); err != nil {
			t.Fatal(err)
		}

		// Verify no edge was created with the traversal target
		edges, err := st.GetOutgoingEdges("README.md")
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edges {
			if e.Kind == "references" {
				t.Errorf("unexpected references edge: %s -> %s", e.Source, e.Target)
			}
		}

		// The ref should remain unresolved
		remaining, err := st.GetUnresolvedRefs()
		if err != nil {
			t.Fatal(err)
		}
		if len(remaining) != 1 {
			t.Fatalf("expected 1 unresolved ref, got %d", len(remaining))
		}
		if remaining[0].ReferenceText != "../../../../etc/passwd" {
			t.Errorf("expected unresolved ref text '../../../../etc/passwd', got %q", remaining[0].ReferenceText)
		}
	})
}

// TestResolveNoPanicOnAdversarialInput locks in the audit verdict that
// resolver.Resolve has no panic vector (security-audit backlog #4). Resolve
// runs on the watcher reindex goroutine, so a panic would crash serve. It feeds
// a store seeded with degenerate document nodes (empty name/path, same-basename
// collisions that drive disambiguate) and a batch of malformed unresolved refs
// (empty/anchor-only/unicode/null-byte targets, every reference kind, an
// unknown kind) and asserts Resolve returns without panicking or erroring.
func TestResolveNoPanicOnAdversarialInput(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	// Two docs share a basename in different dirs → exercises disambiguate().
	// One doc has an empty Name and an unusual path.
	nodes := []store.Node{
		{ID: "a/dup.md", Kind: "document", Name: "Dup", QualifiedName: "a/dup.md", FilePath: "a/dup.md", StartLine: 1, EndLine: 10, UpdatedAt: 1},
		{ID: "b/dup.md", Kind: "document", Name: "Dup", QualifiedName: "b/dup.md", FilePath: "b/dup.md", StartLine: 1, EndLine: 10, UpdatedAt: 1},
		{ID: "blank.md", Kind: "document", Name: "", QualifiedName: "blank.md", FilePath: "blank.md", StartLine: 1, EndLine: 10, UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}

	refs := []store.UnresolvedRef{
		{FromNodeID: "blank.md", ReferenceText: "", ReferenceKind: "markdown_link", FilePath: "blank.md"},
		{FromNodeID: "blank.md", ReferenceText: "#only-anchor", ReferenceKind: "markdown_link", FilePath: "blank.md"},
		{FromNodeID: "blank.md", ReferenceText: "dup", ReferenceKind: "wikilink", FilePath: "a/dup.md"},
		{FromNodeID: "blank.md", ReferenceText: "dup|alias#frag", ReferenceKind: "wikilink", FilePath: "b/dup.md"},
		{FromNodeID: "blank.md", ReferenceText: "image.png", ReferenceKind: "embed", FilePath: "blank.md"},
		{FromNodeID: "blank.md", ReferenceText: "https://example.com", ReferenceKind: "external", FilePath: "blank.md"},
		{FromNodeID: "blank.md", ReferenceText: "標題🔥\x00", ReferenceKind: "wikilink", FilePath: "blank.md"},
		{FromNodeID: "blank.md", ReferenceText: "../../../etc/passwd", ReferenceKind: "markdown_link", FilePath: "blank.md"},
		{FromNodeID: "blank.md", ReferenceText: "whatever", ReferenceKind: "unknown_kind", FilePath: "blank.md"},
	}
	if err := st.InsertUnresolvedRefs(refs); err != nil {
		t.Fatal(err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Resolve panicked on adversarial input: %v", r)
		}
	}()
	if err := Resolve(st); err != nil {
		t.Fatalf("Resolve returned error on adversarial input: %v", err)
	}
}

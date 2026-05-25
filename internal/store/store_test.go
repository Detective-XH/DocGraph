package store

import (
	"os"
	"path/filepath"
	"testing"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func testNode(id, kind, name, filePath string) Node {
	return Node{
		ID: id, Kind: kind, Name: name,
		QualifiedName: id, FilePath: filePath,
		StartLine: 1, EndLine: 10, Level: 0,
		BodyExcerpt: "test body", UpdatedAt: 1,
	}
}

func TestOpenClose(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Verify the DB file was created on disk.
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("DB file not created: %v", err)
	}

	if err := st.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestInsertAndSearchNodes(t *testing.T) {
	st := tempStore(t)

	nodes := []Node{
		testNode("doc1.md", "document", "Alpha Report", "doc1.md"),
		testNode("doc1.md#intro", "heading", "Introduction", "doc1.md"),
		testNode("doc1.md#summary", "heading", "Summary Alpha", "doc1.md"),
	}

	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}

	t.Run("search by name", func(t *testing.T) {
		results, err := st.Search("Alpha Report", "", 10)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected at least 1 result, got 0")
		}
		found := false
		for _, r := range results {
			if r.Node.ID == "doc1.md" {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected doc1.md in search results")
		}
	})

	t.Run("search heading", func(t *testing.T) {
		results, err := st.Search("Introduction", "", 10)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected at least 1 result for 'Introduction', got 0")
		}
	})

	t.Run("search with kind filter", func(t *testing.T) {
		results, err := st.Search("Alpha", "heading", 10)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		for _, r := range results {
			if r.Node.Kind != "heading" {
				t.Errorf("expected kind=heading, got %q", r.Node.Kind)
			}
		}
	})
}

func TestFTS5CJK(t *testing.T) {
	st := tempStore(t)

	node := Node{
		ID: "cjk-doc.md", Kind: "document", Name: "CJK Doc",
		QualifiedName: "cjk-doc.md", FilePath: "cjk-doc.md",
		StartLine: 1, EndLine: 10, Level: 0,
		BodyExcerpt: "情報分析結果在此", UpdatedAt: 1,
	}
	if err := st.InsertNodes([]Node{node}); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}

	t.Run("trigram FTS5 4-char query", func(t *testing.T) {
		// "情報分析" is 4 runes (12 bytes), len >= 3 → FTS5 trigram path
		results, err := st.Search("情報分析", "", 10)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected FTS5 trigram match for '情報分析', got 0 results")
		}
		if results[0].Node.ID != "cjk-doc.md" {
			t.Errorf("expected cjk-doc.md, got %q", results[0].Node.ID)
		}
	})

	t.Run("LIKE fallback 2-char query", func(t *testing.T) {
		// "情報" is 2 runes but 6 bytes; Search sees len([]rune) < 3 → LIKE fallback
		results, err := st.Search("情報", "", 10)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected LIKE fallback match for '情報', got 0 results")
		}
		if results[0].Node.ID != "cjk-doc.md" {
			t.Errorf("expected cjk-doc.md, got %q", results[0].Node.ID)
		}
	})
}

func TestDeleteFileData(t *testing.T) {
	st := tempStore(t)

	nodes := []Node{
		testNode("file-a.md", "document", "File A", "file-a.md"),
		testNode("file-a.md#h1", "heading", "Heading One", "file-a.md"),
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}
	if err := st.UpsertFile(FileInfo{
		Path: "file-a.md", ContentHash: "abc", Size: 100,
		ModifiedAt: 1, IndexedAt: 1, NodeCount: 2,
	}); err != nil {
		t.Fatalf("UpsertFile failed: %v", err)
	}

	if err := st.DeleteFileData("file-a.md"); err != nil {
		t.Fatalf("DeleteFileData failed: %v", err)
	}

	// Verify nodes are gone.
	remaining, err := st.GetNodesByFile("file-a.md")
	if err != nil {
		t.Fatalf("GetNodesByFile failed: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("expected 0 nodes after delete, got %d", len(remaining))
	}

	// Verify file record is gone.
	hash, err := st.GetFileHash("file-a.md")
	if err != nil {
		t.Fatalf("GetFileHash failed: %v", err)
	}
	if hash != "" {
		t.Errorf("expected empty hash after delete, got %q", hash)
	}
}

func TestGetStats(t *testing.T) {
	st := tempStore(t)

	nodes := []Node{
		testNode("stats.md", "document", "Stats Doc", "stats.md"),
		testNode("stats.md#a", "heading", "Section A", "stats.md"),
		testNode("stats.md#b", "heading", "Section B", "stats.md"),
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}

	// Edges must reference existing node IDs (FK constraint).
	edges := []Edge{
		{Source: "stats.md", Target: "stats.md#a", Kind: "contains"},
		{Source: "stats.md", Target: "stats.md#b", Kind: "contains"},
	}
	if err := st.InsertEdges(edges); err != nil {
		t.Fatalf("InsertEdges failed: %v", err)
	}

	stats, err := st.GetStats()
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}

	if stats.NodeCount != 3 {
		t.Errorf("expected NodeCount=3, got %d", stats.NodeCount)
	}
	if stats.EdgeCount != 2 {
		t.Errorf("expected EdgeCount=2, got %d", stats.EdgeCount)
	}
	if stats.NodesByKind["document"] != 1 {
		t.Errorf("expected 1 document node, got %d", stats.NodesByKind["document"])
	}
	if stats.NodesByKind["heading"] != 2 {
		t.Errorf("expected 2 heading nodes, got %d", stats.NodesByKind["heading"])
	}
	if stats.EdgesByKind["contains"] != 2 {
		t.Errorf("expected 2 contains edges, got %d", stats.EdgesByKind["contains"])
	}
	if stats.DBSizeBytes <= 0 {
		t.Error("expected positive DBSizeBytes")
	}
}

func TestInsertOrIgnoreDuplicateNodes(t *testing.T) {
	st := tempStore(t)

	node := testNode("dup.md", "document", "Original", "dup.md")
	if err := st.InsertNodes([]Node{node}); err != nil {
		t.Fatalf("first InsertNodes failed: %v", err)
	}

	// Insert same ID again with different name — INSERT OR IGNORE should skip it.
	dup := testNode("dup.md", "document", "Duplicate Name", "dup.md")
	if err := st.InsertNodes([]Node{dup}); err != nil {
		t.Fatalf("second InsertNodes should not error, got: %v", err)
	}

	stats, err := st.GetStats()
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if stats.NodeCount != 1 {
		t.Errorf("expected 1 node after duplicate insert, got %d", stats.NodeCount)
	}

	// Verify the original name was preserved (not overwritten).
	n, err := st.GetNodeByID("dup.md")
	if err != nil {
		t.Fatalf("GetNodeByID failed: %v", err)
	}
	if n.Name != "Original" {
		t.Errorf("expected name 'Original', got %q", n.Name)
	}
}

func TestSearchSQLInjection(t *testing.T) {
	st := tempStore(t)

	// Insert a node so the table has data.
	node := testNode("safe.md", "document", "Safe Doc", "safe.md")
	if err := st.InsertNodes([]Node{node}); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}

	// Attempt SQL injection via search. This should not crash the process or
	// corrupt the database, regardless of whether Search returns an error or
	// zero results.
	results, err := st.Search(`"; DROP TABLE nodes; --`, "", 10)
	_ = results // We don't care about the result count; only that the DB survives.
	_ = err     // FTS5 may return a syntax error, which is acceptable.

	// The critical assertion: the nodes table must still exist and contain data.
	stats, err := st.GetStats()
	if err != nil {
		t.Fatalf("GetStats failed after injection attempt: %v", err)
	}
	if stats.NodeCount != 1 {
		t.Errorf("expected 1 node (table intact), got %d", stats.NodeCount)
	}
}

func TestGetIncomingOutgoingEdges(t *testing.T) {
	st := tempStore(t)

	// Set up: doc → heading, plus cross-doc reference edges.
	nodes := []Node{
		testNode("a.md", "document", "Doc A", "a.md"),
		testNode("a.md#intro", "heading", "Intro", "a.md"),
		testNode("b.md", "document", "Doc B", "b.md"),
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}

	edges := []Edge{
		{Source: "a.md", Target: "a.md#intro", Kind: "contains"},
		// Cross-doc reference: a.md#intro references b.md
		{Source: "a.md#intro", Target: "b.md", Kind: "references", Line: 5},
		// Cross-doc reference: b.md references a.md#intro
		{Source: "b.md", Target: "a.md#intro", Kind: "wikilinks_to", Line: 3},
	}
	if err := st.InsertEdges(edges); err != nil {
		t.Fatalf("InsertEdges failed: %v", err)
	}

	t.Run("incoming edges for heading node", func(t *testing.T) {
		incoming, err := st.GetIncomingEdges("a.md#intro")
		if err != nil {
			t.Fatalf("GetIncomingEdges failed: %v", err)
		}
		// Should find the wikilinks_to edge from b.md (contains is excluded by filter).
		if len(incoming) != 1 {
			t.Fatalf("expected 1 incoming edge, got %d", len(incoming))
		}
		if incoming[0].Source != "b.md" {
			t.Errorf("expected source=b.md, got %q", incoming[0].Source)
		}
		if incoming[0].Kind != "wikilinks_to" {
			t.Errorf("expected kind=wikilinks_to, got %q", incoming[0].Kind)
		}
	})

	t.Run("outgoing edges for heading node", func(t *testing.T) {
		outgoing, err := st.GetOutgoingEdges("a.md#intro")
		if err != nil {
			t.Fatalf("GetOutgoingEdges failed: %v", err)
		}
		// Should find the references edge to b.md (contains is excluded by filter).
		if len(outgoing) != 1 {
			t.Fatalf("expected 1 outgoing edge, got %d", len(outgoing))
		}
		if outgoing[0].Target != "b.md" {
			t.Errorf("expected target=b.md, got %q", outgoing[0].Target)
		}
		if outgoing[0].Kind != "references" {
			t.Errorf("expected kind=references, got %q", outgoing[0].Kind)
		}
	})

	t.Run("incoming edges for document node", func(t *testing.T) {
		// Document branch: GetIncomingEdges joins on file_path for kind=document.
		incoming, err := st.GetIncomingEdges("b.md")
		if err != nil {
			t.Fatalf("GetIncomingEdges failed: %v", err)
		}
		// a.md#intro → b.md (references)
		if len(incoming) != 1 {
			t.Fatalf("expected 1 incoming edge for document, got %d", len(incoming))
		}
		if incoming[0].Source != "a.md#intro" {
			t.Errorf("expected source=a.md#intro, got %q", incoming[0].Source)
		}
	})

	t.Run("outgoing edges for document node", func(t *testing.T) {
		// Document branch: GetOutgoingEdges joins on file_path for kind=document.
		// b.md → a.md#intro (wikilinks_to) — source is b.md, a document node,
		// so the query joins edges where source.file_path = "b.md".
		outgoing, err := st.GetOutgoingEdges("b.md")
		if err != nil {
			t.Fatalf("GetOutgoingEdges failed: %v", err)
		}
		if len(outgoing) != 1 {
			t.Fatalf("expected 1 outgoing edge for document, got %d", len(outgoing))
		}
		if outgoing[0].Target != "a.md#intro" {
			t.Errorf("expected target=a.md#intro, got %q", outgoing[0].Target)
		}
	})
}

func TestNullAndEmptySearch(t *testing.T) {
	st := tempStore(t)

	// Insert a node so there's data in the DB.
	node := testNode("empty.md", "document", "Empty Test", "empty.md")
	if err := st.InsertNodes([]Node{node}); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}

	t.Run("empty string search", func(t *testing.T) {
		results, err := st.Search("", "", 10)
		if err != nil {
			t.Fatalf("Search with empty string should not crash, got: %v", err)
		}
		// Empty query hits the LIKE "%%' path, which may return all rows.
		// The key assertion is no crash.
		_ = results
	})

	t.Run("whitespace only search", func(t *testing.T) {
		results, err := st.Search("   ", "", 10)
		// May error or return results; must not crash.
		_ = results
		_ = err
	})
}

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
		results, err := st.Searcher.Search("Alpha Report", "", 10)
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
		results, err := st.Searcher.Search("Introduction", "", 10)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected at least 1 result for 'Introduction', got 0")
		}
	})

	t.Run("search with kind filter", func(t *testing.T) {
		results, err := st.Searcher.Search("Alpha", "heading", 10)
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

func TestSearchSectionChunkCandidate(t *testing.T) {
	st := tempStore(t)

	nodes := []Node{
		{
			ID: "ops.md", Kind: "document", Name: "Operations Manual",
			QualifiedName: "ops.md", FilePath: "ops.md",
			StartLine: 1, EndLine: 20, Level: 0, BodyExcerpt: "General operations notes.", UpdatedAt: 1,
		},
		{
			ID: "ops.md#response", Kind: "heading", Name: "Response Playbook",
			QualifiedName: "ops.md#response", FilePath: "ops.md",
			StartLine: 8, EndLine: 14, Level: 2, BodyExcerpt: "", UpdatedAt: 1,
		},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}
	if err := st.UpsertSectionChunks([]SectionChunk{
		sectionChunk("ops.md", "ops.md", "hash", "doc", "", "General operations notes.\n## Response Playbook\nIncident response escalation matrix.", 1, 20),
		sectionChunk("ops.md#response", "ops.md", "hash", "section", "Response Playbook", "## Response Playbook\nIncident response escalation matrix.", 8, 14),
	}); err != nil {
		t.Fatalf("UpsertSectionChunks failed: %v", err)
	}

	results, err := st.Searcher.Search("incident response", "", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected section-level search result, got none")
	}
	if results[0].Node.ID != "ops.md#response" {
		t.Fatalf("expected heading section first, got %q", results[0].Node.ID)
	}
}

func TestSearchFieldWeightedRanking(t *testing.T) {
	st := tempStore(t)

	nodes := []Node{
		{
			ID: "title.md", Kind: "document", Name: "Budget Policy",
			QualifiedName: "title.md", FilePath: "title.md",
			StartLine: 1, EndLine: 5, BodyExcerpt: "Approval workflow.", UpdatedAt: 1,
		},
		{
			ID: "body.md", Kind: "document", Name: "Finance Notes",
			QualifiedName: "body.md", FilePath: "body.md",
			StartLine: 1, EndLine: 5, BodyExcerpt: "Budget policy appears in body text only.", UpdatedAt: 1,
		},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}

	results, err := st.Searcher.Search("budget policy", "document", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected two results, got %d", len(results))
	}
	if results[0].Node.ID != "title.md" {
		t.Fatalf("expected title field match first, got %q", results[0].Node.ID)
	}
}

func TestSearchGraphRerankingIncomingReferences(t *testing.T) {
	st := tempStore(t)

	nodes := []Node{
		{ID: "a.md", Kind: "document", Name: "Shared Topic", QualifiedName: "a.md", FilePath: "a.md", StartLine: 1, EndLine: 5, BodyExcerpt: "shared topic", UpdatedAt: 1},
		{ID: "b.md", Kind: "document", Name: "Shared Topic", QualifiedName: "b.md", FilePath: "b.md", StartLine: 1, EndLine: 5, BodyExcerpt: "shared topic", UpdatedAt: 1},
		{ID: "ref1.md", Kind: "document", Name: "Reference One", QualifiedName: "ref1.md", FilePath: "ref1.md", StartLine: 1, EndLine: 5, UpdatedAt: 1},
		{ID: "ref2.md", Kind: "document", Name: "Reference Two", QualifiedName: "ref2.md", FilePath: "ref2.md", StartLine: 1, EndLine: 5, UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}
	if err := st.InsertEdges([]Edge{
		{Source: "ref1.md", Target: "b.md", Kind: "references"},
		{Source: "ref2.md", Target: "b.md", Kind: "references"},
	}); err != nil {
		t.Fatalf("InsertEdges failed: %v", err)
	}

	results, err := st.Searcher.Search("shared topic", "document", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least two results, got %d", len(results))
	}
	if results[0].Node.ID != "b.md" {
		t.Fatalf("expected graph-boosted document first, got %q", results[0].Node.ID)
	}
}

func TestSearchGovernanceAwareRanking(t *testing.T) {
	st := tempStore(t)

	nodes := []Node{
		{ID: "draft.md", Kind: "document", Name: "Incident Policy", QualifiedName: "draft.md", FilePath: "draft.md", StartLine: 1, EndLine: 5, BodyExcerpt: "incident response policy", UpdatedAt: 1},
		{ID: "approved.md", Kind: "document", Name: "Incident Policy", QualifiedName: "approved.md", FilePath: "approved.md", StartLine: 1, EndLine: 5, BodyExcerpt: "incident response policy", UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("draft.md", []MetadataTuple{
		{Key: "status", Value: "draft", ValueType: "string", Source: "frontmatter"},
		{Key: "sensitivity", Value: "restricted", ValueType: "string", Source: "frontmatter"},
		{Key: "canonical_source", Value: "false", ValueType: "bool", Source: "frontmatter"},
		{Key: "review_due", Value: "2020-01-01", ValueType: "date", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata draft: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("approved.md", []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "sensitivity", Value: "public", ValueType: "string", Source: "frontmatter"},
		{Key: "canonical_source", Value: "true", ValueType: "bool", Source: "frontmatter"},
		{Key: "effective_date", Value: "2025-01-01", ValueType: "date", Source: "frontmatter"},
		{Key: "review_due", Value: "2099-01-01", ValueType: "date", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata approved: %v", err)
	}

	results, err := st.Searcher.SearchWithOptions(SearchOptions{
		Query: "incident response policy",
		Kind:  "document",
		Limit: 10,
		Governance: GovernanceSearchOptions{
			AsOfDate: "2026-05-26",
		},
	})
	if err != nil {
		t.Fatalf("SearchWithOptions failed: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected two results, got %d", len(results))
	}
	if results[0].Node.ID != "approved.md" {
		t.Fatalf("expected approved canonical document first, got %q", results[0].Node.ID)
	}
}

func TestSearchGovernanceFiltersAudienceAndEffectiveDate(t *testing.T) {
	st := tempStore(t)

	nodes := []Node{
		{ID: "future.md", Kind: "document", Name: "Access Policy", QualifiedName: "future.md", FilePath: "future.md", StartLine: 1, EndLine: 5, BodyExcerpt: "access policy", UpdatedAt: 1},
		{ID: "restricted.md", Kind: "document", Name: "Access Policy", QualifiedName: "restricted.md", FilePath: "restricted.md", StartLine: 1, EndLine: 5, BodyExcerpt: "access policy", UpdatedAt: 1},
		{ID: "analyst.md", Kind: "document", Name: "Access Policy", QualifiedName: "analyst.md", FilePath: "analyst.md", StartLine: 1, EndLine: 5, BodyExcerpt: "access policy", UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}
	fixtures := map[string][]MetadataTuple{
		"future.md": {
			{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
			{Key: "effective_date", Value: "2030-01-01", ValueType: "date", Source: "frontmatter"},
			{Key: "allowed_audience", Value: "analyst", ValueType: "list", Source: "frontmatter"},
		},
		"restricted.md": {
			{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
			{Key: "effective_date", Value: "2024-01-01", ValueType: "date", Source: "frontmatter"},
			{Key: "allowed_audience", Value: "executive", ValueType: "list", Source: "frontmatter"},
		},
		"analyst.md": {
			{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
			{Key: "effective_date", Value: "2024-01-01", ValueType: "date", Source: "frontmatter"},
			{Key: "allowed_audience", Value: "analyst,security", ValueType: "list", Source: "frontmatter"},
		},
	}
	for id, tuples := range fixtures {
		if err := st.UpsertGovernanceMetadata(id, tuples); err != nil {
			t.Fatalf("UpsertGovernanceMetadata %s: %v", id, err)
		}
	}

	results, err := st.Searcher.SearchWithOptions(SearchOptions{
		Query: "access policy",
		Kind:  "document",
		Limit: 10,
		Governance: GovernanceSearchOptions{
			AllowedAudience: "analyst",
			AsOfDate:        "2026-05-26",
		},
	})
	if err != nil {
		t.Fatalf("SearchWithOptions failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one audience/effective-date result, got %d: %#v", len(results), results)
	}
	if results[0].Node.ID != "analyst.md" {
		t.Fatalf("expected analyst.md, got %q", results[0].Node.ID)
	}
}

func TestSearchResearchAwareRanking(t *testing.T) {
	st := tempStore(t)

	nodes := []Node{
		{ID: "low.md", Kind: "document", Name: "Supply Assessment", QualifiedName: "low.md", FilePath: "low.md", StartLine: 1, EndLine: 5, BodyExcerpt: "supply chain assessment", UpdatedAt: 1},
		{ID: "high.md", Kind: "document", Name: "Supply Assessment", QualifiedName: "high.md", FilePath: "high.md", StartLine: 1, EndLine: 5, BodyExcerpt: "supply chain assessment", UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}
	if err := st.UpsertResearchMetadata("low.md", []MetadataTuple{
		{Key: "source_type", Value: "social", ValueType: "string", Source: "frontmatter"},
		{Key: "confidence", Value: "low", ValueType: "string", Source: "frontmatter"},
		{Key: "analyst_status", Value: "draft", ValueType: "string", Source: "frontmatter"},
		{Key: "valid_until", Value: "2020-01-01", ValueType: "date", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertResearchMetadata low: %v", err)
	}
	if err := st.UpsertResearchMetadata("high.md", []MetadataTuple{
		{Key: "source_type", Value: "primary", ValueType: "string", Source: "frontmatter"},
		{Key: "confidence", Value: "high", ValueType: "string", Source: "frontmatter"},
		{Key: "analyst_status", Value: "verified", ValueType: "string", Source: "frontmatter"},
		{Key: "valid_until", Value: "2099-01-01", ValueType: "date", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertResearchMetadata high: %v", err)
	}

	results, err := st.Searcher.SearchWithOptions(SearchOptions{
		Query: "supply chain assessment",
		Kind:  "document",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchWithOptions failed: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected two results, got %d", len(results))
	}
	if results[0].Node.ID != "high.md" {
		t.Fatalf("expected high-confidence primary source first, got %q", results[0].Node.ID)
	}
}

func TestSearchTagExpansionReturnsTaggedDocument(t *testing.T) {
	st := tempStore(t)

	nodes := []Node{
		{ID: "policy.md", Kind: "document", Name: "Access Policy", QualifiedName: "policy.md", FilePath: "policy.md", StartLine: 1, EndLine: 5, BodyExcerpt: "Identity controls.", UpdatedAt: 1},
		{ID: "tag:security", Kind: "tag", Name: "security", QualifiedName: "tag:security", FilePath: "policy.md", StartLine: 0, EndLine: 0, UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}
	if err := st.InsertEdges([]Edge{{Source: "policy.md", Target: "tag:security", Kind: "tagged"}}); err != nil {
		t.Fatalf("InsertEdges failed: %v", err)
	}

	results, err := st.Searcher.Search("security", "document", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected tagged document result, got none")
	}
	if results[0].Node.ID != "policy.md" {
		t.Fatalf("expected tagged document first, got %q", results[0].Node.ID)
	}
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
		results, err := st.Searcher.Search("情報分析", "", 10)
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
		results, err := st.Searcher.Search("情報", "", 10)
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

	t.Run("multi-term all-short CJK query", func(t *testing.T) {
		// Two 2-char CJK terms: the query is not Short under the old single-term
		// rule, so it hit FTS MATCH → 0 trigram rows with no fallback. The terms
		// are non-adjacent in the body ("情報分析結果在此"), so a whole-query LIKE
		// would also miss — only a per-term AND fallback matches.
		results, err := st.Searcher.Search("情報 結果", "", 10)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected per-term LIKE fallback match for '情報 結果', got 0 results")
		}
		if results[0].Node.ID != "cjk-doc.md" {
			t.Errorf("expected cjk-doc.md, got %q", results[0].Node.ID)
		}
	})

	t.Run("multi-term all-short CJK requires ALL terms", func(t *testing.T) {
		// "情報" is in the body but "无关" is not → per-term AND must exclude it.
		results, err := st.Searcher.Search("情報 无关", "", 10)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		for _, r := range results {
			if r.Node.ID == "cjk-doc.md" {
				t.Fatalf("per-term AND must require every term; doc lacks '无关' yet matched")
			}
		}
	})
}

func TestSearchMultiTermAllShortLatin(t *testing.T) {
	// Latin sub-trigram multi-term: "is" + "to" are each <3 chars, so the query
	// is all-short → LIKE fallback. Before the fix it hit FTS MATCH → 0 rows.
	st := tempStore(t)
	nodes := []Node{
		{
			ID: "a.md", Kind: "document", Name: "Alpha",
			QualifiedName: "a.md", FilePath: "a.md",
			StartLine: 1, EndLine: 3, BodyExcerpt: "this is how to do it", UpdatedAt: 1,
		},
		{
			ID: "b.md", Kind: "document", Name: "Beta",
			QualifiedName: "b.md", FilePath: "b.md",
			StartLine: 1, EndLine: 3, BodyExcerpt: "an unrelated sentence", UpdatedAt: 1,
		},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}
	results, err := st.Searcher.Search("is to", "", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	var gotA bool
	for _, r := range results {
		if r.Node.ID == "b.md" {
			t.Fatalf("b.md contains neither 'is' nor 'to' as both terms; per-term AND must exclude it")
		}
		if r.Node.ID == "a.md" {
			gotA = true
		}
	}
	if !gotA {
		t.Fatal("expected a.md (contains both 'is' and 'to') for all-short query 'is to', got none")
	}
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
	results, err := st.Searcher.Search(`"; DROP TABLE nodes; --`, "", 10)
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
		results, err := st.Searcher.Search("", "", 10)
		if err != nil {
			t.Fatalf("Search with empty string should not crash, got: %v", err)
		}
		// Empty query hits the LIKE "%%' path, which may return all rows.
		// The key assertion is no crash.
		_ = results
	})

	t.Run("whitespace only search", func(t *testing.T) {
		results, err := st.Searcher.Search("   ", "", 10)
		// May error or return results; must not crash.
		_ = results
		_ = err
	})
}

func TestGetAllTags_Empty(t *testing.T) {
	st := tempStore(t)
	tags, err := st.GetAllTags()
	if err != nil {
		t.Fatalf("GetAllTags on empty DB failed: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("expected 0 tags, got %d", len(tags))
	}
}

func TestGetAllTags_CountsAndOrder(t *testing.T) {
	st := tempStore(t)

	// Two documents tagged with "roadmap", one with "api".
	nodes := []Node{
		testNode("doc1.md", "document", "Doc One", "doc1.md"),
		testNode("doc2.md", "document", "Doc Two", "doc2.md"),
		testNode("doc3.md", "document", "Doc Three", "doc3.md"),
		// tag nodes — one per (doc, tag) pair
		testNode("doc1.md#tag:roadmap", "tag", "roadmap", "doc1.md"),
		testNode("doc2.md#tag:roadmap", "tag", "roadmap", "doc2.md"),
		testNode("doc3.md#tag:api", "tag", "api", "doc3.md"),
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}
	edges := []Edge{
		{Source: "doc1.md", Target: "doc1.md#tag:roadmap", Kind: "tagged"},
		{Source: "doc2.md", Target: "doc2.md#tag:roadmap", Kind: "tagged"},
		{Source: "doc3.md", Target: "doc3.md#tag:api", Kind: "tagged"},
	}
	if err := st.InsertEdges(edges); err != nil {
		t.Fatalf("InsertEdges failed: %v", err)
	}

	tags, err := st.GetAllTags()
	if err != nil {
		t.Fatalf("GetAllTags failed: %v", err)
	}
	if len(tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(tags))
	}
	// "roadmap" should come first (count=2), then "api" (count=1).
	if tags[0].Name != "roadmap" || tags[0].Count != 2 {
		t.Errorf("expected first tag roadmap/2, got %s/%d", tags[0].Name, tags[0].Count)
	}
	if tags[1].Name != "api" || tags[1].Count != 1 {
		t.Errorf("expected second tag api/1, got %s/%d", tags[1].Name, tags[1].Count)
	}
}

func TestGetDocumentsByTag(t *testing.T) {
	st := tempStore(t)

	nodes := []Node{
		testNode("doc1.md", "document", "Doc One", "doc1.md"),
		testNode("doc2.md", "document", "Doc Two", "doc2.md"),
		testNode("doc3.md", "document", "Doc Three", "doc3.md"),
		testNode("doc1.md#tag:roadmap", "tag", "roadmap", "doc1.md"),
		testNode("doc2.md#tag:roadmap", "tag", "roadmap", "doc2.md"),
		testNode("doc3.md#tag:api", "tag", "api", "doc3.md"),
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}
	edges := []Edge{
		{Source: "doc1.md", Target: "doc1.md#tag:roadmap", Kind: "tagged"},
		{Source: "doc2.md", Target: "doc2.md#tag:roadmap", Kind: "tagged"},
		{Source: "doc3.md", Target: "doc3.md#tag:api", Kind: "tagged"},
	}
	if err := st.InsertEdges(edges); err != nil {
		t.Fatalf("InsertEdges failed: %v", err)
	}

	t.Run("exact match", func(t *testing.T) {
		docs, err := st.GetDocumentsByTag("roadmap")
		if err != nil {
			t.Fatalf("GetDocumentsByTag failed: %v", err)
		}
		if len(docs) != 2 {
			t.Fatalf("expected 2 docs for tag 'roadmap', got %d", len(docs))
		}
		paths := map[string]bool{}
		for _, d := range docs {
			paths[d.FilePath] = true
		}
		if !paths["doc1.md"] || !paths["doc2.md"] {
			t.Errorf("expected doc1.md and doc2.md, got %v", paths)
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		docs, err := st.GetDocumentsByTag("ROADMAP")
		if err != nil {
			t.Fatalf("GetDocumentsByTag case-insensitive failed: %v", err)
		}
		if len(docs) != 2 {
			t.Errorf("expected 2 docs for tag 'ROADMAP' (case-insensitive), got %d", len(docs))
		}
	})

	t.Run("missing tag", func(t *testing.T) {
		docs, err := st.GetDocumentsByTag("nonexistent")
		if err != nil {
			t.Fatalf("GetDocumentsByTag for missing tag failed: %v", err)
		}
		if len(docs) != 0 {
			t.Errorf("expected 0 docs for nonexistent tag, got %d", len(docs))
		}
	})
}

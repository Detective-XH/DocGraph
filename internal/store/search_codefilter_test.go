package store

import (
	"slices"
	"strings"
	"testing"
)

const codeFilePath = "telemetry.go"

// seedDocAndCodeFile inserts one markdown document plus a code_doc-style file:
// a kind="code_file" node AND a kind="heading" node derived from it (mirroring
// codedoc.buildResult, which emits one heading per test func / doc comment /
// file header). Both code nodes share file_path=telemetry.go. All three nodes
// match the term "telemetry" and the 2-char substring "et", so the collectors
// are exercised on both the FTS and LIKE-fallback paths.
func seedDocAndCodeFile(t *testing.T) *Store {
	t.Helper()
	st := tempStore(t)
	nodes := []Node{
		{
			ID: "report.md", Kind: "document", Name: "Telemetry Report",
			QualifiedName: "report.md", FilePath: "report.md",
			StartLine: 1, EndLine: 5, BodyExcerpt: "telemetry metrics overview", UpdatedAt: 1,
		},
		{
			ID: codeFilePath, Kind: "code_file", Name: codeFilePath,
			QualifiedName: codeFilePath, FilePath: codeFilePath,
			StartLine: 1, EndLine: 10, BodyExcerpt: "package telemetry metrics", UpdatedAt: 1,
		},
		{
			// code-derived heading (e.g. a test func name): kind="heading" but
			// FilePath points at the .go file — NOT caught by a kind filter.
			ID: codeFilePath + "#test_func-3", Kind: "heading", Name: "TestTelemetryEmit",
			QualifiedName: codeFilePath + "#test_func-3", FilePath: codeFilePath,
			StartLine: 3, EndLine: 6, Level: 2, BodyExcerpt: "telemetry emit metrics", UpdatedAt: 1,
		},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes failed: %v", err)
	}
	if err := st.UpsertSectionChunks([]SectionChunk{
		sectionChunk("report.md", "report.md", "h1", "doc", "", "telemetry metrics overview", 1, 5),
		sectionChunk(codeFilePath, codeFilePath, "h2", "doc", "File Header", "package telemetry metrics", 1, 10),
		sectionChunk(codeFilePath+"#test_func-3", codeFilePath, "h2", "section", "Tests > TestTelemetryEmit", "telemetry emit metrics", 3, 6),
	}); err != nil {
		t.Fatalf("UpsertSectionChunks failed: %v", err)
	}
	return st
}

// isCodeResult reports whether a result is code-derived. The robust axis is the
// source file (a code_file node OR any node sharing its .go path), not kind:
// code emits kind="heading" nodes that a kind filter would miss.
func isCodeResult(r SearchResult) bool {
	return r.Node.Kind == "code_file" || strings.HasSuffix(r.Node.FilePath, ".go")
}

func anyCode(results []SearchResult) bool {
	return slices.ContainsFunc(results, isCodeResult)
}

// TestSearchExcludesCodeFileByDefault: docgraph_search returns documentation
// only unless code is explicitly opted in — including the code-derived heading
// nodes, not just the file-level code_file node. Covers the FTS path (>=3-char
// term) and the LIKE fallback (2-char term → req.Short).
func TestSearchExcludesCodeFileByDefault(t *testing.T) {
	st := seedDocAndCodeFile(t)

	ftsResults, err := st.Searcher.Search("telemetry", "", 10)
	if err != nil {
		t.Fatalf("Search(fts) failed: %v", err)
	}
	if len(ftsResults) == 0 {
		t.Fatal("expected doc results for \"telemetry\", got none")
	}
	if anyCode(ftsResults) {
		t.Fatalf("default FTS search must exclude all code-derived nodes, got %+v", ftsResults)
	}

	likeResults, err := st.Searcher.Search("et", "", 10)
	if err != nil {
		t.Fatalf("Search(like) failed: %v", err)
	}
	if len(likeResults) == 0 {
		t.Fatal("expected doc results for short query \"et\", got none")
	}
	if anyCode(likeResults) {
		t.Fatalf("default LIKE-fallback search must exclude all code-derived nodes, got %+v", likeResults)
	}
}

// TestSearchIncludeCodeOptsIn: include_code=true surfaces code-derived results
// (both the code_file node and its heading children) on the FTS and LIKE paths.
func TestSearchIncludeCodeOptsIn(t *testing.T) {
	st := seedDocAndCodeFile(t)

	ftsResults, err := st.Searcher.SearchWithOptions(SearchOptions{Query: "telemetry", IncludeCode: true, Limit: 10})
	if err != nil {
		t.Fatalf("SearchWithOptions(fts) failed: %v", err)
	}
	if !anyCode(ftsResults) {
		t.Fatalf("include_code FTS search must surface code-derived nodes, got %+v", ftsResults)
	}
	// The code-derived heading (kind=heading, .go path) must be reachable —
	// a kind-only filter would have wrongly dropped it.
	if !hasCodeHeading(ftsResults) {
		t.Fatalf("include_code must surface the code-derived heading, got %+v", ftsResults)
	}

	likeResults, err := st.Searcher.SearchWithOptions(SearchOptions{Query: "et", IncludeCode: true, Limit: 10})
	if err != nil {
		t.Fatalf("SearchWithOptions(like) failed: %v", err)
	}
	if !anyCode(likeResults) {
		t.Fatalf("include_code LIKE-fallback search must surface code-derived nodes, got %+v", likeResults)
	}
}

func hasCodeHeading(results []SearchResult) bool {
	return slices.ContainsFunc(results, func(r SearchResult) bool {
		return r.Node.Kind == "heading" && strings.HasSuffix(r.Node.FilePath, ".go")
	})
}

// TestSearchKindCodeFileImpliesIncludeCode: passing kind=code_file is itself an
// opt-in. Without the newSearchRequest derivation, the code-exclusion filter
// would contradict the kind=='code_file' restriction and return nothing.
func TestSearchKindCodeFileImpliesIncludeCode(t *testing.T) {
	st := seedDocAndCodeFile(t)

	results, err := st.Searcher.Search("telemetry", "code_file", 10)
	if err != nil {
		t.Fatalf("Search(kind=code_file) failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("kind=code_file must return code_file results, got none")
	}
	for _, r := range results {
		if r.Node.Kind != "code_file" {
			t.Fatalf("kind=code_file must return only code_file, got %q", r.Node.Kind)
		}
	}
}

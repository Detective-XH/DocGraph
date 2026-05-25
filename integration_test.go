package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/parser"
	"github.com/Detective-XH/docgraph/internal/resolver"
	"github.com/Detective-XH/docgraph/internal/scanner"
	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/Detective-XH/docgraph/internal/tools"
	"github.com/Detective-XH/docgraph/internal/workspace"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// indexTestProject indexes all .md files in projectDir into a fresh store,
// resolves references, and returns the store. The DB lives in t.TempDir().
func indexTestProject(t *testing.T, projectDir string) *store.Store {
	t.Helper()
	dbDir := filepath.Join(t.TempDir(), ".docgraph")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dbDir, "docgraph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	entries, err := scanner.ScanDir(projectDir)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		src, err := os.ReadFile(e.Path)
		if err != nil {
			continue
		}
		h := sha256.Sum256(src)
		hash := hex.EncodeToString(h[:])
		st.DeleteFileData(e.RelPath)
		res, err := parser.ParseFile(e.Path, e.RelPath, src, hash)
		if err != nil {
			continue
		}
		nodes := append(append([]store.Node{res.DocNode}, res.Headings...), res.Tags...)
		res.FileInfo.ModifiedAt = e.ModifiedAt
		if err := st.InsertNodes(nodes); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertEdges(res.Edges); err != nil {
			t.Fatal(err)
		}
		if len(res.RawLinks) > 0 {
			refs := make([]store.UnresolvedRef, 0, len(res.RawLinks))
			for _, rl := range res.RawLinks {
				refs = append(refs, store.UnresolvedRef{
					FromNodeID:    rl.FromNodeID,
					ReferenceText: rl.Target,
					ReferenceKind: rl.Kind,
					Line:          rl.Line,
					FilePath:      e.RelPath,
				})
			}
			if err := st.InsertUnresolvedRefs(refs); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.UpsertFile(res.FileInfo); err != nil {
			t.Fatal(err)
		}
	}

	if err := resolver.Resolve(st); err != nil {
		t.Fatal(err)
	}
	return st
}

// indexTestProjectIncremental mirrors workspace.indexProject: it skips files
// whose content hash has not changed. It returns (nNew, nSkip).
func indexTestProjectIncremental(t *testing.T, st *store.Store, projectDir string) (nNew, nSkip int) {
	t.Helper()
	entries, err := scanner.ScanDir(projectDir)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		src, err := os.ReadFile(e.Path)
		if err != nil {
			continue
		}
		h := sha256.Sum256(src)
		hash := hex.EncodeToString(h[:])
		if old, _ := st.GetFileHash(e.RelPath); hash == old {
			nSkip++
			continue
		}
		st.DeleteFileData(e.RelPath)
		res, err := parser.ParseFile(e.Path, e.RelPath, src, hash)
		if err != nil {
			continue
		}
		nodes := append(append([]store.Node{res.DocNode}, res.Headings...), res.Tags...)
		res.FileInfo.ModifiedAt = e.ModifiedAt
		if err := st.InsertNodes(nodes); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertEdges(res.Edges); err != nil {
			t.Fatal(err)
		}
		if len(res.RawLinks) > 0 {
			refs := make([]store.UnresolvedRef, 0, len(res.RawLinks))
			for _, rl := range res.RawLinks {
				refs = append(refs, store.UnresolvedRef{
					FromNodeID:    rl.FromNodeID,
					ReferenceText: rl.Target,
					ReferenceKind: rl.Kind,
					Line:          rl.Line,
					FilePath:      e.RelPath,
				})
			}
			if err := st.InsertUnresolvedRefs(refs); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.UpsertFile(res.FileInfo); err != nil {
			t.Fatal(err)
		}
		nNew++
	}

	if nNew > 0 {
		if err := resolver.Resolve(st); err != nil {
			t.Fatal(err)
		}
	}
	return nNew, nSkip
}

func fixtureDir(t *testing.T, project string) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", project))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// copyDir recursively copies src to dst.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Test 1: Full pipeline single project
// ---------------------------------------------------------------------------

func TestFullPipelineSingleProject(t *testing.T) {
	projectDir := fixtureDir(t, "project-a")

	// Step 1: ScanDir should find exactly 4 .md files
	entries, err := scanner.ScanDir(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(entries); got != 4 {
		t.Fatalf("ScanDir: expected 4 files, got %d", got)
	}

	// Step 2-3: Full index + resolve
	st := indexTestProject(t, projectDir)

	stats, err := st.GetStats()
	if err != nil {
		t.Fatal(err)
	}

	// Step 4: Verify 4 document nodes
	docCount := stats.NodesByKind["document"]
	if docCount != 4 {
		t.Errorf("expected 4 document nodes, got %d", docCount)
	}

	// Verify headings > 0
	headingCount := stats.NodesByKind["heading"]
	if headingCount == 0 {
		t.Error("expected heading nodes > 0, got 0")
	}

	// Verify reference edges > 0 and wikilinks_to edges > 0
	refEdges := stats.EdgesByKind["references"]
	if refEdges == 0 {
		t.Error("expected references edges > 0, got 0")
	}
	wikiEdges := stats.EdgesByKind["wikilinks_to"]
	if wikiEdges == 0 {
		t.Error("expected wikilinks_to edges > 0, got 0")
	}

	// Step 5: doc-a → doc-b wikilink edge exists
	// [[doc-b]] appears on line 3 of doc-a.md, before any heading,
	// so FromNodeID = "doc-a.md" (the document node).
	// After resolve, target should be "doc-b.md".
	docBNode, err := st.FindNodeByName("Document B")
	if err != nil {
		t.Fatal(err)
	}
	if docBNode == nil {
		t.Fatal("doc-b document node not found")
	}
	inEdges, err := st.GetIncomingEdges(docBNode.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundWikilink := false
	for _, e := range inEdges {
		if e.Kind == "wikilinks_to" && strings.HasPrefix(e.Source, "doc-a.md") {
			foundWikilink = true
			break
		}
	}
	if !foundWikilink {
		t.Error("missing wikilink edge from doc-a.md to doc-b.md")
	}

	// Step 6: README → doc-a reference edge exists
	// doc-a.md's H1 is "Document A"; the document node ID is "doc-a.md"
	docANode, err := st.FindNodeByPath("doc-a.md")
	if err != nil {
		t.Fatal(err)
	}
	if docANode == nil {
		t.Fatal("doc-a.md document node not found")
	}
	docAInEdges, err := st.GetIncomingEdges(docANode.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundReadmeRef := false
	for _, e := range docAInEdges {
		// Source is a heading node inside README.md (e.g. "README.md#project-a")
		if e.Kind == "references" && strings.HasPrefix(e.Source, "README.md") {
			foundReadmeRef = true
			break
		}
	}
	if !foundReadmeRef {
		t.Error("missing reference edge from README.md to doc-a.md")
	}

	// Step 7: nested → README reference edge (relative path ../README.md resolved)
	readmeNode, err := st.FindNodeByPath("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if readmeNode == nil {
		t.Fatal("README.md document node not found")
	}
	readmeInEdges, err := st.GetIncomingEdges(readmeNode.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundNestedRef := false
	for _, e := range readmeInEdges {
		if e.Kind == "references" && strings.Contains(e.Source, "nested") {
			foundNestedRef = true
			break
		}
	}
	if !foundNestedRef {
		t.Error("missing reference edge from subdir/nested.md to README.md")
	}
}

// ---------------------------------------------------------------------------
// Test 2: Incremental re-index
// ---------------------------------------------------------------------------

func TestFullPipelineIncremental(t *testing.T) {
	// Copy project-a to a temp dir so we can modify files safely
	tmpRoot := t.TempDir()
	projectDir := filepath.Join(tmpRoot, "project-a")
	copyDir(t, fixtureDir(t, "project-a"), projectDir)

	// Step 1: Initial index
	dbDir := filepath.Join(t.TempDir(), ".docgraph")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dbDir, "docgraph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	nNew, _ := indexTestProjectIncremental(t, st, projectDir)
	if nNew != 4 {
		t.Fatalf("initial index: expected 4 new, got %d", nNew)
	}

	// Step 2: Re-index unchanged — should be 0 new, 4 skipped
	nNew, nSkip := indexTestProjectIncremental(t, st, projectDir)
	if nNew != 0 {
		t.Errorf("re-index unchanged: expected 0 new, got %d", nNew)
	}
	if nSkip != 4 {
		t.Errorf("re-index unchanged: expected 4 skipped, got %d", nSkip)
	}

	// Step 3: Modify one file (append a heading to doc-a.md)
	docAPath := filepath.Join(projectDir, "doc-a.md")
	original, err := os.ReadFile(docAPath)
	if err != nil {
		t.Fatal(err)
	}
	modified := append(original, []byte("\n\n## New Heading Added\n\nExtra content.\n")...)
	if err := os.WriteFile(docAPath, modified, 0o644); err != nil {
		t.Fatal(err)
	}

	// Step 4: Re-index — should be 1 new, 3 skipped
	nNew, nSkip = indexTestProjectIncremental(t, st, projectDir)
	if nNew != 1 {
		t.Errorf("re-index modified: expected 1 new, got %d", nNew)
	}
	if nSkip != 3 {
		t.Errorf("re-index modified: expected 3 skipped, got %d", nSkip)
	}
}

// ---------------------------------------------------------------------------
// Test 3: MCP round-trip via stdio pipe
// ---------------------------------------------------------------------------

func TestMCPRoundTrip(t *testing.T) {
	// Index project-a so there is data to query
	projectDir := fixtureDir(t, "project-a")
	st := indexTestProject(t, projectDir)

	// Create MCP server and register tools
	srv := mcpserver.NewMCPServer("docgraph", "0.1.0")
	tools.Register(srv, st, projectDir)

	// Create pipes for stdin/stdout
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()

	stdio := mcpserver.NewStdioServer(srv)
	stdio.SetErrorLogger(log.New(io.Discard, "", 0))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErrCh := make(chan error, 1)
	go func() {
		err := stdio.Listen(ctx, stdinReader, stdoutWriter)
		if err != nil && err != io.EOF && err != context.Canceled {
			serverErrCh <- err
		}
		stdoutWriter.Close()
		close(serverErrCh)
	}()

	scan := bufio.NewScanner(stdoutReader)

	sendAndRecv := func(t *testing.T, id int, method string, params map[string]any) map[string]any {
		t.Helper()
		req := map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  method,
		}
		if params != nil {
			req["params"] = params
		}
		reqBytes, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stdinWriter.Write(append(reqBytes, '\n')); err != nil {
			t.Fatal(err)
		}
		if !scan.Scan() {
			t.Fatal("failed to read response from MCP server")
		}
		var resp map[string]any
		if err := json.Unmarshal(scan.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if resp["jsonrpc"] != "2.0" {
			t.Errorf("expected jsonrpc 2.0, got %v", resp["jsonrpc"])
		}
		if resp["id"].(float64) != float64(id) {
			t.Errorf("expected id %d, got %v", id, resp["id"])
		}
		return resp
	}

	// 1. Initialize
	resp := sendAndRecv(t, 1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	if resp["error"] != nil {
		t.Fatalf("initialize error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatal("expected result object in initialize response")
	}
	serverInfo, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatal("expected serverInfo in result")
	}
	if serverInfo["name"] != "docgraph" {
		t.Errorf("expected serverInfo.name == 'docgraph', got %v", serverInfo["name"])
	}

	// 2. Call docgraph_status
	resp = sendAndRecv(t, 2, "tools/call", map[string]any{
		"name":      "docgraph_status",
		"arguments": map[string]any{},
	})
	if resp["error"] != nil {
		t.Fatalf("docgraph_status error: %v", resp["error"])
	}
	statusResult, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatal("expected result in docgraph_status response")
	}
	content, ok := statusResult["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatal("expected non-empty content in docgraph_status result")
	}
	firstContent := content[0].(map[string]any)
	statusText, _ := firstContent["text"].(string)
	if !strings.Contains(statusText, "Files:") {
		t.Errorf("expected status text to contain 'Files:', got: %s", statusText)
	}

	// 3. Call docgraph_search
	resp = sendAndRecv(t, 3, "tools/call", map[string]any{
		"name":      "docgraph_search",
		"arguments": map[string]any{"query": "Document", "limit": float64(5)},
	})
	if resp["error"] != nil {
		t.Fatalf("docgraph_search error: %v", resp["error"])
	}
	searchResult, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatal("expected result in docgraph_search response")
	}
	searchContent, ok := searchResult["content"].([]any)
	if !ok || len(searchContent) == 0 {
		t.Fatal("expected non-empty content in docgraph_search result")
	}
	searchText, _ := searchContent[0].(map[string]any)["text"].(string)
	if !strings.Contains(searchText, "Search Results") {
		t.Errorf("expected search text to contain 'Search Results', got: %s", searchText)
	}
	// Should find results (not "Found 0 results")
	if strings.Contains(searchText, "Found 0 results") {
		t.Error("search for 'Document' returned 0 results, expected matches")
	}

	// Cleanup
	cancel()
	stdinWriter.Close()
	if err := <-serverErrCh; err != nil {
		t.Errorf("unexpected server error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test 4: Workspace search across multiple projects
// ---------------------------------------------------------------------------

func TestWorkspaceSearch(t *testing.T) {
	// Copy only the three project dirs to a temp dir to avoid polluting the repo
	// (workspace.Open creates .docgraph/ dirs inside each project)
	tmpRoot := t.TempDir()
	for _, proj := range []string{"project-a", "project-b", "project-c"} {
		copyDir(t, fixtureDir(t, proj), filepath.Join(tmpRoot, proj))
	}

	// Step 1-2: Open workspace and index all
	w, err := workspace.Open(tmpRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })

	if err := w.IndexAll(); err != nil {
		t.Fatal(err)
	}

	// Verify we have 3 projects
	if got := len(w.Projects); got != 3 {
		t.Fatalf("expected 3 projects, got %d", got)
	}

	// Step 3: Search for "Document" → should find results from project-a
	results, err := w.Search("Document", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("search for 'Document' returned 0 results, expected matches from project-a")
	}
	foundProjectA := false
	for _, r := range results {
		if strings.Contains(r.Node.QualifiedName, "project-a") {
			foundProjectA = true
			break
		}
	}
	if !foundProjectA {
		t.Error("search for 'Document' did not include project-a results")
	}

	// Step 4: Search for "Glossary" → should find results from project-b
	results, err = w.Search("Glossary", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("search for 'Glossary' returned 0 results, expected matches from project-b")
	}
	foundProjectB := false
	for _, r := range results {
		if strings.Contains(r.Node.QualifiedName, "project-b") {
			foundProjectB = true
			break
		}
	}
	if !foundProjectB {
		t.Error("search for 'Glossary' did not include project-b results")
	}

	// Step 5: Search for "中文" → should find results from project-c
	// Note: len([]rune("中文")) == 2 < 3, so this uses the LIKE branch
	results, err = w.Search("中文", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("search for '中文' returned 0 results, expected matches from project-c")
	}
	foundProjectC := false
	for _, r := range results {
		if strings.Contains(r.Node.QualifiedName, "project-c") {
			foundProjectC = true
			break
		}
	}
	if !foundProjectC {
		t.Error("search for '中文' did not include project-c results")
	}
}

// ---------------------------------------------------------------------------
// Test 5: .gitignore exclusion
// ---------------------------------------------------------------------------

func TestGitignoreExclusion(t *testing.T) {
	projectDir := fixtureDir(t, "project-c")
	st := indexTestProject(t, projectDir)

	files, err := st.GetFiles("")
	if err != nil {
		t.Fatal(err)
	}

	// Build a set of indexed file paths
	indexed := make(map[string]bool)
	for _, f := range files {
		indexed[f.Path] = true
	}

	// secret.md should NOT be indexed (excluded by .gitignore)
	if indexed["secret.md"] {
		t.Error("secret.md should NOT be in the files table (excluded by .gitignore)")
	}

	// chinese.md SHOULD be indexed
	if !indexed["chinese.md"] {
		t.Error("chinese.md should be in the files table")
		t.Logf("indexed files: %v", files)
	}

	// Verify CJK headings were parsed
	nodes, err := st.GetNodesByFile("chinese.md")
	if err != nil {
		t.Fatal(err)
	}
	headingFound := false
	for _, n := range nodes {
		if n.Kind == "heading" {
			headingFound = true
			break
		}
	}
	if !headingFound {
		t.Error("expected heading nodes in chinese.md (CJK headings)")
	}

	// Verify the document name from the H1
	doc, err := st.FindNodeByPath("chinese.md")
	if err != nil {
		t.Fatal(err)
	}
	if doc == nil {
		t.Fatal("chinese.md document node not found")
	}
	if doc.Name != "中文文件標題" {
		t.Errorf("expected document name '中文文件標題', got %q", doc.Name)
	}
}

// ---------------------------------------------------------------------------
// Test 6: Section content reading
// ---------------------------------------------------------------------------

func TestSectionContent(t *testing.T) {
	projectDir := fixtureDir(t, "project-a")
	st := indexTestProject(t, projectDir)

	// Test ReadSectionContent on a known heading.
	// doc-a.md has "## Section One" and "## Section Two".
	node, err := st.FindNodeByPath("doc-a.md")
	if err != nil || node == nil {
		t.Fatal("doc-a.md not found")
	}

	// Get headings to find "Section One".
	headings, err := st.GetChildHeadings("doc-a.md")
	if err != nil {
		t.Fatal(err)
	}

	var sectionOne *store.Node
	for i, h := range headings {
		if h.Name == "Section One" {
			sectionOne = &headings[i]
			break
		}
	}
	if sectionOne == nil {
		t.Fatal("heading 'Section One' not found")
	}

	content, err := store.ReadSectionContent(sectionOne.FilePath, sectionOne.StartLine, sectionOne.EndLine, projectDir, 2000)
	if err != nil {
		t.Fatalf("ReadSectionContent failed: %v", err)
	}

	if content == "" {
		t.Error("expected non-empty section content")
	}
	if !strings.Contains(content, "Content here") {
		t.Errorf("expected section content to contain 'Content here', got: %s", content)
	}

	// Test maxBytes cap.
	tiny, err := store.ReadSectionContent(sectionOne.FilePath, sectionOne.StartLine, sectionOne.EndLine, projectDir, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(tiny) > 100 { // 5 bytes + truncation message
		t.Errorf("expected truncated content, got %d bytes", len(tiny))
	}
	if !strings.Contains(tiny, "truncated") {
		t.Error("expected truncation message")
	}

	// Test path traversal rejection.
	_, err = store.ReadSectionContent("../../etc/passwd", 1, 10, projectDir, 2000)
	if err == nil {
		t.Error("expected error for path traversal, got nil")
	}
}

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
		nodes := append([]store.Node{res.DocNode}, res.Headings...)
		nodes = append(nodes, res.Defs...)
		nodes = append(nodes, res.Tags...)
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
		nodes := append([]store.Node{res.DocNode}, res.Headings...)
		nodes = append(nodes, res.Defs...)
		nodes = append(nodes, res.Tags...)
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

func TestIndexPathForceRebuildsDatabase(t *testing.T) {
	projectDir := t.TempDir()
	docPath := filepath.Join(projectDir, "doc.md")
	if err := os.WriteFile(docPath, []byte("# Doc\n\nInitial content.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := indexPathOpts(projectDir, false)
	stats, err := st.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.FileCount != 1 {
		t.Fatalf("expected initial index to contain 1 file, got %d", stats.FileCount)
	}
	st.Close()

	if err := os.Remove(docPath); err != nil {
		t.Fatal(err)
	}

	st = indexPathOpts(projectDir, true)
	defer st.Close()
	stats, err = st.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.FileCount != 0 {
		t.Fatalf("expected force rebuild to remove stale file rows, got %d files", stats.FileCount)
	}
}

func TestIndexPathRefreshesSimilarityAfterSingleFileChange(t *testing.T) {
	projectDir := t.TempDir()
	docA := filepath.Join(projectDir, "a.md")
	docB := filepath.Join(projectDir, "b.md")
	docC := filepath.Join(projectDir, "c.md")
	glossary := filepath.Join(projectDir, "glossary.md")
	if err := os.WriteFile(docA, []byte("---\ntags: [shared]\n---\n# Alpha\n\nAlpha links to [terms](glossary.md).\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docB, []byte("---\ntags: [shared]\n---\n# Beta\n\nBeta links to [terms](glossary.md).\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docC, []byte("# Quantum\n\nneutrino entropy lattice\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(glossary, []byte("# Glossary\n\nterms definitions\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := indexPath(projectDir)
	if err := st.InsertEdges([]store.Edge{{Source: "a.md", Target: "b.md", Kind: "similar_to"}}); err != nil {
		t.Fatal(err)
	}
	stats, err := st.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.EdgesByKind["similar_to"] == 0 {
		t.Fatalf("expected test setup to contain a stale similar_to edge, got stats: %+v", stats.EdgesByKind)
	}
	st.Close()

	if err := os.WriteFile(docB, []byte("# Volcano\n\nmagma basalt caldera\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st = indexPath(projectDir)
	defer st.Close()
	stats, err = st.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	if got := stats.EdgesByKind["similar_to"]; got != 0 {
		t.Fatalf("expected single-file change to refresh stale similar_to edges, got %d", got)
	}
}

func TestCmdSyncIndexesProject(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "doc.md"), []byte("# Synced\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmdSync([]string{projectDir})

	st, err := store.Open(filepath.Join(projectDir, ".docgraph", "docgraph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	stats, err := st.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.FileCount != 1 {
		t.Fatalf("expected sync to index 1 file, got %d", stats.FileCount)
	}
}

func TestCmdIndexSimilarityThreshold(t *testing.T) {
	projectDir := t.TempDir()
	docs := map[string]string{
		"a.md": "# Governance\n\npolicy security compliance architecture\n",
		"b.md": "# Security\n\npolicy security compliance audit\n",
		"c.md": "# Install\n\nquickstart tutorial setup\n",
	}
	for name, content := range docs {
		if err := os.WriteFile(filepath.Join(projectDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmdIndex([]string{"--threshold", "0.01", projectDir})

	st, err := store.Open(filepath.Join(projectDir, ".docgraph", "docgraph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	stats, err := st.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.EdgesByKind["similar_to"] == 0 {
		t.Fatalf("expected low threshold to create similar_to edges, got stats: %+v", stats.EdgesByKind)
	}
}

func TestCmdInitCreatesLocalConfig(t *testing.T) {
	projectDir := t.TempDir()

	cmdInit([]string{projectDir})

	for _, path := range []string{
		filepath.Join(projectDir, ".docgraph"),
		filepath.Join(projectDir, ".docgraphignore"),
		filepath.Join(projectDir, ".gitignore"),
		filepath.Join(projectDir, ".mcp.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected init artifact %s: %v", path, err)
		}
	}

	gitignore, err := os.ReadFile(filepath.Join(projectDir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gitignore), ".docgraph/") {
		t.Fatalf("expected .gitignore to contain .docgraph/, got %q", string(gitignore))
	}

	mcpConfig, err := os.ReadFile(filepath.Join(projectDir, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mcpConfig), `"docgraph"`) || !strings.Contains(string(mcpConfig), `"serve"`) {
		t.Fatalf("expected .mcp.json to configure docgraph serve, got %s", string(mcpConfig))
	}
}

func TestCmdInitInstallsSelectedMCPClients(t *testing.T) {
	projectDir := t.TempDir()
	home := t.TempDir()
	codexHome := filepath.Join(t.TempDir(), "codex")
	xdgConfig := filepath.Join(t.TempDir(), "xdg")
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("XDG_CONFIG_HOME", xdgConfig)

	cmdInit([]string{"--install-clients", "claude,codex,hermes,opencode", projectDir})

	assertJSONMCPArgs(t, filepath.Join(projectDir, ".mcp.json"), []string{"serve", "--path", "."})

	codexConfig := readFile(t, filepath.Join(codexHome, "config.toml"))
	if !strings.Contains(codexConfig, "[mcp_servers.docgraph]") ||
		!strings.Contains(codexConfig, filepath.ToSlash(projectDir)) {
		t.Fatalf("expected Codex config to contain docgraph entry for %s, got %s", projectDir, codexConfig)
	}

	hermesConfig := readFile(t, filepath.Join(home, ".hermes", "config.yaml"))
	if !strings.Contains(hermesConfig, "mcp_servers:") ||
		!strings.Contains(hermesConfig, "docgraph:") ||
		!strings.Contains(hermesConfig, projectDir) {
		t.Fatalf("expected Hermes config to contain docgraph entry for %s, got %s", projectDir, hermesConfig)
	}

	assertJSONMCPArgs(t, filepath.Join(xdgConfig, "opencode", "opencode.json"), []string{"serve", "--path", projectDir})
}

func TestCmdInstallAutoDetectsExistingMCPClientDirs(t *testing.T) {
	projectDir := t.TempDir()
	home := t.TempDir()
	codexHome := filepath.Join(t.TempDir(), "codex")
	xdgConfig := filepath.Join(t.TempDir(), "xdg")
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("XDG_CONFIG_HOME", xdgConfig)

	for _, dir := range []string{
		codexHome,
		filepath.Join(home, ".hermes"),
		filepath.Join(xdgConfig, "opencode"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cmdInstall([]string{"--clients", "auto", "--workspace", projectDir})

	assertJSONMCPArgs(t, filepath.Join(projectDir, ".mcp.json"), []string{"serve", "--workspace", projectDir})
	if got := readFile(t, filepath.Join(codexHome, "config.toml")); !strings.Contains(got, "--workspace") {
		t.Fatalf("expected auto-installed Codex config to use workspace mode, got %s", got)
	}
	if got := readFile(t, filepath.Join(home, ".hermes", "config.yaml")); !strings.Contains(got, "--workspace") {
		t.Fatalf("expected auto-installed Hermes config to use workspace mode, got %s", got)
	}
	assertJSONMCPArgs(t, filepath.Join(xdgConfig, "opencode", "opencode.json"), []string{"serve", "--workspace", projectDir})
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertJSONMCPArgs(t *testing.T, path string, want []string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var doc struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	got, ok := doc.MCPServers["docgraph"]
	if !ok {
		t.Fatalf("expected docgraph MCP server in %s, got %s", path, string(data))
	}
	if got.Command != "docgraph" {
		t.Fatalf("expected command docgraph in %s, got %q", path, got.Command)
	}
	if strings.Join(got.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("expected args %v in %s, got %v", want, path, got.Args)
	}
}

// assertNodeKindCount asserts stats.NodesByKind[kind] == want.
func assertNodeKindCount(t *testing.T, stats store.Stats, kind string, want int) {
	t.Helper()
	if got := stats.NodesByKind[kind]; got != want {
		t.Errorf("expected %d %s nodes, got %d", want, kind, got)
	}
}

// assertEdgeKindNonZero asserts that stats.EdgesByKind[kind] > 0.
func assertEdgeKindNonZero(t *testing.T, stats store.Stats, kind string) {
	t.Helper()
	if stats.EdgesByKind[kind] == 0 {
		t.Errorf("expected %s edges > 0, got 0", kind)
	}
}

// assertIncomingEdge asserts that the node identified by nodePath has an
// incoming edge of edgeKind whose Source satisfies matchFn.
func assertIncomingEdge(t *testing.T, st *store.Store, nodePath, edgeKind string, matchFn func(src string) bool, failMsg string) {
	t.Helper()
	node, err := st.FindNodeByPath(nodePath)
	if err != nil {
		t.Fatal(err)
	}
	if node == nil {
		t.Fatalf("%s document node not found", nodePath)
	}
	edges, err := st.GetIncomingEdges(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range edges {
		if e.Kind == edgeKind && matchFn(e.Source) {
			return
		}
	}
	t.Error(failMsg)
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

	// Step 4: Verify node and edge counts
	assertNodeKindCount(t, stats, "document", 4)
	if stats.NodesByKind["heading"] == 0 {
		t.Error("expected heading nodes > 0, got 0")
	}
	assertEdgeKindNonZero(t, stats, "references")
	assertEdgeKindNonZero(t, stats, "wikilinks_to")

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
	assertIncomingEdge(t, st, "doc-a.md", "references",
		func(src string) bool { return strings.HasPrefix(src, "README.md") },
		"missing reference edge from README.md to doc-a.md")

	// Step 7: nested → README reference edge (relative path ../README.md resolved)
	assertIncomingEdge(t, st, "README.md", "references",
		func(src string) bool { return strings.Contains(src, "nested") },
		"missing reference edge from subdir/nested.md to README.md")
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

// mcpStdioClient is a test helper that wraps an in-process MCP stdio connection.
// Use newMCPStdioClient to create one; call close when done.
type mcpStdioClient struct {
	stdinWriter  *io.PipeWriter
	scan         *bufio.Scanner
	serverErrCh  chan error
	cancelServer context.CancelFunc
}

// newMCPStdioClient starts an MCP stdio server backed by srv and returns a
// client connected via in-process pipes. The caller must call client.close(t)
// to drain the server error channel and assert no unexpected error occurred.
func newMCPStdioClient(t *testing.T, srv *mcpserver.MCPServer) *mcpStdioClient {
	t.Helper()
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	stdio := mcpserver.NewStdioServer(srv)
	stdio.SetErrorLogger(log.New(io.Discard, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		err := stdio.Listen(ctx, stdinReader, stdoutWriter)
		if err != nil && err != io.EOF && err != context.Canceled {
			errCh <- err
		}
		stdoutWriter.Close()
		close(errCh)
	}()
	return &mcpStdioClient{
		stdinWriter:  stdinWriter,
		scan:         bufio.NewScanner(stdoutReader),
		serverErrCh:  errCh,
		cancelServer: cancel,
	}
}

// sendAndRecv sends a JSON-RPC request and reads one response, verifying
// protocol version and matching id.
func (c *mcpStdioClient) sendAndRecv(t *testing.T, id int, method string, params map[string]any) map[string]any {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.stdinWriter.Write(append(reqBytes, '\n')); err != nil {
		t.Fatal(err)
	}
	if !c.scan.Scan() {
		t.Fatal("failed to read response from MCP server")
	}
	var resp map[string]any
	if err := json.Unmarshal(c.scan.Bytes(), &resp); err != nil {
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

// close tears down the server and asserts no unexpected error was received.
func (c *mcpStdioClient) close(t *testing.T) {
	t.Helper()
	c.cancelServer()
	c.stdinWriter.Close()
	if err := <-c.serverErrCh; err != nil {
		t.Errorf("unexpected server error: %v", err)
	}
}

// assertMCPInitialize verifies the initialize response contains a valid serverInfo.
func assertMCPInitialize(t *testing.T, resp map[string]any) {
	t.Helper()
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
}

// assertMCPToolResultText extracts the first text content item from a tool call
// response and returns it. Fatal if the response is an error or has no content.
func assertMCPToolResultText(t *testing.T, toolName string, resp map[string]any) string {
	t.Helper()
	if resp["error"] != nil {
		t.Fatalf("%s error: %v", toolName, resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result in %s response", toolName)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected non-empty content in %s result", toolName)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	return text
}

// ---------------------------------------------------------------------------
// Test 3: MCP round-trip via stdio pipe
// ---------------------------------------------------------------------------

func TestMCPRoundTrip(t *testing.T) {
	// Index project-a so there is data to query
	projectDir := fixtureDir(t, "project-a")
	st := indexTestProject(t, projectDir)

	// Create MCP server, register tools, and start stdio client
	srv := mcpserver.NewMCPServer("docgraph", "0.1.0")
	tools.Register(srv, st, projectDir)
	client := newMCPStdioClient(t, srv)
	defer client.close(t)

	// 1. Initialize
	assertMCPInitialize(t, client.sendAndRecv(t, 1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	}))

	// 2. Call docgraph_status
	statusText := assertMCPToolResultText(t, "docgraph_status", client.sendAndRecv(t, 2, "tools/call", map[string]any{
		"name":      "docgraph_status",
		"arguments": map[string]any{},
	}))
	if !strings.Contains(statusText, "Files:") {
		t.Errorf("expected status text to contain 'Files:', got: %s", statusText)
	}

	// 3. Call docgraph_search
	searchText := assertMCPToolResultText(t, "docgraph_search", client.sendAndRecv(t, 3, "tools/call", map[string]any{
		"name":      "docgraph_search",
		"arguments": map[string]any{"query": "Document", "limit": float64(5)},
	}))
	if !strings.Contains(searchText, "Search Results") {
		t.Errorf("expected search text to contain 'Search Results', got: %s", searchText)
	}
	// Should find results (not "Found 0 results")
	if strings.Contains(searchText, "Found 0 results") {
		t.Error("search for 'Document' returned 0 results, expected matches")
	}

	// 4. Call docgraph_context and verify bounded source content is included.
	contextText := assertMCPToolResultText(t, "docgraph_context", client.sendAndRecv(t, 4, "tools/call", map[string]any{
		"name":      "docgraph_context",
		"arguments": map[string]any{"task": "Document", "maxNodes": float64(1), "maxContentBytes": float64(1000)},
	}))
	if !strings.Contains(contextText, "#### Content") || !strings.Contains(contextText, "```markdown") {
		t.Errorf("expected context output to include bounded markdown content, got: %s", contextText)
	}
}

// assertWorkspaceSearchFinds searches for query in the workspace and asserts
// that at least one result's QualifiedName contains projectSubstr.
func assertWorkspaceSearchFinds(t *testing.T, w *workspace.Workspace, query, projectSubstr string) {
	t.Helper()
	results, err := w.Search(query, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Errorf("search for %q returned 0 results, expected matches from %s", query, projectSubstr)
		return
	}
	for _, r := range results {
		if strings.Contains(r.Node.QualifiedName, projectSubstr) {
			return
		}
	}
	t.Errorf("search for %q did not include %s results", query, projectSubstr)
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
	assertWorkspaceSearchFinds(t, w, "Document", "project-a")

	// Step 4: Search for "Glossary" → should find results from project-b
	assertWorkspaceSearchFinds(t, w, "Glossary", "project-b")

	// Step 5: Search for "中文" → should find results from project-c
	// Note: len([]rune("中文")) == 2 < 3, so this uses the LIKE branch
	assertWorkspaceSearchFinds(t, w, "中文", "project-c")
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

// ftsSearchFinds reports whether a Search for term returns a result in wantFile.
func ftsSearchFinds(t *testing.T, st *store.Store, term, wantFile string) bool {
	t.Helper()
	res, err := st.Searcher.Search(term, "", 20)
	if err != nil {
		t.Fatalf("search %q: %v", term, err)
	}
	for _, r := range res {
		if r.Node.FilePath == wantFile {
			return true
		}
	}
	return false
}

// TestIndexFTSBulkRebuildAndIncrementalUpdate exercises the section_chunks_fts
// bulk-rebuild fast path end-to-end: a full build must populate the FTS via
// 'rebuild' (terms placed PAST the 500-char body_excerpt are only findable through
// section_chunks_fts), and a subsequent single-file change must update the FTS via
// the recreated triggers — with the final graph identical to a from-scratch build.
func TestIndexFTSBulkRebuildAndIncrementalUpdate(t *testing.T) {
	projectDir := t.TempDir()
	// >500 chars of filler so the distinctive term lives beyond nodes.body_excerpt
	// (capped at 500), making it reachable ONLY via section_chunks_fts.
	filler := strings.Repeat("padding word ", 60) // ~780 chars
	docA := filepath.Join(projectDir, "a.md")
	docB := filepath.Join(projectDir, "b.md")
	if err := os.WriteFile(docA, []byte("# Alpha\n\n"+filler+" zebracrossing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docB, []byte("# Beta\n\n"+filler+" tangerine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Full build → FTS empty at start → bulk-rebuild fast path.
	st := indexPathOpts(projectDir, true)
	if !ftsSearchFinds(t, st, "zebracrossing", "a.md") {
		t.Error("full build: section_chunks_fts did not index 'zebracrossing' (rebuild path broken)")
	}
	if !ftsSearchFinds(t, st, "tangerine", "b.md") {
		t.Error("full build: section_chunks_fts did not index 'tangerine'")
	}
	st.Close()

	// Change one file → incremental reindex (FTS non-empty → recreated triggers).
	if err := os.WriteFile(docA, []byte("# Alpha\n\n"+filler+" kumquat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st = indexPath(projectDir)
	if !ftsSearchFinds(t, st, "kumquat", "a.md") {
		t.Error("incremental: trigger did not index new term 'kumquat' (trigger not recreated)")
	}
	if ftsSearchFinds(t, st, "zebracrossing", "a.md") {
		t.Error("incremental: stale term 'zebracrossing' still indexed")
	}
	if !ftsSearchFinds(t, st, "tangerine", "b.md") {
		t.Error("incremental: untouched file b.md no longer found")
	}
	incr, err := st.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Count-equivalence: a from-scratch rebuild of the final on-disk state must
	// match the incremental result.
	st = indexPathOpts(projectDir, true)
	defer st.Close()
	fresh, err := st.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	if incr.NodeCount != fresh.NodeCount || incr.EdgeCount != fresh.EdgeCount {
		t.Errorf("incremental vs fresh-rebuild mismatch: nodes %d/%d edges %d/%d",
			incr.NodeCount, fresh.NodeCount, incr.EdgeCount, fresh.EdgeCount)
	}
	if !ftsSearchFinds(t, st, "kumquat", "a.md") {
		t.Error("fresh rebuild of final state: 'kumquat' not findable")
	}
}

// TestIndexFTSSelfHealsEmptyFTS covers the crash-recovery path: if a build dies
// after bulk-loading section_chunks but before the FTS rebuild, the DB has base
// rows + an empty FTS + dropped triggers. The next indexStore must detect the
// empty FTS (fullBuild) and rebuild even though every file hash-skips (nNew==0).
func TestIndexFTSSelfHealsEmptyFTS(t *testing.T) {
	projectDir := t.TempDir()
	filler := strings.Repeat("padding word ", 60)
	if err := os.WriteFile(filepath.Join(projectDir, "a.md"),
		[]byte("# Alpha\n\n"+filler+" zebracrossing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := indexPathOpts(projectDir, true)
	if !ftsSearchFinds(t, st, "zebracrossing", "a.md") {
		t.Fatal("setup: term not indexed by full build")
	}
	// Simulate the crash window: base rows intact, FTS emptied, triggers dropped.
	if err := st.Fts.DropSectionFTSTriggers(); err != nil {
		t.Fatal(err)
	}
	if err := st.Fts.DeleteAllSectionFTS(); err != nil {
		t.Fatal(err)
	}
	if empty, _ := st.Fts.SectionFTSIsEmpty(); !empty {
		t.Fatal("setup: FTS should be empty after delete-all")
	}
	if ftsSearchFinds(t, st, "zebracrossing", "a.md") {
		t.Fatal("setup: term should be unfindable while FTS is empty")
	}
	st.Close()

	// Incremental reindex (force=false): every file hash-skips (nNew==0), but the
	// empty FTS must still trigger a rebuild that repopulates section search.
	st = indexPath(projectDir)
	defer st.Close()
	if !ftsSearchFinds(t, st, "zebracrossing", "a.md") {
		t.Error("self-heal failed: empty FTS was not rebuilt on a hash-skip-only run")
	}
}

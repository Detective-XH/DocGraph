// ============================================================================
// DEVELOPER-ONLY TOOL — NEVER COMPILED INTO RELEASE BINARIES
//
// This file carries "//go:build ignore" so that:
//   go build ./...   — SKIPS this package entirely
//   go test ./...    — SKIPS this package entirely
//   release skill    — cross-compiles only the root package (go build . …)
//
// To run: pass the file DIRECTLY (build constraints are bypassed for file args):
//
//	go run ./cmd/ax-assert/main.go --project-root /path/to/project
//
// DO NOT remove the build constraint. DO NOT add this path to the release
// cross-compile matrix. DO NOT ship ax-assert as part of any docgraph release.
// ============================================================================
//
// cmd/ax-assert — AX Layer-1b render/determinism assertion runner.
//
// Calls tool handlers in-process over the real .docgraph index (zero LLM, zero network).
// Covers the render/determinism layer of A0–A5, A7–A11, A16/A17/A21: verifies that
// the current code + current index produce the correct output strings.
//
// Does NOT detect deploy-lag (whether the live server runs the current binary) — that
// requires a live gate call (at minimum A0 against the running MCP server).
//
// serverInstructions + tool-description assertions (A6/A12/A13/A14/A15/A18/A19/A20)
// are covered by tool_surface_test.go CI — this binary does not duplicate them.
//
// Exit 0 if all assertions pass; exit 1 if any fail; exit 2 on fatal setup error.

//go:build ignore

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/Detective-XH/docgraph/internal/tools"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func main() {
	checkModulePath()
	projectRoot := flag.String("project-root", ".", "project root (directory containing .docgraph/)")
	flag.Parse()

	root, err := filepath.Abs(*projectRoot)
	if err != nil {
		fatalf("resolve project-root: %v", err)
	}

	dbPath := filepath.Join(root, ".docgraph", "docgraph.db")
	if _, err := os.Stat(dbPath); err != nil {
		fatalf("DB not found at %s\nHint: run 'docgraph index --no-gitignore %s' first (plans/ is gitignored by default)", dbPath, root)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		fatalf("store.Open: %v", err)
	}
	defer st.Close()

	// Pre-flight: verify required documents are in the index.
	required := []string{
		"plans/PERF-METHODOLOGY.md",
		"plans/CHANGELOG-v0.2.x.md",
		"CHANGELOG.md",
	}
	for _, doc := range required {
		nodes, err := st.GetNodesByFile(doc)
		if err != nil || len(nodes) == 0 {
			fatalf("required document not in index: %s\nHint: index was likely built without --no-gitignore (plans/ is gitignored by default)", doc)
		}
	}

	// Find a document that has BOTH incoming and outgoing edges for A4.
	a4doc, err := findDocWithBothEdges(st, []string{
		"plans/ax-audit-workflow.md",
		"plans/CHANGELOG-v0.2.x.md",
		"plans/PERF-METHODOLOGY.md",
	})
	if err != nil {
		fatalf("A4 pre-flight: %v", err)
	}

	r, cleanup := newRunner(st, root)
	defer cleanup()

	// Find a document with similarity results for A1/A5 by probing candidates via the tool.
	// GetOutgoingEdges excludes similar_to (similarity-engine edges), so we call the tool.
	a1doc, err := findDocWithSimilarityResults(r, []string{
		"plans/ax-audit-workflow.md",
		"plans/CHANGELOG-v0.1.x.md",
		"plans/CHANGELOG-v0.2.x.md",
	})
	if err != nil {
		fatalf("A1 pre-flight: %v", err)
	}

	type assertion struct {
		id   string
		tool string
		args map[string]any
		must []string
	}

	assertions := []assertion{
		{
			id:   "A0",
			tool: "docgraph_graph",
			args: map[string]any{"operation": "outgoing", "document": "plans/PERF-METHODOLOGY.md", "limit": 15},
			must: []string{"distinct other documents"},
		},
		{
			id:   "A1",
			tool: "docgraph_similar",
			args: map[string]any{"document": a1doc},
			must: []string{"0-1 weighted blend", "signals behind this score"},
		},
		{
			id:   "A2",
			tool: "docgraph_similar",
			args: map[string]any{"document": "CHANGELOG.md"},
			must: []string{"not a misconfiguration", "keyword- and link-based"},
		},
		{
			id:   "A3",
			tool: "docgraph_files",
			args: map[string]any{"path": "__nonexistent_ax_assert__"},
			must: []string{"No indexed files found under path", "Known top-level"},
		},
		{
			id:   "A4",
			tool: "docgraph_node",
			args: map[string]any{"document": a4doc},
			must: []string{"outgoing edges →", "incoming edges ←"},
		},
		{
			id:   "A7",
			tool: "docgraph_search",
			args: map[string]any{"query": "CHANGELOG-v0.2.x", "limit": 5},
			must: []string{"plans/CHANGELOG-v0.2.x.md"},
		},
		{
			id:   "A8",
			tool: "docgraph_context",
			args: map[string]any{"task": "performance optimization work"},
			must: []string{"Also matched", "in this same document"},
		},
		{
			id:   "A9",
			tool: "docgraph_search",
			args: map[string]any{"query": "performance"},
			must: []string{"parent document:"},
		},
		{
			id:   "A10",
			tool: "docgraph_graph",
			args: map[string]any{"operation": "outgoing", "document": "plans/PERF-METHODOLOGY.md", "limit": 15},
			must: []string{"same-document reference"},
		},
		{
			id:   "A11",
			tool: "docgraph_graph",
			args: map[string]any{"operation": "outgoing", "document": "plans/PERF-METHODOLOGY.md", "limit": 15},
			must: []string{"report the distinct-document count, not the edge-row count"},
		},
		{
			id:   "A16",
			tool: "docgraph_search",
			args: map[string]any{"query": "docgraph", "limit": 3},
			must: []string{"distinct file(s) — count distinct files, not rows"},
		},
		{
			id:   "A17",
			tool: "docgraph_node",
			args: map[string]any{"document": "CHANGELOG.md"},
			must: []string{"does not satisfy the frontmatter governance status field"},
		},
		{
			id:   "A21",
			tool: "docgraph_search",
			args: map[string]any{"query": "the", "limit": 1},
			must: []string{"page count, not a corpus-wide distinct-document total"},
		},
	}

	failed := 0
	total := len(assertions) + 1 // +1 for A5

	for _, a := range assertions {
		text, err := r.call(a.tool, a.args)
		if err != nil {
			fmt.Printf("FAIL %s  (%d bytes) — error: %v\n", a.id, 0, err)
			failed++
			continue
		}
		pass := true
		for _, want := range a.must {
			if !strings.Contains(text, want) {
				fmt.Printf("FAIL %s  (%d bytes) — missing: %q\n", a.id, len(text), want)
				pass = false
				break
			}
		}
		if pass {
			fmt.Printf("PASS %s  (%d bytes)\n", a.id, len(text))
		} else {
			failed++
		}
	}

	// A5: determinism — two identical calls must produce byte-identical output.
	text1, err1 := r.call("docgraph_similar", map[string]any{"document": a1doc})
	text2, err2 := r.call("docgraph_similar", map[string]any{"document": a1doc})
	switch {
	case err1 != nil || err2 != nil:
		fmt.Printf("FAIL A5  (%d bytes) — error: %v / %v\n", 0, err1, err2)
		failed++
	case text1 != text2:
		fmt.Printf("FAIL A5  (%d/%d bytes) — outputs not byte-identical\n", len(text1), len(text2))
		failed++
	default:
		fmt.Printf("PASS A5  (%d bytes)\n", len(text1))
	}

	fmt.Printf("\n%d/%d passed\n", total-failed, total)
	if failed > 0 {
		os.Exit(1)
	}
}

// findDocWithSimilarityResults probes each candidate by calling docgraph_similar and
// returns the first that produces at least one result (contains "0-1 weighted blend").
// GetOutgoingEdges excludes similar_to edges, so we probe via the tool itself.
func findDocWithSimilarityResults(r *runner, candidates []string) (string, error) {
	for _, doc := range candidates {
		text, err := r.call("docgraph_similar", map[string]any{"document": doc})
		if err != nil {
			continue
		}
		if strings.Contains(text, "0-1 weighted blend") {
			return doc, nil
		}
	}
	return "", fmt.Errorf("no candidate document has similarity results (tried %v) — ensure similarity index is built", candidates)
}

// findDocWithBothEdges returns the first candidate path that has at least one
// incoming edge and at least one outgoing edge in the index.
func findDocWithBothEdges(st *store.Store, candidates []string) (string, error) {
	for _, doc := range candidates {
		in, err := st.GetIncomingEdges(doc)
		if err != nil || len(in) == 0 {
			continue
		}
		out, err := st.GetOutgoingEdges(doc)
		if err != nil || len(out) == 0 {
			continue
		}
		return doc, nil
	}
	return "", fmt.Errorf("no candidate document has both incoming and outgoing edges (tried %v) — ensure the index is built with --no-gitignore", candidates)
}

// runner holds a persistent in-process MCP stdio connection to the tool server.
type runner struct {
	sendAndRecv func(id int, method string, params map[string]any) map[string]any
	stdinWriter *io.PipeWriter
	nextID      int
}

func newRunner(st *store.Store, projectRoot string) (*runner, func()) {
	srv := mcpserver.NewMCPServer("ax-assert", "0.1.0")
	tools.RegisterWithOpts(srv, st, projectRoot, tools.RegisterOpts{})

	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	stdio := mcpserver.NewStdioServer(srv)
	stdio.SetErrorLogger(log.New(io.Discard, "", 0))

	ctx, cancel := context.WithCancel(context.Background())
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
	scan.Buffer(make([]byte, 1<<20), 1<<20) // 1 MiB — tool responses can be large

	send := func(id int, method string, params map[string]any) map[string]any {
		req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
		if params != nil {
			req["params"] = params
		}
		b, _ := json.Marshal(req)
		if _, err := stdinWriter.Write(append(b, '\n')); err != nil {
			return nil
		}
		if !scan.Scan() {
			return nil
		}
		var resp map[string]any
		if err := json.Unmarshal(scan.Bytes(), &resp); err != nil {
			return nil
		}
		return resp
	}

	r := &runner{sendAndRecv: send, stdinWriter: stdinWriter, nextID: 1}

	// MCP initialize handshake.
	r.sendAndRecv(r.nextID, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "ax-assert", "version": "0.1"},
	})
	r.nextID++

	cleanup := func() {
		cancel()
		stdinWriter.Close()
		<-serverErrCh
	}
	return r, cleanup
}

func (r *runner) call(toolName string, args map[string]any) (string, error) {
	resp := r.sendAndRecv(r.nextID, "tools/call", map[string]any{
		"name":      toolName,
		"arguments": args,
	})
	r.nextID++

	if resp == nil {
		return "", fmt.Errorf("no response from server")
	}
	if errObj := resp["error"]; errObj != nil {
		return "", fmt.Errorf("tool call error: %v", errObj)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("unexpected result shape: %v", resp["result"])
	}
	content, ok := result["content"].([]any)
	if !ok {
		return "", fmt.Errorf("result has no content array")
	}
	for _, c := range content {
		if m, ok := c.(map[string]any); ok {
			if m["type"] == "text" {
				if text, ok := m["text"].(string); ok {
					return text, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no text content in result")
}

// checkModulePath blocks execution when the binary was compiled from a module
// with an unexpected path — catching the case where an attacker removes the
// //go:build ignore constraint, rebuilds, and redistributes the binary from a
// fork with a renamed go.mod. In file-mode runs (go run ./cmd/ax-assert/main.go)
// Main.Path is empty, so the check is skipped; primary defence there is
// go mod verify against the checksum database (see CI test.yml and S-6).
func checkModulePath() {
	const want = "github.com/Detective-XH/docgraph"
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Path == "" {
		return // file-mode run or build-info unavailable — allow
	}
	if info.Main.Path != want {
		fmt.Fprintf(os.Stderr, "ax-assert: SECURITY: module path is %q, expected %q\n", info.Main.Path, want)
		fmt.Fprintf(os.Stderr, "ax-assert: Do not run this tool from untrusted forks. Run 'go mod verify' first.\n")
		os.Exit(2)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ax-assert: "+format+"\n", args...)
	os.Exit(2)
}

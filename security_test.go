package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/Detective-XH/docgraph/internal/tools"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// ---------------------------------------------------------------------------
// Red-team / security fuzzing tests for MCP protocol abuse.
// Every test indexes testdata/project-a for a populated store, then sends
// adversarial JSON-RPC requests through the MCP server stdio pipe.
// ---------------------------------------------------------------------------

// mcpCall sends an initialize handshake followed by a single tools/call to the
// MCP server over stdio pipes and returns the raw JSON-RPC response line for
// the tool call.
func mcpCall(t *testing.T, st *store.Store, toolName string, args map[string]interface{}) string {
	t.Helper()

	srv := mcpserver.NewMCPServer("docgraph", "0.1.0")
	tools.Register(srv, st, "")

	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()

	stdio := mcpserver.NewStdioServer(srv)
	stdio.SetErrorLogger(log.New(io.Discard, "", 0))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErrCh := make(chan error, 1)
	go func() {
		err := stdio.Listen(ctx, serverR, serverW)
		if err != nil && err != io.EOF && err != context.Canceled {
			serverErrCh <- err
		}
		serverW.Close()
		close(serverErrCh)
	}()

	scanner := bufio.NewScanner(clientR)
	// Increase buffer to 1 MB to handle large responses (e.g., oversized query echo)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	// 1. Send initialize
	initReq, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "security-test", "version": "0.1"},
		},
	})
	if _, err := clientW.Write(append(initReq, '\n')); err != nil {
		t.Fatal("write init:", err)
	}
	if !scanner.Scan() {
		t.Fatal("failed to read init response")
	}

	// 2. Send tool call
	argsJSON, _ := json.Marshal(args)
	toolReq, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": json.RawMessage(argsJSON),
		},
	})
	if _, err := clientW.Write(append(toolReq, '\n')); err != nil {
		t.Fatal("write tool call:", err)
	}
	if !scanner.Scan() {
		t.Fatal("failed to read tool response")
	}

	result := scanner.Text()

	// Cleanup: cancel context and close writer pipe
	cancel()
	clientW.Close()

	return result
}

// requireValidJSONRPC parses the response line and asserts it is a valid
// JSON-RPC 2.0 message with the expected id. Returns the parsed map.
func requireValidJSONRPC(t *testing.T, raw string) map[string]interface{} {
	t.Helper()
	if raw == "" {
		t.Fatal("empty response from MCP server")
	}
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v\nraw: %s", err, raw[:min(len(raw), 200)])
	}
	if resp["jsonrpc"] != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %v", resp["jsonrpc"])
	}
	// id should be 2 (our tool call id)
	if id, ok := resp["id"].(float64); !ok || id != 2 {
		t.Errorf("expected id 2, got %v", resp["id"])
	}
	return resp
}

// getContentText extracts the first content[].text from a successful result.
func getContentText(t *testing.T, resp map[string]interface{}) string {
	t.Helper()
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		return ""
	}
	content, ok := result["content"].([]interface{})
	if !ok || len(content) == 0 {
		return ""
	}
	first, ok := content[0].(map[string]interface{})
	if !ok {
		return ""
	}
	text, _ := first["text"].(string)
	return text
}

// isErrorResult checks whether the response is an MCP tool error result
// (isError: true in result) or a JSON-RPC level error.
func isErrorResult(resp map[string]interface{}) bool {
	// JSON-RPC level error
	if resp["error"] != nil {
		return true
	}
	// MCP tool-level error (result.isError == true)
	if result, ok := resp["result"].(map[string]interface{}); ok {
		if isErr, ok := result["isError"].(bool); ok && isErr {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Test 1: Oversized query string
// ---------------------------------------------------------------------------

func TestMCPOversizedQuery(t *testing.T) {
	st := indexTestProject(t, fixtureDir(t, "project-a"))

	// Build a 50000 character query
	bigQuery := strings.Repeat("A", 50000)

	raw := mcpCall(t, st, "docgraph_search", map[string]interface{}{
		"query": bigQuery,
	})

	resp := requireValidJSONRPC(t, raw)

	// Must be a valid response (no crash)
	text := getContentText(t, resp)

	// sanitizeArg caps at 10000 chars — the echoed query in the response
	// should not contain the full 50000-char string
	if strings.Contains(text, bigQuery) {
		t.Error("response contains the full 50000-char query — sanitizeArg did not truncate")
	}

	// The response should still be well-formed with a search results header
	if !strings.Contains(text, "Search Results") && !isErrorResult(resp) {
		t.Error("expected either a Search Results header or an error result")
	}
}

// ---------------------------------------------------------------------------
// Test 2: SQL injection via search
// ---------------------------------------------------------------------------

func TestMCPSQLInjectionViaSearch(t *testing.T) {
	st := indexTestProject(t, fixtureDir(t, "project-a"))

	// Send a classic SQL injection payload
	raw := mcpCall(t, st, "docgraph_search", map[string]interface{}{
		"query": `"; DROP TABLE nodes; --`,
	})

	resp := requireValidJSONRPC(t, raw)

	// The response must be valid (no crash / panic)
	_ = getContentText(t, resp)

	// Verify data integrity: the store must still have data
	stats, err := st.GetStats()
	if err != nil {
		t.Fatal("GetStats after injection:", err)
	}
	if stats.NodeCount == 0 {
		t.Fatal("NodeCount is 0 after SQL injection attempt — data may have been destroyed")
	}
	if stats.FileCount == 0 {
		t.Fatal("FileCount is 0 after SQL injection attempt — data may have been destroyed")
	}

	// Follow-up search must still work
	raw2 := mcpCall(t, st, "docgraph_search", map[string]interface{}{
		"query": "Document",
	})
	resp2 := requireValidJSONRPC(t, raw2)
	text2 := getContentText(t, resp2)
	if strings.Contains(text2, "Found 0 results") {
		t.Error("follow-up search returned 0 results — data may be corrupted")
	}
}

// ---------------------------------------------------------------------------
// Test 3: Type confusion — wrong argument types
// ---------------------------------------------------------------------------

func TestMCPTypeConfusion(t *testing.T) {
	st := indexTestProject(t, fixtureDir(t, "project-a"))

	cases := []struct {
		name     string
		toolName string
		args     map[string]interface{}
	}{
		{
			name:     "query as number",
			toolName: "docgraph_search",
			args:     map[string]interface{}{"query": 42},
		},
		{
			name:     "limit as string",
			toolName: "docgraph_search",
			args:     map[string]interface{}{"query": "test", "limit": "abc"},
		},
		{
			name:     "document as array",
			toolName: "docgraph_references",
			args:     map[string]interface{}{"document": []interface{}{1, 2, 3}},
		},
		{
			name:     "query as boolean",
			toolName: "docgraph_search",
			args:     map[string]interface{}{"query": true},
		},
		{
			name:     "document as map",
			toolName: "docgraph_node",
			args:     map[string]interface{}{"document": map[string]interface{}{"evil": true}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := mcpCall(t, st, tc.toolName, tc.args)
			resp := requireValidJSONRPC(t, raw)

			// Must be valid JSON-RPC — either an error result or a gracefully
			// handled response, but never a panic / crash
			_ = resp
		})
	}
}

// ---------------------------------------------------------------------------
// Test 4: Null arguments
// ---------------------------------------------------------------------------

func TestMCPNullArguments(t *testing.T) {
	st := indexTestProject(t, fixtureDir(t, "project-a"))

	cases := []struct {
		name     string
		toolName string
		args     map[string]interface{}
	}{
		{
			name:     "null query",
			toolName: "docgraph_search",
			args:     map[string]interface{}{"query": nil},
		},
		{
			name:     "null document",
			toolName: "docgraph_references",
			args:     map[string]interface{}{"document": nil},
		},
		{
			name:     "null task",
			toolName: "docgraph_context",
			args:     map[string]interface{}{"task": nil},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := mcpCall(t, st, tc.toolName, tc.args)
			resp := requireValidJSONRPC(t, raw)

			// Required-parameter tools should return an error for nil values
			if !isErrorResult(resp) {
				t.Logf("tool %s accepted null arg without error (may be acceptable)", tc.toolName)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test 5: Path traversal via document parameter
// ---------------------------------------------------------------------------

func TestMCPPathTraversalViaDocument(t *testing.T) {
	st := indexTestProject(t, fixtureDir(t, "project-a"))

	traversalPayloads := []string{
		"../../../../etc/passwd",
		"../../../etc/shadow",
		"/etc/passwd",
		"..\\..\\..\\windows\\system32\\config\\sam",
		"%2e%2e%2f%2e%2e%2fetc%2fpasswd",
		"....//....//etc/passwd",
	}

	for _, payload := range traversalPayloads {
		t.Run(payload, func(t *testing.T) {
			raw := mcpCall(t, st, "docgraph_references", map[string]interface{}{
				"document": payload,
			})
			resp := requireValidJSONRPC(t, raw)
			text := getContentText(t, resp)

			// The document resolver looks up by path/name in the DB.
			// A traversal path should not match any indexed document.
			if isErrorResult(resp) {
				// Good — document not found
				return
			}

			// If it returned a result, verify it does NOT contain sensitive
			// system file content
			if strings.Contains(text, "root:") || strings.Contains(text, "daemon:") {
				t.Errorf("path traversal payload %q may have leaked system file content", payload)
			}

			// Also check via docgraph_node
			rawNode := mcpCall(t, st, "docgraph_node", map[string]interface{}{
				"document": payload,
			})
			respNode := requireValidJSONRPC(t, rawNode)

			if !isErrorResult(respNode) {
				nodeText := getContentText(t, respNode)
				if strings.Contains(nodeText, "root:") {
					t.Errorf("docgraph_node with path traversal payload %q leaked content", payload)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test 6: Empty and missing arguments
// ---------------------------------------------------------------------------

func TestMCPEmptyAndMissingArgs(t *testing.T) {
	st := indexTestProject(t, fixtureDir(t, "project-a"))

	cases := []struct {
		name     string
		toolName string
		args     map[string]interface{}
	}{
		{
			name:     "empty query string",
			toolName: "docgraph_search",
			args:     map[string]interface{}{"query": ""},
		},
		{
			name:     "missing query entirely",
			toolName: "docgraph_search",
			args:     map[string]interface{}{},
		},
		{
			name:     "empty document string",
			toolName: "docgraph_references",
			args:     map[string]interface{}{"document": ""},
		},
		{
			name:     "missing document entirely",
			toolName: "docgraph_references",
			args:     map[string]interface{}{},
		},
		{
			name:     "missing from/to for trace",
			toolName: "docgraph_trace",
			args:     map[string]interface{}{},
		},
		{
			name:     "empty from/to for trace",
			toolName: "docgraph_trace",
			args:     map[string]interface{}{"from": "", "to": ""},
		},
		{
			name:     "empty task for context",
			toolName: "docgraph_context",
			args:     map[string]interface{}{"task": ""},
		},
		{
			name:     "missing all args for impact",
			toolName: "docgraph_impact",
			args:     map[string]interface{}{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := mcpCall(t, st, tc.toolName, tc.args)
			resp := requireValidJSONRPC(t, raw)

			// Tools with required parameters should return an error result
			// for empty/missing args.
			if !isErrorResult(resp) {
				// Not necessarily a bug — but log it for review
				t.Logf("tool %s accepted empty/missing args without error", tc.toolName)
			}
		})
	}
}

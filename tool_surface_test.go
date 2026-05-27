package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/tools"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const maxInstructionsBytes = 2700

func TestToolSurfaceRegistry(t *testing.T) {
	expected := []string{
		"docgraph_context",
		"docgraph_embeddings",
		"docgraph_enrichment",
		"docgraph_explore",
		"docgraph_files",
		"docgraph_graph",
		"docgraph_history",
		"docgraph_node",
		"docgraph_search",
		"docgraph_similar",
		"docgraph_status",
		"docgraph_tags",
	}

	actual := registeredToolNames(t, tools.ToolProfileCompact)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf(
			"registered MCP tool surface changed.\nexpected: %s\nactual:   %s\nIf this is intentional, add a tool-surface decision record and update the allowlist, server instructions, and docs together.",
			strings.Join(expected, ", "),
			strings.Join(actual, ", "),
		)
	}
}

func TestToolSurfaceInstructionsStayCompact(t *testing.T) {
	section := markdownSection(serverInstructions, "## Tool selection", "## Reducing noise")
	if section == "" {
		t.Fatal("server instructions must include a Tool selection section before Reducing noise")
	}
	for _, hidden := range []string{"docgraph_references", "docgraph_links", "docgraph_impact", "docgraph_trace", "docgraph_embeddings_pending", "docgraph_embeddings_store", "docgraph_embeddings_clear"} {
		if strings.Contains(section, hidden) {
			t.Fatalf("server instructions must not name hidden tool %s", hidden)
		}
	}
	if !strings.Contains(section, "docgraph_graph") {
		t.Fatal("server instructions must route graph work through docgraph_graph")
	}
	if !strings.Contains(section, "docgraph_embeddings(action=pending/store/clear)") {
		t.Fatal("server instructions must route embedding work through docgraph_embeddings facade")
	}
	dataRows := countMarkdownDataRows(section)
	if dataRows > 6 {
		t.Fatalf("Tool selection table has %d data rows; keep to <= 6", dataRows)
	}
}

func TestServerInstructionsFitBudget(t *testing.T) {
	if len(serverInstructions) > maxInstructionsBytes {
		t.Fatalf("serverInstructions is %d bytes; must be <= %d to fit injection budget", len(serverInstructions), maxInstructionsBytes)
	}
}

func TestCodeGraphInteropInstructionsStayAdvisory(t *testing.T) {
	section := markdownSection(serverInstructions, "## CodeGraph interoperability", "")
	if section == "" {
		t.Fatal("serverInstructions must include CodeGraph interoperability guidance")
	}

	required := []string{
		"advisory only",
		"does not call CodeGraph",
		"read .codegraph/",
		"codegraph_anchor metadata field stays empty",
		"stable export/API contract",
		"codegraph_* MCP tools",
		"ask the user before running codegraph init -i",
	}
	for _, phrase := range required {
		if !strings.Contains(section, phrase) {
			t.Fatalf("CodeGraph interoperability guidance missing %q", phrase)
		}
	}
}

func registeredToolNames(t *testing.T, profile tools.ToolProfile) []string {
	t.Helper()

	srv := mcpserver.NewMCPServer("docgraph", "0.1.0")
	tools.RegisterWithProfile(srv, nil, "", profile)

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
	sendAndRecv := func(id int, method string, params map[string]any) map[string]any {
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
			t.Fatalf("failed to unmarshal MCP response: %v", err)
		}
		if resp["error"] != nil {
			t.Fatalf("%s returned error: %v", method, resp["error"])
		}
		return resp
	}

	sendAndRecv(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "tool-surface-test", "version": "0.1"},
	})

	resp := sendAndRecv(2, "tools/list", nil)
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatal("tools/list response is missing result object")
	}
	rawTools, ok := result["tools"].([]any)
	if !ok {
		t.Fatal("tools/list response is missing tools array")
	}

	names := make([]string, 0, len(rawTools))
	for _, raw := range rawTools {
		toolObj, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("tool entry has unexpected shape: %#v", raw)
		}
		name, ok := toolObj["name"].(string)
		if !ok || name == "" {
			t.Fatalf("tool entry is missing name: %#v", raw)
		}
		names = append(names, name)
	}
	sort.Strings(names)

	cancel()
	stdinWriter.Close()
	if err := <-serverErrCh; err != nil {
		t.Errorf("unexpected MCP server error: %v", err)
	}

	return names
}

func markdownSection(markdown, startHeading, endHeading string) string {
	_, rest, found := strings.Cut(markdown, startHeading)
	if !found {
		return ""
	}
	if endHeading == "" {
		return strings.TrimSpace(rest)
	}
	section, _, _ := strings.Cut(rest, endHeading)
	return strings.TrimSpace(section)
}

func countMarkdownDataRows(markdown string) int {
	rows := 0
	for line := range strings.SplitSeq(markdown, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
			continue
		}
		if strings.Contains(line, "---") || strings.Contains(line, "Intent") {
			continue
		}
		rows++
	}
	return rows
}

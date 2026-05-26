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

func TestToolSurfaceGovernanceRegistry(t *testing.T) {
	// Tool-surface guardrail: adding a top-level MCP tool changes the agent-facing
	// protocol. This allowlist forces that change to be intentional and reviewed.
	expected := []string{
		"docgraph_context",
		"docgraph_embeddings_clear",
		"docgraph_embeddings_pending",
		"docgraph_embeddings_store",
		"docgraph_explore",
		"docgraph_files",
		"docgraph_history",
		"docgraph_impact",
		"docgraph_links",
		"docgraph_node",
		"docgraph_references",
		"docgraph_search",
		"docgraph_similar",
		"docgraph_status",
		"docgraph_tags",
		"docgraph_trace",
	}

	actual := registeredToolNames(t)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf(
			"registered MCP tool surface changed.\nexpected: %s\nactual:   %s\nIf this is intentional, add a tool-surface decision record and update the allowlist, server instructions, and docs together.",
			strings.Join(expected, ", "),
			strings.Join(actual, ", "),
		)
	}
}

func TestToolSurfaceGovernanceInstructionsStayCompact(t *testing.T) {
	// Tool-surface guardrail: generated MCP instructions route users through compact
	// decision paths instead of repeating a long one-row-per-tool catalog.
	section := markdownSection(serverInstructions, "## Tool selection", "## Reducing noise")
	if section == "" {
		t.Fatal("serverInstructions must include a Tool selection section before Reducing noise")
	}

	dataRows := countMarkdownDataRows(section)
	if dataRows > 6 {
		t.Fatalf("Tool selection table has %d data rows; keep generated instructions to a compact decision tree", dataRows)
	}

	table := firstMarkdownTable(section)
	if strings.Count(table, "docgraph_") > 16 {
		t.Fatalf("Tool selection section repeats too many tool names; route through grouped primary surfaces instead")
	}
}

func TestCodeGraphInteropInstructionsStayAdvisory(t *testing.T) {
	// CodeGraph interoperability guardrail: starts as agent handoff
	// guidance only. DocGraph must not imply that it owns CodeGraph internals.
	section := markdownSection(serverInstructions, "## CodeGraph interoperability", "## Managing .docgraphignore")
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

func registeredToolNames(t *testing.T) []string {
	t.Helper()

	srv := mcpserver.NewMCPServer("docgraph", "0.1.0")
	tools.Register(srv, nil, "")

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
	start := strings.Index(markdown, startHeading)
	if start < 0 {
		return ""
	}
	rest := markdown[start+len(startHeading):]
	end := strings.Index(rest, endHeading)
	if end < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
}

func countMarkdownDataRows(markdown string) int {
	rows := 0
	for _, line := range strings.Split(markdown, "\n") {
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

func firstMarkdownTable(markdown string) string {
	var lines []string
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "|") && strings.HasSuffix(line, "|") {
			lines = append(lines, line)
			continue
		}
		if len(lines) > 0 {
			break
		}
	}
	return strings.Join(lines, "\n")
}

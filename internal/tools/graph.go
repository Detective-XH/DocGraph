package tools

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

var impactTool = mcp.NewTool("docgraph_impact",
	mcp.WithDescription("Analyze what documents would be affected if this document changes. Traverses incoming references transitively. Start with depth=1 for focused results."),
	mcp.WithString("document", mcp.Required(), mcp.Description("Document name or path")),
	mcp.WithNumber("depth", mcp.Description("Levels of transitive references (default 2, max 5; use 1 for focused results)")),
)
var traceTool = mcp.NewTool("docgraph_trace",
	mcp.WithDescription("Find the shortest reference path between two documents via BFS. Max 10 hops. Use to understand HOW two docs are connected; use docgraph_impact to find WHAT would be affected by a change."),
	mcp.WithString("from", mcp.Required(), mcp.Description("Starting document name or path")),
	mcp.WithString("to", mcp.Required(), mcp.Description("Target document name or path")),
)

func (h *handler) getDocID(nodeID string) string {
	if n := h.getNodeByID(nodeID); n != nil {
		if n.Kind == "document" {
			return n.ID
		}
		return n.FilePath
	}
	return nodeID
}
func (h *handler) edgesOf(nodeID string, incoming bool) []store.Edge {
	get := func(s *store.Store) ([]store.Edge, error) {
		if incoming {
			return s.GetIncomingEdges(nodeID)
		}
		return s.GetOutgoingEdges(nodeID)
	}
	if h.workspace != nil {
		var all []store.Edge
		for _, p := range h.workspace.Projects {
			if es, err := get(p.Store); err == nil {
				all = append(all, es...)
			}
		}
		return all
	}
	es, _ := get(h.store)
	return es
}
func (h *handler) nodeName(id string) (string, string) {
	if n := h.getNodeByID(id); n != nil {
		return n.Name, n.FilePath
	}
	return id, id
}
func (h *handler) resolveOrErr(s string) (*store.Node, *mcp.CallToolResult) {
	node, err := h.resolveDoc(s)
	if err != nil {
		return nil, mcp.NewToolResultError(fmt.Sprintf("resolve failed: %v", err))
	}
	if node == nil {
		return nil, mcp.NewToolResultError(fmt.Sprintf("not found: %s — try docgraph_search to find the correct name or path", s))
	}
	return node, nil
}

type impactEntry struct{ docID, kind, via string }
type traceHop struct{ from, kind string }

func (h *handler) handleImpact(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	doc := getStringArg(req.GetArguments(), "document", "")
	if doc == "" {
		return mcp.NewToolResultError("document parameter is required"), nil
	}
	doc = sanitizeArg(doc, maxArgLength)
	depth := getIntArg(req.GetArguments(), "depth", 2)

	return h.renderImpact(doc, depth)
}

func (h *handler) renderImpact(doc string, depth int) (*mcp.CallToolResult, error) {
	if depth < 1 {
		depth = 1
	} else if depth > 5 {
		depth = 5
	}
	node, e := h.resolveOrErr(doc)
	if e != nil {
		return e, nil
	}
	startID := h.getDocID(node.ID)
	visited, queue := map[string]bool{startID: true}, []string{startID}
	levels, total := make(map[int][]impactEntry), 0
	for lv := 1; lv <= depth && len(queue) > 0; lv++ {
		var next []string
		for _, id := range queue {
			for _, edge := range h.edgesOf(id, true) {
				src := h.getDocID(edge.Source)
				if visited[src] {
					continue
				}
				visited[src] = true
				next = append(next, src)
				via := ""
				if lv > 1 {
					via = id
				}
				levels[lv] = append(levels[lv], impactEntry{src, edge.Kind, via})
				total++
			}
		}
		queue = next
	}
	const maxPerLevel = 20
	startName, _ := h.nodeName(startID)
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Impact Analysis for %q\n", startName)
	for lv := 1; lv <= depth; lv++ {
		if len(levels[lv]) == 0 {
			continue
		}
		label := "direct references"
		if lv > 1 {
			label = "transitive"
		}
		fmt.Fprintf(&sb, "\nDepth %d (%s): %d documents\n", lv, label, len(levels[lv]))
		shown := levels[lv]
		if len(shown) > maxPerLevel {
			shown = shown[:maxPerLevel]
		}
		for _, ent := range shown {
			nm, fp := h.nodeName(ent.docID)
			if ent.via != "" {
				vn, _ := h.nodeName(ent.via)
				fmt.Fprintf(&sb, "- %s (%s) → %s %s\n", nm, fp, ent.kind, vn)
			} else {
				fmt.Fprintf(&sb, "- %s (%s) via %s\n", nm, fp, ent.kind)
			}
		}
		if len(levels[lv]) > maxPerLevel {
			fmt.Fprintf(&sb, "- (and %d more)\n", len(levels[lv])-maxPerLevel)
		}
	}
	fmt.Fprintf(&sb, "\nTotal: %d documents affected\n", total)
	return mcp.NewToolResultText(sb.String()), nil
}
func (h *handler) handleTrace(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	from, to := getStringArg(args, "from", ""), getStringArg(args, "to", "")
	if from == "" || to == "" {
		return mcp.NewToolResultError("both 'from' and 'to' parameters are required"), nil
	}
	from = sanitizeArg(from, maxArgLength)
	to = sanitizeArg(to, maxArgLength)

	return h.renderTrace(from, to)
}

func (h *handler) renderTrace(from string, to string) (*mcp.CallToolResult, error) {
	fNode, e := h.resolveOrErr(from)
	if e != nil {
		return e, nil
	}
	tNode, e := h.resolveOrErr(to)
	if e != nil {
		return e, nil
	}
	fID, tID := h.getDocID(fNode.ID), h.getDocID(tNode.ID)
	if fID == tID {
		return mcp.NewToolResultText(fmt.Sprintf("## Trace: %q → %q\n\nSame document.\n", fNode.Name, tNode.Name)), nil
	}
	parent, visited := map[string]traceHop{}, map[string]bool{fID: true}
	queue, found := []string{fID}, false
	for lv := 0; lv < 10 && !found && len(queue) > 0; lv++ {
		var next []string
		for _, id := range queue {
			for _, edge := range h.edgesOf(id, false) {
				if edge.Kind == "links_external" {
					continue
				}
				tgt := h.getDocID(edge.Target)
				if visited[tgt] {
					continue
				}
				parent[tgt] = traceHop{id, edge.Kind}
				if tgt == tID {
					found = true
					break
				}
				visited[tgt] = true
				next = append(next, tgt)
			}
			if found {
				break
			}
		}
		queue = next
	}
	if !found {
		return mcp.NewToolResultText(fmt.Sprintf("## Trace: %q → %q\n\nNo path found within 10 hops.\n", fNode.Name, tNode.Name)), nil
	}
	var path, kinds []string
	for cur := tID; cur != fID; {
		hop := parent[cur]
		path = append(path, cur)
		kinds = append(kinds, hop.kind)
		cur = hop.from
	}
	path = append(path, fID)
	slices.Reverse(path)
	slices.Reverse(kinds)
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Trace: %q → %q\n\nPath found (%d hops):\n", fNode.Name, tNode.Name, len(path)-1)
	for i, id := range path {
		nm, fp := h.nodeName(id)
		fmt.Fprintf(&sb, "\n%d. **%s** (%s)\n", i+1, nm, fp)
		if i < len(kinds) {
			fmt.Fprintf(&sb, "   ↓ %s\n", kinds[i])
		}
	}
	return mcp.NewToolResultText(sb.String()), nil
}

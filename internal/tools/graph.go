package tools

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
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

// edgesOfBatch calls GetIncomingEdgesBatch across all project stores and merges
// results in project-iteration order (matches edgesOf serial behavior).
func (h *handler) edgesOfBatch(ids []string) map[string][]store.Edge {
	if h.workspace != nil {
		merged := make(map[string][]store.Edge)
		for _, p := range h.workspace.Projects {
			if batch, err := p.Store.GetIncomingEdgesBatch(ids); err == nil {
				for nodeID, edges := range batch {
					merged[nodeID] = append(merged[nodeID], edges...)
				}
			}
		}
		return merged
	}
	result, _ := h.store.GetIncomingEdgesBatch(ids)
	if result == nil {
		return make(map[string][]store.Edge)
	}
	return result
}

// batchNodes loads nodes by ID with first-match workspace semantics,
// mirroring getNodeByID's per-project iteration order.
func (h *handler) batchNodes(ids []string) map[string]*store.Node {
	if len(ids) == 0 {
		return make(map[string]*store.Node)
	}
	if h.workspace != nil {
		out := make(map[string]*store.Node, len(ids))
		for _, p := range h.workspace.Projects {
			ns, _ := p.Store.GetNodesByIDs(ids)
			for id, n := range ns {
				if _, exists := out[id]; !exists {
					out[id] = n // first-match: don't overwrite
				}
			}
		}
		return out
	}
	ns, _ := h.store.GetNodesByIDs(ids)
	if ns == nil {
		return make(map[string]*store.Node)
	}
	return ns
}

// getDocIDFromCache replicates getDocID's three-way fallback using a node cache.
func getDocIDFromCache(id string, cache map[string]*store.Node) string {
	if n, ok := cache[id]; ok && n != nil {
		if n.Kind == "document" {
			return n.ID
		}
		return n.FilePath
	}
	return id
}

// nodeNameFromCache replicates nodeName's fallback using a node cache.
func nodeNameFromCache(id string, cache map[string]*store.Node) (string, string) {
	if n, ok := cache[id]; ok && n != nil {
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
	nodeCache := make(map[string]*store.Node)

	for lv := 1; lv <= depth && len(queue) > 0; lv++ {
		// Pattern 1: one batch call per level replaces N per-frontier queries.
		edgeMap := h.edgesOfBatch(queue)

		// Pattern 2: batch-resolve edge sources for getDocID (called per-edge in serial).
		var newSources []string
		seenSrc := make(map[string]bool)
		for _, edges := range edgeMap {
			for _, edge := range edges {
				if !seenSrc[edge.Source] {
					seenSrc[edge.Source] = true
					if _, cached := nodeCache[edge.Source]; !cached {
						newSources = append(newSources, edge.Source)
					}
				}
			}
		}
		if len(newSources) > 0 {
			maps.Copy(nodeCache, h.batchNodes(newSources))
		}

		var next []string
		for _, id := range queue { // walk queue in original order
			for _, edge := range edgeMap[id] { // edges are in SQL ORDER BY order
				src := getDocIDFromCache(edge.Source, nodeCache)
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

	// Pattern 3: batch-load all render-phase node IDs not yet in cache.
	var renderIDs []string
	renderSeen := make(map[string]bool)
	addRenderID := func(id string) {
		if !renderSeen[id] {
			renderSeen[id] = true
			if _, cached := nodeCache[id]; !cached {
				renderIDs = append(renderIDs, id)
			}
		}
	}
	addRenderID(startID)
	for _, entries := range levels {
		for _, ent := range entries {
			addRenderID(ent.docID)
			if ent.via != "" {
				addRenderID(ent.via)
			}
		}
	}
	if len(renderIDs) > 0 {
		maps.Copy(nodeCache, h.batchNodes(renderIDs))
	}

	const maxPerLevel = 20
	startName, _ := nodeNameFromCache(startID, nodeCache)
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
			nm, fp := nodeNameFromCache(ent.docID, nodeCache)
			if ent.via != "" {
				vn, _ := nodeNameFromCache(ent.via, nodeCache)
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
				// GetOutgoingEdges already restricts to the reference-edge family
				// (references, wikilinks_to, related_to, embeds) plus links_external;
				// links_external is the one non-navigational kind it returns, so
				// skipping it leaves only forward reference/link edges to traverse.
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
		return mcp.NewToolResultText(fmt.Sprintf("## Trace: %q → %q\n\nNo reference path found within 10 hops (trace follows forward markdown links, wikilinks, and embeds). This does NOT mean the documents are unrelated — they may link in the reverse direction (try operation=incoming) or share tags or topical similarity (try docgraph_similar).\n", fNode.Name, tNode.Name)), nil
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

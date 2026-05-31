package tools

// Batch-equivalence tests for renderImpact and contextPackImpactLevels.
//
// Each test calls a serial reference implementation (which mirrors the
// pre-batch logic) and the batch implementation, then asserts byte-identical
// output. The serial references are permanent test oracles — DO NOT DELETE.
// They self-maintain: if the fixture changes, both paths change together.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/Detective-XH/docgraph/internal/workspace"
)

// renderImpactSerialRef is the pre-batch serial implementation of renderImpact.
// Permanent test oracle for TestGraphImpactBatchEquivalence. DO NOT DELETE.
func (h *handler) renderImpactSerialRef(doc string, depth int) (string, error) {
	if depth < 1 {
		depth = 1
	} else if depth > 5 {
		depth = 5
	}
	node, e := h.resolveOrErr(doc)
	if e != nil {
		return "", fmt.Errorf("resolveOrErr: %v", e)
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
	return sb.String(), nil
}

// appendContextPackImpactSerialRef is the pre-batch serial implementation of
// appendContextPackImpact. Permanent test oracle for
// TestContextPackImpactBatchEquivalence. DO NOT DELETE.
func (h *handler) appendContextPackImpactSerialRef(sb *strings.Builder, st *store.Store, docID string, depth, limit int) {
	if st == nil || docID == "" {
		return
	}
	visited := map[string]bool{docID: true}
	queue := []string{docID}
	levels := make(map[int][]impactEntry)
	for level := 1; level <= depth && len(queue) > 0; level++ {
		var next []string
		for _, id := range queue {
			edges, err := st.GetIncomingEdges(id)
			if err != nil {
				continue
			}
			for _, edge := range edges {
				n, err2 := st.GetNodeByID(edge.Source)
				var src string
				if err2 != nil || n == nil {
					src = edge.Source
				} else {
					src = contextPackDocID(*n)
				}
				if visited[src] {
					continue
				}
				visited[src] = true
				next = append(next, src)
				via := ""
				if level > 1 {
					via = id
				}
				levels[level] = append(levels[level], impactEntry{docID: src, kind: edge.Kind, via: via})
			}
		}
		queue = next
	}
	sb.WriteString("\n### Impacted Documents\n")
	total := 0
	for level := 1; level <= depth; level++ {
		entries := levels[level]
		total += len(entries)
		sb.WriteString(fmt.Sprintf("- **Depth %d:** %d documents\n", level, len(entries)))
		shown := entries
		if len(shown) > limit {
			shown = shown[:limit]
		}
		for _, entry := range shown {
			n := h.getNodeByIDFromStore(st, entry.docID)
			if entry.via != "" {
				via := h.getNodeByIDFromStore(st, entry.via)
				sb.WriteString(fmt.Sprintf("  - %s via %s through %s\n",
					contextPackNodeLabel(n, entry.docID), entry.kind, contextPackNodeLabel(via, entry.via)))
			} else {
				sb.WriteString(fmt.Sprintf("  - %s via %s\n", contextPackNodeLabel(n, entry.docID), entry.kind))
			}
		}
		if len(entries) > limit {
			sb.WriteString(fmt.Sprintf("  - ... %d more impacted documents omitted\n", len(entries)-limit))
		}
	}
	sb.WriteString(fmt.Sprintf("- **Total impacted:** %d\n", total))
}

// setupImpactFixture builds a store with a topology that covers:
//   - depth=3 linear chain (d1←c1←b1←hub)
//   - fan-out: b1, b2, b3, ca, doc-e, f01..f21 → hub (26 at depth=1; tests truncation at 20)
//   - cycle: ca←hub, cb←ca, ca→cb (confirms visited dedup; no infinite loop)
//   - via annotation at depth≥2 (c1 via b1, cb via ca, d1 via c1)
//   - tie case: doc-e has two edges targeting hub's file_path (doc node + heading node)
//     with different kinds (references < wikilinks_to); ORDER BY picks references first
func setupImpactFixture(t *testing.T) (*handler, *store.Store) {
	t.Helper()
	h, st := newTestHandler(t)

	docs := []store.Node{
		{ID: "hub.md", Kind: "document", Name: "Hub", QualifiedName: "hub.md", FilePath: "hub.md", UpdatedAt: 1},
		{ID: "b1.md", Kind: "document", Name: "B1", QualifiedName: "b1.md", FilePath: "b1.md", UpdatedAt: 1},
		{ID: "b2.md", Kind: "document", Name: "B2", QualifiedName: "b2.md", FilePath: "b2.md", UpdatedAt: 1},
		{ID: "b3.md", Kind: "document", Name: "B3", QualifiedName: "b3.md", FilePath: "b3.md", UpdatedAt: 1},
		{ID: "c1.md", Kind: "document", Name: "C1", QualifiedName: "c1.md", FilePath: "c1.md", UpdatedAt: 1},
		{ID: "d1.md", Kind: "document", Name: "D1", QualifiedName: "d1.md", FilePath: "d1.md", UpdatedAt: 1},
		{ID: "ca.md", Kind: "document", Name: "CycA", QualifiedName: "ca.md", FilePath: "ca.md", UpdatedAt: 1},
		{ID: "cb.md", Kind: "document", Name: "CycB", QualifiedName: "cb.md", FilePath: "cb.md", UpdatedAt: 1},
		{ID: "doc-e.md", Kind: "document", Name: "DocE", QualifiedName: "doc-e.md", FilePath: "doc-e.md", UpdatedAt: 1},
		// heading node in hub.md — same file_path as hub.md (for tie case)
		{ID: "hub.md#intro", Kind: "heading", Name: "Intro", QualifiedName: "hub.md#intro", FilePath: "hub.md", UpdatedAt: 1},
	}
	for i := 1; i <= 21; i++ {
		id := fmt.Sprintf("f%02d.md", i)
		docs = append(docs, store.Node{
			ID:            id,
			Kind:          "document",
			Name:          fmt.Sprintf("F%02d", i),
			QualifiedName: id,
			FilePath:      id,
			UpdatedAt:     1,
		})
	}
	if err := st.InsertNodes(docs); err != nil {
		t.Fatal(err)
	}

	edges := []store.Edge{
		// depth=1 direct refs to hub.md
		{Source: "b1.md", Target: "hub.md", Kind: "references", Line: 1},
		{Source: "b2.md", Target: "hub.md", Kind: "references", Line: 2},
		{Source: "b3.md", Target: "hub.md", Kind: "references", Line: 3},
		// depth=2 chain: c1 references b1 (via=b1.md)
		{Source: "c1.md", Target: "b1.md", Kind: "wikilinks_to", Line: 4},
		// depth=3 chain: d1 references c1 (via=c1.md)
		{Source: "d1.md", Target: "c1.md", Kind: "references", Line: 5},
		// cycle: ca → hub (depth=1), cb → ca (depth=2), ca → cb (back-edge; ca visited → skip at depth=3)
		{Source: "ca.md", Target: "hub.md", Kind: "wikilinks_to", Line: 6},
		{Source: "cb.md", Target: "ca.md", Kind: "references", Line: 7},
		{Source: "ca.md", Target: "cb.md", Kind: "references", Line: 8},
		// tie case: two edges from doc-e.md targeting hub.md's file_path
		// ORDER BY source,kind,line,target → "references" (line=10) before "wikilinks_to" (line=11)
		// BFS uses kind from first edge; second edge is skipped (doc-e.md already visited)
		{Source: "doc-e.md", Target: "hub.md", Kind: "references", Line: 10},
		{Source: "doc-e.md", Target: "hub.md#intro", Kind: "wikilinks_to", Line: 11},
	}
	for i := 1; i <= 21; i++ {
		edges = append(edges, store.Edge{
			Source: fmt.Sprintf("f%02d.md", i),
			Target: "hub.md",
			Kind:   "references",
			Line:   100 + i,
		})
	}
	if err := st.InsertEdges(edges); err != nil {
		t.Fatal(err)
	}
	return h, st
}

func TestGraphImpactBatchEquivalence(t *testing.T) {
	h, _ := setupImpactFixture(t)

	for _, depth := range []int{1, 2, 3, 5} {
		t.Run(fmt.Sprintf("depth=%d", depth), func(t *testing.T) {
			want, err := h.renderImpactSerialRef("hub.md", depth)
			if err != nil {
				t.Fatalf("serial ref error: %v", err)
			}
			gotRes, err := h.renderImpact("hub.md", depth)
			if err != nil {
				t.Fatalf("batch error: %v", err)
			}
			if gotRes.IsError {
				t.Fatalf("batch returned tool error: %s", extractText(gotRes))
			}
			got := extractText(gotRes)
			if want != got {
				t.Fatalf("batch output differs at depth=%d:\n--- serial\n%s\n+++ batch\n%s", depth, want, got)
			}
		})
	}
}

func TestContextPackImpactBatchEquivalence(t *testing.T) {
	h, st := setupImpactFixture(t)

	for _, tc := range []struct {
		depth int
		limit int
	}{
		{depth: 1, limit: 10},
		{depth: 2, limit: 10},
		{depth: 3, limit: 10},
		{depth: 3, limit: 3}, // tests truncation + "N more" path
	} {
		t.Run(fmt.Sprintf("depth=%d_limit=%d", tc.depth, tc.limit), func(t *testing.T) {
			var wantSB strings.Builder
			h.appendContextPackImpactSerialRef(&wantSB, st, "hub.md", tc.depth, tc.limit)
			want := wantSB.String()

			var gotSB strings.Builder
			h.appendContextPackImpact(&gotSB, st, "hub.md", tc.depth, tc.limit)
			got := gotSB.String()

			if want != got {
				t.Fatalf("batch output differs at depth=%d limit=%d:\n--- serial\n%s\n+++ batch\n%s",
					tc.depth, tc.limit, want, got)
			}
		})
	}
}

// setupWorkspaceImpactFixture creates a two-project workspace handler.
//
// Project alpha: hub.md, b1.md, b2.md, c1.md, d1.md, ca.md
//   Edges (all intra-store):  b1→hub, b2→hub, c1→b1, d1→c1, ca→hub
//
// Project beta: hub.md (duplicate for FK), f01.md, f02.md
//   Edges: f01→hub, f02→hub
//
// Exercises workspace-specific branches:
//   - edgesOfBatch per-project merge: hub.md is doc in alpha (doc-branch) and
//     beta (doc-branch); results merged in project-iteration order (alpha then beta)
//   - batchNodes first-match: hub.md found in alpha first; f01/f02 found only in beta
//   - depth=2 uses only alpha's store (c1 found via b1); beta contributes only depth=1 fan-out
func setupWorkspaceImpactFixture(t *testing.T) *handler {
	t.Helper()
	_, stA := newTestHandler(t)
	_, stB := newTestHandler(t)

	if err := stA.InsertNodes([]store.Node{
		{ID: "hub.md", Kind: "document", Name: "Hub", QualifiedName: "hub.md", FilePath: "hub.md", UpdatedAt: 1},
		{ID: "b1.md", Kind: "document", Name: "B1", QualifiedName: "b1.md", FilePath: "b1.md", UpdatedAt: 1},
		{ID: "b2.md", Kind: "document", Name: "B2", QualifiedName: "b2.md", FilePath: "b2.md", UpdatedAt: 1},
		{ID: "c1.md", Kind: "document", Name: "C1", QualifiedName: "c1.md", FilePath: "c1.md", UpdatedAt: 1},
		{ID: "d1.md", Kind: "document", Name: "D1", QualifiedName: "d1.md", FilePath: "d1.md", UpdatedAt: 1},
		{ID: "ca.md", Kind: "document", Name: "CycA", QualifiedName: "ca.md", FilePath: "ca.md", UpdatedAt: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := stA.InsertEdges([]store.Edge{
		{Source: "b1.md", Target: "hub.md", Kind: "references", Line: 1},
		{Source: "b2.md", Target: "hub.md", Kind: "references", Line: 2},
		{Source: "c1.md", Target: "b1.md", Kind: "wikilinks_to", Line: 4},
		{Source: "d1.md", Target: "c1.md", Kind: "references", Line: 5},
		{Source: "ca.md", Target: "hub.md", Kind: "wikilinks_to", Line: 6},
	}); err != nil {
		t.Fatal(err)
	}
	// beta has its own copy of hub.md (FK) and adds two more referrers
	if err := stB.InsertNodes([]store.Node{
		{ID: "hub.md", Kind: "document", Name: "Hub", QualifiedName: "hub.md", FilePath: "hub.md", UpdatedAt: 1},
		{ID: "f01.md", Kind: "document", Name: "F01", QualifiedName: "f01.md", FilePath: "f01.md", UpdatedAt: 1},
		{ID: "f02.md", Kind: "document", Name: "F02", QualifiedName: "f02.md", FilePath: "f02.md", UpdatedAt: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := stB.InsertEdges([]store.Edge{
		{Source: "f01.md", Target: "hub.md", Kind: "references", Line: 101},
		{Source: "f02.md", Target: "hub.md", Kind: "references", Line: 102},
	}); err != nil {
		t.Fatal(err)
	}
	return &handler{workspace: &workspace.Workspace{Projects: []*workspace.Project{
		{Name: "alpha", Path: t.TempDir(), Store: stA},
		{Name: "beta", Path: t.TempDir(), Store: stB},
	}}}
}

// TestGraphImpactBatchEquivalenceWorkspace verifies byte-identical output between
// the serial reference and the batch implementation in workspace (multi-store) mode.
// Exercises edgesOfBatch's per-project merge and batchNodes's first-match semantics.
func TestGraphImpactBatchEquivalenceWorkspace(t *testing.T) {
	h := setupWorkspaceImpactFixture(t)
	for _, depth := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("depth=%d", depth), func(t *testing.T) {
			want, err := h.renderImpactSerialRef("hub.md", depth)
			if err != nil {
				t.Fatalf("serial ref error: %v", err)
			}
			gotRes, err := h.renderImpact("hub.md", depth)
			if err != nil {
				t.Fatalf("batch error: %v", err)
			}
			if gotRes.IsError {
				t.Fatalf("batch returned tool error: %s", extractText(gotRes))
			}
			got := extractText(gotRes)
			if want != got {
				t.Fatalf("workspace batch output differs at depth=%d:\n--- serial\n%s\n+++ batch\n%s", depth, want, got)
			}
		})
	}
}

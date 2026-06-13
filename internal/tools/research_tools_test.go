package tools

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/Detective-XH/docgraph/internal/workspace"
)

func openToolTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestHandleSearchResearchFilter(t *testing.T) {
	h, st := newTestHandler(t)

	node := store.Node{
		ID:            "research-alpha.md",
		Kind:          "document",
		Name:          "Research Alpha",
		QualifiedName: "research-alpha.md",
		FilePath:      "research-alpha.md",
		StartLine:     1,
		EndLine:       10,
		BodyExcerpt:   "alpha research evidence",
		UpdatedAt:     1,
	}
	if err := st.InsertNodes([]store.Node{node}); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	tuples := []store.MetadataTuple{
		{Key: "claim_id", Value: "claim-alpha-001", ValueType: "string", Source: "frontmatter"},
		{Key: "source_type", Value: "primary", ValueType: "string", Source: "frontmatter"},
		{Key: "confidence", Value: "high", ValueType: "string", Source: "frontmatter"},
	}
	if err := st.UpsertResearchMetadata("research-alpha.md", tuples); err != nil {
		t.Fatalf("UpsertResearchMetadata: %v", err)
	}

	res, err := callTool(h, h.handleSearch, map[string]any{
		"query":       "alpha",
		"source_type": "primary",
		"confidence":  "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	text := extractText(res)
	if !strings.Contains(text, "Research Alpha") {
		t.Errorf("expected research result in output, got:\n%s", text)
	}
	if !strings.Contains(text, "source_type") || !strings.Contains(text, "confidence") {
		t.Errorf("expected research filters in output, got:\n%s", text)
	}
}

func TestHandleSearchResearchFilterAppliesLimitAfterFTS(t *testing.T) {
	h, st := newTestHandler(t)

	nodes := []store.Node{
		{
			ID:            "aaa-nonmatch.md",
			Kind:          "document",
			Name:          "AAA Nonmatch",
			QualifiedName: "aaa-nonmatch.md",
			FilePath:      "aaa-nonmatch.md",
			StartLine:     1,
			EndLine:       10,
			BodyExcerpt:   "unrelated body",
			UpdatedAt:     1,
		},
		{
			ID:            "zzz-match.md",
			Kind:          "document",
			Name:          "ZZZ Match",
			QualifiedName: "zzz-match.md",
			FilePath:      "zzz-match.md",
			StartLine:     1,
			EndLine:       10,
			BodyExcerpt:   "alpha body",
			UpdatedAt:     1,
		},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	for _, id := range []string{"aaa-nonmatch.md", "zzz-match.md"} {
		if err := st.UpsertResearchMetadata(id, []store.MetadataTuple{
			{Key: "source_type", Value: "primary", ValueType: "string", Source: "frontmatter"},
			{Key: "confidence", Value: "high", ValueType: "string", Source: "frontmatter"},
		}); err != nil {
			t.Fatalf("UpsertResearchMetadata %s: %v", id, err)
		}
	}

	res, err := callTool(h, h.handleSearch, map[string]any{
		"query":       "alpha",
		"source_type": "primary",
		"confidence":  "high",
		"limit":       float64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	text := extractText(res)
	if !strings.Contains(text, "ZZZ Match") {
		t.Errorf("expected FTS-matching research result after applying limit, got:\n%s", text)
	}
	if strings.Contains(text, "AAA Nonmatch") {
		t.Errorf("did not expect non-FTS result, got:\n%s", text)
	}
}

func TestHandleSearchResearchFilterDoesNotDropLowRankMetadataHit(t *testing.T) {
	h, st := newTestHandler(t)

	var nodes []store.Node
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("non-metadata-%02d.md", i)
		nodes = append(nodes, store.Node{
			ID:            id,
			Kind:          "document",
			Name:          fmt.Sprintf("Alpha Non Metadata %02d", i),
			QualifiedName: id,
			FilePath:      id,
			StartLine:     1,
			EndLine:       10,
			BodyExcerpt:   "alpha common body",
			UpdatedAt:     1,
		})
	}
	target := store.Node{
		ID:            "zzzz-research-hit.md",
		Kind:          "document",
		Name:          "ZZZZ Research Hit",
		QualifiedName: "zzzz-research-hit.md",
		FilePath:      "zzzz-research-hit.md",
		StartLine:     1,
		EndLine:       10,
		BodyExcerpt:   "alpha common body",
		UpdatedAt:     1,
	}
	nodes = append(nodes, target)
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	if err := st.UpsertResearchMetadata(target.ID, []store.MetadataTuple{
		{Key: "source_type", Value: "primary", ValueType: "string", Source: "frontmatter"},
		{Key: "confidence", Value: "high", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertResearchMetadata: %v", err)
	}

	res, err := callTool(h, h.handleSearch, map[string]any{
		"query":       "alpha",
		"source_type": "primary",
		"confidence":  "high",
		"limit":       float64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	text := extractText(res)
	if !strings.Contains(text, "ZZZZ Research Hit") {
		t.Errorf("expected metadata hit even when it is not in the first FTS page, got:\n%s", text)
	}
}

func TestHandleContextWorkspaceUsesResultProjectStore(t *testing.T) {
	storeA := openToolTestStore(t)
	storeB := openToolTestStore(t)

	sharedA := store.Node{
		ID:            "shared.md",
		Kind:          "document",
		Name:          "Shared A",
		QualifiedName: "shared.md",
		FilePath:      "shared.md",
		StartLine:     1,
		EndLine:       5,
		BodyExcerpt:   "alpha only",
		UpdatedAt:     1,
	}
	sharedB := store.Node{
		ID:            "shared.md",
		Kind:          "document",
		Name:          "Shared B",
		QualifiedName: "shared.md",
		FilePath:      "shared.md",
		StartLine:     1,
		EndLine:       5,
		BodyExcerpt:   "target research body",
		UpdatedAt:     1,
	}
	if err := storeA.InsertNodes([]store.Node{sharedA}); err != nil {
		t.Fatalf("InsertNodes storeA: %v", err)
	}
	if err := storeB.InsertNodes([]store.Node{sharedB}); err != nil {
		t.Fatalf("InsertNodes storeB: %v", err)
	}
	if err := storeB.UpsertSectionChunks([]store.SectionChunk{{
		NodeID:      "shared.md",
		FilePath:    "shared.md",
		StartLine:   1,
		EndLine:     5,
		ContentHash: "hash-b",
		SectionHash: "section-b",
		Text:        "target project-b chunk",
	}}); err != nil {
		t.Fatalf("UpsertSectionChunks: %v", err)
	}
	if err := storeB.UpsertResearchMetadata("shared.md", []store.MetadataTuple{
		{Key: "claim_id", Value: "claim-project-b", ValueType: "string", Source: "frontmatter"},
		{Key: "confidence", Value: "high", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertResearchMetadata: %v", err)
	}

	h := &handler{workspace: &workspace.Workspace{Projects: []*workspace.Project{
		{Name: "project-a", Path: t.TempDir(), Store: storeA},
		{Name: "project-b", Path: t.TempDir(), Store: storeB},
	}}}

	res, err := callTool(h, h.handleContext, map[string]any{
		"task":            "target",
		"maxNodes":        float64(1),
		"maxContentBytes": float64(2000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	text := extractText(res)
	for _, want := range []string{"Shared B", "target project-b chunk", "claim-project-b"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q from project-b scoped store, got:\n%s", want, text)
		}
	}
}

func TestHandleNodeResearchSection(t *testing.T) {
	h, st := newTestHandler(t)

	node := store.Node{
		ID:            "research-node.md",
		Kind:          "document",
		Name:          "Research Node",
		QualifiedName: "research-node.md",
		FilePath:      "research-node.md",
		StartLine:     1,
		EndLine:       10,
		BodyExcerpt:   "research node body",
		UpdatedAt:     1,
	}
	if err := st.InsertNodes([]store.Node{node}); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	if err := st.UpsertResearchMetadata("research-node.md", []store.MetadataTuple{
		{Key: "claim_id", Value: "claim-node-001", ValueType: "string", Source: "frontmatter"},
		{Key: "confidence", Value: "medium", ValueType: "string", Source: "frontmatter"},
		{Key: "analyst_status", Value: "peer-reviewed", ValueType: "string", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertResearchMetadata: %v", err)
	}

	res, err := callTool(h, h.handleNode, map[string]any{
		"document": "research-node.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	text := extractText(res)
	for _, want := range []string{"### Research Provenance", "claim-node-001", "medium", "peer-reviewed"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in node output, got:\n%s", want, text)
		}
	}
}

func TestHandleNodeRendersGitHistory(t *testing.T) {
	h, st := newTestHandler(t)

	node := store.Node{
		ID: "hist-node.md", Kind: "document", Name: "Hist Node",
		QualifiedName: "hist-node.md", FilePath: "hist-node.md",
		StartLine: 1, EndLine: 10, BodyExcerpt: "body", UpdatedAt: 1,
	}
	if err := st.InsertNodes([]store.Node{node}); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	if err := st.UpsertFileHistory(store.FileHistory{
		Path: "hist-node.md", CommitCount: 3, AuthorCount: 2,
		LastAuthor: "Ada", LastSubject: "tidy up",
		FirstCommitAt: 1700000000, LastCommitAt: 1710000000,
	}); err != nil {
		t.Fatalf("UpsertFileHistory: %v", err)
	}

	res, err := callTool(h, h.handleNode, map[string]any{"document": "hist-node.md"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	text := extractText(res)
	for _, want := range []string{
		"### History",
		"**Amended:** 3 times by 2 authors",
		"**Last author:** Ada",
		"**First changed:** 2023-11-14",
		"**Last changed:** 2024-03-09",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in node history output, got:\n%s", want, text)
		}
	}
}

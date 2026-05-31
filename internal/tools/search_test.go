package tools

import (
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

// TestHandleSearch_DistinctFileFooter verifies that when multiple result rows
// (document + heading nodes) originate from the same source file, the search
// output appends a footer indicating the number of distinct files vs total rows.
func TestHandleSearch_DistinctFileFooter(t *testing.T) {
	h, st := newTestHandler(t)

	// Insert one document node and two heading nodes — all from the same file.
	nodes := []store.Node{
		{
			ID:            "readme.md",
			Kind:          "document",
			Name:          "README",
			QualifiedName: "readme.md",
			FilePath:      "readme.md",
			StartLine:     1,
			EndLine:       50,
			BodyExcerpt:   "overview content with keyword",
			UpdatedAt:     1,
		},
		{
			ID:            "readme.md#installation:10",
			Kind:          "heading",
			Name:          "Installation",
			QualifiedName: "readme.md#installation:10",
			FilePath:      "readme.md",
			StartLine:     10,
			EndLine:       20,
			BodyExcerpt:   "installation steps with keyword",
			UpdatedAt:     1,
		},
		{
			ID:            "readme.md#usage:21",
			Kind:          "heading",
			Name:          "Usage",
			QualifiedName: "readme.md#usage:21",
			FilePath:      "readme.md",
			StartLine:     21,
			EndLine:       35,
			BodyExcerpt:   "usage examples with keyword",
			UpdatedAt:     1,
		},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}

	res, err := callTool(h, h.handleSearch, map[string]any{
		"query": "keyword",
		"limit": float64(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	text := extractText(res)

	// Must have at least 2 results (document + at least one heading).
	if !strings.Contains(text, "Found ") {
		t.Fatalf("result missing 'Found' count line:\n%s", text)
	}

	// Footer must be present and mention distinct file count.
	// With 3 rows all from readme.md the footer should say 1 distinct file.
	if !strings.Contains(text, "distinct file") {
		t.Errorf("expected distinct-file footer in search output, got:\n%s", text)
	}

	// The footer must not appear when there are 0 results (tested below).
	// Verify the footer reflects "1 distinct file" when all rows come from readme.md.
	if !strings.Contains(text, "1 distinct file") {
		t.Errorf("expected '1 distinct file' (all rows from readme.md), got:\n%s", text)
	}

	// Ordering invariant: document row must appear before heading rows.
	docIdx := strings.Index(text, "[document]")
	headingIdx := strings.Index(text, "[heading]")
	if docIdx < 0 {
		t.Errorf("no [document] row in output:\n%s", text)
	}
	if headingIdx < 0 {
		t.Errorf("no [heading] row in output:\n%s", text)
	}
	if docIdx >= 0 && headingIdx >= 0 && docIdx > headingIdx {
		t.Errorf("document row should appear before heading rows; docIdx=%d headingIdx=%d\noutput:\n%s",
			docIdx, headingIdx, text)
	}
}

// TestHandleSearch_DistinctFileFooter_MultiFile verifies that when results come
// from two different files the footer reports 2 distinct files.
func TestHandleSearch_DistinctFileFooter_MultiFile(t *testing.T) {
	h, st := newTestHandler(t)

	nodes := []store.Node{
		{
			ID:            "alpha.md",
			Kind:          "document",
			Name:          "Alpha",
			QualifiedName: "alpha.md",
			FilePath:      "alpha.md",
			StartLine:     1,
			EndLine:       10,
			BodyExcerpt:   "shared keyword",
			UpdatedAt:     1,
		},
		{
			ID:            "beta.md",
			Kind:          "document",
			Name:          "Beta",
			QualifiedName: "beta.md",
			FilePath:      "beta.md",
			StartLine:     1,
			EndLine:       10,
			BodyExcerpt:   "shared keyword",
			UpdatedAt:     1,
		},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}

	res, err := callTool(h, h.handleSearch, map[string]any{
		"query": "keyword",
		"limit": float64(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	text := extractText(res)

	if !strings.Contains(text, "2 distinct file") {
		t.Errorf("expected '2 distinct file(s)' for two-file result, got:\n%s", text)
	}
}

// TestHandleSearch_NoFooterOnZeroResults verifies that the footer is suppressed
// when the search returns 0 results.
func TestHandleSearch_NoFooterOnZeroResults(t *testing.T) {
	h, _ := newTestHandler(t)

	res, err := callTool(h, h.handleSearch, map[string]any{
		"query": "nonexistent_unique_term_xyz",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	text := extractText(res)

	if strings.Contains(text, "distinct file") {
		t.Errorf("footer must not appear on zero-result search, got:\n%s", text)
	}
}

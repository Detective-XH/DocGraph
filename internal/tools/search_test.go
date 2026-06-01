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

// TestHandleSearch_DistinctFileFooter_Truncated verifies that when the result
// set is capped at the limit (len(results) >= limit), the footer contains the
// page-scope caveat explaining that the distinct-file count is page-bounded and
// not a corpus-wide total. Uses limit=2 with ≥3 matching rows so the cap fires.
func TestHandleSearch_DistinctFileFooter_Truncated(t *testing.T) {
	h, st := newTestHandler(t)

	// Insert three document nodes that all match "trunckeyword".
	// With limit=2 the search will return exactly 2 rows (capped), triggering
	// the page-scope branch. Each node lives in a separate file so distinctFiles
	// would grow if the limit were raised (mirrors the probe v5 scenario).
	nodes := []store.Node{
		{
			ID:            "file-a.md",
			Kind:          "document",
			Name:          "File A",
			QualifiedName: "file-a.md",
			FilePath:      "file-a.md",
			StartLine:     1,
			EndLine:       10,
			BodyExcerpt:   "content with trunckeyword",
			UpdatedAt:     1,
		},
		{
			ID:            "file-b.md",
			Kind:          "document",
			Name:          "File B",
			QualifiedName: "file-b.md",
			FilePath:      "file-b.md",
			StartLine:     1,
			EndLine:       10,
			BodyExcerpt:   "content with trunckeyword",
			UpdatedAt:     1,
		},
		{
			ID:            "file-c.md",
			Kind:          "document",
			Name:          "File C",
			QualifiedName: "file-c.md",
			FilePath:      "file-c.md",
			StartLine:     1,
			EndLine:       10,
			BodyExcerpt:   "content with trunckeyword",
			UpdatedAt:     1,
		},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}

	// limit=2 forces a cap when 3 rows match — len(results)==limit fires the branch.
	res, err := callTool(h, h.handleSearch, map[string]any{
		"query": "trunckeyword",
		"limit": float64(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	text := extractText(res)

	// A16 invariant: both branches must retain this exact substring.
	if !strings.Contains(text, "distinct file(s) — count distinct files, not rows") {
		t.Errorf("A16 substring missing from truncated footer:\n%s", text)
	}

	// Page-scope note must be present when capped.
	if !strings.Contains(text, "page count, not a corpus-wide distinct-document total") {
		t.Errorf("expected page-scope caveat in truncated footer, got:\n%s", text)
	}
	if !strings.Contains(text, "capped at limit=") {
		t.Errorf("expected 'capped at limit=' in truncated footer, got:\n%s", text)
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

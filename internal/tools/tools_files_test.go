package tools

import (
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/Detective-XH/docgraph/internal/workspace"
)

// seedFilesStore opens a temporary store and upserts the given file paths so
// GetFiles and GetTopLevelDirs have real rows to return.
func seedFilesStore(t *testing.T, paths ...string) *store.Store {
	t.Helper()
	h, st := newTestHandler(t)
	_ = h
	for _, p := range paths {
		if err := st.UpsertFile(store.FileInfo{Path: p, Size: 100, NodeCount: 1}); err != nil {
			t.Fatalf("UpsertFile(%s): %v", p, err)
		}
	}
	return st
}

// TestHandleFilesZeroResultWithPathFilter verifies that when a path filter
// matches nothing, handleFiles returns the explanatory message instead of a
// bare "0 total" table, and that the known-dirs line lists real top-level dirs.
func TestHandleFilesZeroResultWithPathFilter(t *testing.T) {
	// Seed a store with files in "internal/" and "docs/" subtrees.
	st := seedFilesStore(t,
		"internal/store/store.go",
		"internal/tools/tools_files.go",
		"docs/README.md",
	)
	h := &handler{store: st, projectRoot: t.TempDir()}

	res, err := callTool(h, h.handleFiles, map[string]any{"path": "nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", extractText(res))
	}
	text := extractText(res)

	// Must contain the explanatory no-match message.
	if !strings.Contains(text, `No indexed files found under path "nonexistent"`) {
		t.Errorf("expected no-match message, got:\n%s", text)
	}

	// Must list the known top-level directories.
	if !strings.Contains(text, "Known top-level indexed directories:") {
		t.Errorf("expected known-dirs line, got:\n%s", text)
	}

	// "internal" must appear (we know it is indexed).
	if !strings.Contains(text, "internal") {
		t.Errorf("expected 'internal' in known-dirs, got:\n%s", text)
	}

	// "docs" must appear (we know it is indexed).
	if !strings.Contains(text, "docs") {
		t.Errorf("expected 'docs' in known-dirs, got:\n%s", text)
	}

	// Must NOT contain the empty-table header.
	if strings.Contains(text, "## Indexed Files") {
		t.Errorf("must not contain populated-table header in zero-result path, got:\n%s", text)
	}
}

// TestHandleFilesZeroResultNoPathFilter checks that when no filter is given and
// the index is empty, handleFiles returns the generic "not yet indexed" message.
func TestHandleFilesZeroResultNoPathFilter(t *testing.T) {
	h, _ := newTestHandler(t)

	res, err := callTool(h, h.handleFiles, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", extractText(res))
	}
	text := extractText(res)

	if !strings.Contains(text, "No files have been indexed yet.") {
		t.Errorf("expected empty-index message, got:\n%s", text)
	}
}

// TestHandleFilesZeroResultEmptyIndex verifies the fallback "The index appears
// to be empty." text when a path filter is given but the whole store is empty.
func TestHandleFilesZeroResultEmptyIndex(t *testing.T) {
	h, _ := newTestHandler(t)

	res, err := callTool(h, h.handleFiles, map[string]any{"path": "docs"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", extractText(res))
	}
	text := extractText(res)

	if !strings.Contains(text, `No indexed files found under path "docs"`) {
		t.Errorf("expected no-match message, got:\n%s", text)
	}
	if !strings.Contains(text, "The index appears to be empty.") {
		t.Errorf("expected empty-index fallback, got:\n%s", text)
	}
}

// TestHandleFilesPopulatedPathUnchanged verifies that when the path filter
// matches real files, the normal populated-table format is returned intact.
func TestHandleFilesPopulatedPathUnchanged(t *testing.T) {
	st := seedFilesStore(t,
		"internal/store/store.go",
		"internal/tools/tools_files.go",
		"docs/README.md",
	)
	h := &handler{store: st, projectRoot: t.TempDir()}

	res, err := callTool(h, h.handleFiles, map[string]any{"path": "internal"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", extractText(res))
	}
	text := extractText(res)

	if !strings.Contains(text, "## Indexed Files") {
		t.Errorf("expected populated-table header, got:\n%s", text)
	}
	if !strings.Contains(text, "internal/store/store.go") {
		t.Errorf("expected internal/store/store.go in output, got:\n%s", text)
	}
	// docs path must not appear (filtered out).
	if strings.Contains(text, "docs/README.md") {
		t.Errorf("docs/README.md must not appear under internal filter, got:\n%s", text)
	}
}

// TestHandleFilesNoFilterPopulatedUnchanged verifies that listing all files
// (no path filter) with a populated index returns the normal table.
func TestHandleFilesNoFilterPopulatedUnchanged(t *testing.T) {
	st := seedFilesStore(t, "internal/store/store.go", "docs/README.md")
	h := &handler{store: st, projectRoot: t.TempDir()}

	res, err := callTool(h, h.handleFiles, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", extractText(res))
	}
	text := extractText(res)

	if !strings.Contains(text, "## Indexed Files (2 total)") {
		t.Errorf("expected '## Indexed Files (2 total)', got:\n%s", text)
	}
}

// TestHandleFilesWorkspaceZeroResultWithPathFilter checks the workspace branch:
// when a path filter matches nothing, the known-dirs from all projects are merged.
func TestHandleFilesWorkspaceZeroResultWithPathFilter(t *testing.T) {
	storeA := seedFilesStore(t, "internal/a.md")
	storeB := seedFilesStore(t, "docs/b.md")

	h := &handler{workspace: &workspace.Workspace{Projects: []*workspace.Project{
		{Name: "proj-a", Path: t.TempDir(), Store: storeA},
		{Name: "proj-b", Path: t.TempDir(), Store: storeB},
	}}}

	res, err := callTool(h, h.handleFiles, map[string]any{"path": "nowhere"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", extractText(res))
	}
	text := extractText(res)

	if !strings.Contains(text, `No indexed files found under path "nowhere"`) {
		t.Errorf("expected no-match message, got:\n%s", text)
	}
	if !strings.Contains(text, "internal") {
		t.Errorf("expected 'internal' in workspace known-dirs, got:\n%s", text)
	}
	if !strings.Contains(text, "docs") {
		t.Errorf("expected 'docs' in workspace known-dirs, got:\n%s", text)
	}
}

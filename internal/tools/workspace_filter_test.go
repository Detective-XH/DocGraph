package tools

import (
	"path/filepath"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/Detective-XH/docgraph/internal/workspace"
)

// openFilterStore opens a temporary store and inserts a single document node
// whose BodyExcerpt contains the given keyword. Returns the open *store.Store;
// the caller must call st.Close() when done.
func openFilterStore(t *testing.T, nodeID, keyword string) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "filter.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	node := store.Node{
		ID:            nodeID,
		Kind:          "document",
		Name:          nodeID,
		QualifiedName: nodeID,
		FilePath:      nodeID,
		StartLine:     1,
		EndLine:       10,
		BodyExcerpt:   "content with " + keyword,
		UpdatedAt:     1,
	}
	if err := st.InsertNodes([]store.Node{node}); err != nil {
		st.Close()
		t.Fatalf("InsertNodes: %v", err)
	}
	return st
}

// TestWorkspaceProjectFilter_SearchScoped verifies that SearchWithOptions with
// ProjectFilter set only returns documents from the named project and not from
// the other project.
func TestWorkspaceProjectFilter_SearchScoped(t *testing.T) {
	stA := openFilterStore(t, "alpha.md", "filtertoken")
	defer stA.Close()
	stB := openFilterStore(t, "beta.md", "filtertoken")
	defer stB.Close()

	w := &workspace.Workspace{Projects: []*workspace.Project{
		{Name: "project-alpha", Path: t.TempDir(), Store: stA},
		{Name: "project-beta", Path: t.TempDir(), Store: stB},
	}}

	results, err := w.SearchWithOptions(store.SearchOptions{
		Query:         "filtertoken",
		Limit:         10,
		ProjectFilter: "project-alpha",
	})
	if err != nil {
		t.Fatalf("SearchWithOptions: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result for project-alpha, got none")
	}
	for _, r := range results {
		if r.Node.ProjectName != "project-alpha" {
			t.Errorf("result %q has ProjectName=%q, want project-alpha", r.Node.ID, r.Node.ProjectName)
		}
		if r.Node.ID == "beta.md" {
			t.Errorf("result from project-beta must not appear when filter=project-alpha")
		}
	}
}

// TestWorkspaceProjectFilter_UnknownProject verifies that filtering by a project
// name that doesn't exist returns an empty slice without error.
func TestWorkspaceProjectFilter_UnknownProject(t *testing.T) {
	stA := openFilterStore(t, "alpha.md", "filtertoken")
	defer stA.Close()

	w := &workspace.Workspace{Projects: []*workspace.Project{
		{Name: "project-alpha", Path: t.TempDir(), Store: stA},
	}}

	results, err := w.SearchWithOptions(store.SearchOptions{
		Query:         "filtertoken",
		Limit:         10,
		ProjectFilter: "nonexistent",
	})
	if err != nil {
		t.Fatalf("SearchWithOptions with unknown project returned error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for unknown project, got %d", len(results))
	}
}

// TestWorkspaceProjectFilter_NoFilter verifies that omitting ProjectFilter returns
// documents from all projects (existing fan-out behavior preserved).
func TestWorkspaceProjectFilter_NoFilter(t *testing.T) {
	stA := openFilterStore(t, "alpha.md", "filtertoken")
	defer stA.Close()
	stB := openFilterStore(t, "beta.md", "filtertoken")
	defer stB.Close()

	w := &workspace.Workspace{Projects: []*workspace.Project{
		{Name: "project-alpha", Path: t.TempDir(), Store: stA},
		{Name: "project-beta", Path: t.TempDir(), Store: stB},
	}}

	results, err := w.SearchWithOptions(store.SearchOptions{
		Query: "filtertoken",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchWithOptions: %v", err)
	}

	seen := make(map[string]bool)
	for _, r := range results {
		seen[r.Node.ProjectName] = true
	}
	if !seen["project-alpha"] {
		t.Error("project-alpha results missing when no ProjectFilter set")
	}
	if !seen["project-beta"] {
		t.Error("project-beta results missing when no ProjectFilter set")
	}
}

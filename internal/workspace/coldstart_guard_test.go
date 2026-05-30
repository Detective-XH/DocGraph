package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

// TestColdStartDeleteSkipPreservesCorrectness locks the §A optimization in
// indexProjectOpts: the per-file stale-row deletes are skipped ONLY when the
// project's base table is empty (cold-start, where they match 0 rows), and still
// RUN on an incremental re-index of a changed file over a POPULATED DB (where
// they are load-bearing — InsertEdges/InsertUnresolvedRefs are plain INSERTs).
//
// It cold-starts a project (guard skips the no-op deletes), changes a.md, then
// re-indexes (now populated → deletes must run), and asserts the result is
// row-equivalent to a clean rebuild of the final content. A regression that
// skips the deletes when the DB is populated (e.g. an always-skip guard) leaves
// stale nodes / duplicate edges / dangling unresolved refs and fails here.
// Similarity is suppressed (threshold 0.99) so similar_to edges don't confound
// the row-count equivalence — the test isolates the delete-guard behavior.
func TestColdStartDeleteSkipPreservesCorrectness(t *testing.T) {
	const v1 = "---\nstatus: draft\n---\n# A\n\n## Beta\n\nSee [x](other.md).\n"
	const v2 = "---\nstatus: final\n---\n# A\n\n## Gamma\n\nNo link here.\n"
	const stable = "# B\n\nunchanged body\n"

	// Recovery workspace: cold-start (deletes skipped on empty DB), then change
	// a.md and re-index (deletes run on the now-populated DB).
	recRoot := t.TempDir()
	recProj := filepath.Join(recRoot, "proj")
	writeWSFile(t, filepath.Join(recProj, "a.md"), v1)
	writeWSFile(t, filepath.Join(recProj, "b.md"), stable)
	indexWorkspaceProj(t, recRoot) // cold-start (guard skips no-op deletes)
	writeWSFile(t, filepath.Join(recProj, "a.md"), v2)
	got := indexWorkspaceProj(t, recRoot) // incremental over populated DB → deletes run

	// Reference: clean cold-start of the final content.
	refRoot := t.TempDir()
	refProj := filepath.Join(refRoot, "proj")
	writeWSFile(t, filepath.Join(refProj, "a.md"), v2)
	writeWSFile(t, filepath.Join(refProj, "b.md"), stable)
	want := indexWorkspaceProj(t, refRoot)

	// Non-vacuous sanity: both files indexed; v2 has no outbound link so a correct
	// build leaves zero unresolved refs (v1 had one — it must be deleted).
	if want.FileCount != 2 {
		t.Fatalf("fixture invariant: clean build should index 2 files, got %d", want.FileCount)
	}
	if want.UnresolvedCount != 0 {
		t.Fatalf("fixture invariant: clean v2 build should have 0 unresolved refs, got %d", want.UnresolvedCount)
	}
	if got.NodeCount != want.NodeCount {
		t.Errorf("NodeCount: incremental-after-change=%d, clean rebuild=%d (stale nodes survived)", got.NodeCount, want.NodeCount)
	}
	if got.EdgeCount != want.EdgeCount {
		t.Errorf("EdgeCount: incremental-after-change=%d, clean rebuild=%d (duplicate edges from plain InsertEdges)", got.EdgeCount, want.EdgeCount)
	}
	if got.UnresolvedCount != want.UnresolvedCount {
		t.Errorf("UnresolvedCount: incremental-after-change=%d, clean rebuild=%d (stale unresolved refs survived)", got.UnresolvedCount, want.UnresolvedCount)
	}
	if got.FileCount != want.FileCount {
		t.Errorf("FileCount: incremental-after-change=%d, clean rebuild=%d", got.FileCount, want.FileCount)
	}
}

// indexWorkspaceProj opens the workspace at root, runs a full IndexAll, and
// returns the "proj" project's stats (after closing the stores). Reopening the
// same root reuses the persisted per-project DB, so a second call indexes
// incrementally over the populated DB.
func indexWorkspaceProj(t *testing.T, root string) store.Stats {
	t.Helper()
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	w.NoGitignore = true
	w.SimilarityThreshold = 0.99 // suppress similar_to edges so they don't confound counts
	if err := w.IndexAll(); err != nil {
		t.Fatal(err)
	}
	p := w.FindProject("proj")
	if p == nil {
		t.Fatal("project 'proj' not found")
	}
	st, err := p.Store.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return st
}

func writeWSFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

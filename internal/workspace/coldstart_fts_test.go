package workspace

import (
	"path/filepath"
	"testing"
)

// TestColdStartWorkspaceFTSRebuildAndSync locks the workspace cold-start FTS
// bulk-rebuild path in indexProjectOpts. GetStats (used by the §A guard test)
// returns only base-table counts, so a botched FTS rebuild leaves those counts
// correct and passes a count-only test — this test must therefore assert on FTS
// SEARCH (the MATCH path), which is exactly what the rebuild populates.
//
// Both reindex passes run on the SAME open Workspace (no reopen). That mirrors
// the live `serve --workspace` watcher (open stores once → cold-start IndexAll →
// ReindexProject on the same store) and is the ONLY path where recreating the
// triggers is load-bearing: store.Open's bootstrapSchema re-creates the dropped
// triggers (CREATE TRIGGER IF NOT EXISTS), so a reopen would mask a skipped
// recreate. Holding the store open exposes it.
//
//   - Cold-start IndexAll over a doc carrying a unique term → search finds it.
//     Proves the 'rebuild' (run with triggers dropped during the load) populated
//     the FTS. Mutation: disable the two Rebuild*FTS calls → cold-start FTS is
//     empty → this search returns nothing → FAIL.
//   - Rewrite the doc (drop the old term, add a new one) and reindex on the same
//     open workspace. The DB is now populated (fullBuild=false) so the recreated
//     triggers must sync the change incrementally → new term hits, old term
//     misses. Mutation: skip CreateSectionFTSTriggers/CreateNodesFTSTriggers after
//     the rebuild → the triggers stay dropped on the open store → the incremental
//     UPDATE never reaches the FTS → new term misses / old term lingers → FAIL.
func TestColdStartWorkspaceFTSRebuildAndSync(t *testing.T) {
	const oldTerm = "alphacoldstartterm"
	const newTerm = "betawarmsyncterm"
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	docPath := filepath.Join(proj, "doc.md")
	writeWSFile(t, docPath, "---\nstatus: draft\n---\n# Doc\n\nThe body mentions "+oldTerm+" prominently.\n")
	writeWSFile(t, filepath.Join(proj, "stable.md"), "# Stable\n\nunchanging filler body\n")

	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.NoGitignore = true
	w.SimilarityThreshold = 0.99

	// Cold-start: FTS empty → drop triggers, bulk-load, rebuild.
	if err := w.IndexAll(); err != nil {
		t.Fatal(err)
	}
	if hits := wsSearchCount(t, w, oldTerm); hits == 0 {
		t.Errorf("cold-start: search %q = 0 hits, want >0 (FTS rebuild did not populate the index)", oldTerm)
	}

	// Rewrite the searchable term, then reindex on the SAME open workspace
	// (fullBuild=false → the recreated live triggers must keep the FTS current).
	writeWSFile(t, docPath, "---\nstatus: draft\n---\n# Doc\n\nThe body now mentions "+newTerm+" instead.\n")
	if err := w.IndexAll(); err != nil {
		t.Fatal(err)
	}
	if hits := wsSearchCount(t, w, newTerm); hits == 0 {
		t.Errorf("warm reindex: search %q = 0 hits, want >0 (triggers not recreated → incremental change missed FTS)", newTerm)
	}
	if hits := wsSearchCount(t, w, oldTerm); hits != 0 {
		t.Errorf("warm reindex: search %q = %d hits, want 0 (stale FTS posting survived the update)", oldTerm, hits)
	}
}

// TestColdStartWorkspaceFTSCrashRecovery exercises the crash-recovery state
// unique to the combined §A delete-skip + this FTS gate: base tables POPULATED
// but FTS EMPTY (a crash between bulk-load and rebuild). There baseEmpty=false
// (the per-file deletes are load-bearing and must RUN for the changed file) AND
// fullBuild=true (the FTS must be rebuilt from the settled base). It seeds that
// state with DeleteAll*FTS over a populated DB, changes a file, reindexes, and
// asserts (a) the FTS reflects the change (new term hits, removed term misses)
// and (b) base-table counts match a clean rebuild (the deletes ran → no
// duplicate edges / stale unresolved refs).
func TestColdStartWorkspaceFTSCrashRecovery(t *testing.T) {
	const v1Term = "crashbeforeterm"
	const v2Term = "crashafterterm"
	v1 := "---\nstatus: draft\n---\n# A\n\nVersion one says " + v1Term + ". See [x](other.md).\n"
	v2 := "---\nstatus: final\n---\n# A\n\nVersion two says " + v2Term + ". No link.\n"
	const stable = "# B\n\nunchanged filler body\n"

	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	writeWSFile(t, filepath.Join(proj, "a.md"), v1)
	writeWSFile(t, filepath.Join(proj, "b.md"), stable)

	// Cold-start populates base + FTS, then simulate the crash: empty both FTS
	// indexes while base rows stay intact (base populated + FTS empty).
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	w.NoGitignore = true
	w.SimilarityThreshold = 0.99 // suppress similar_to edges so counts compare cleanly
	if err := w.IndexAll(); err != nil {
		t.Fatal(err)
	}
	p := w.FindProject("proj")
	if p == nil {
		t.Fatal("project 'proj' not found")
	}
	if err := p.Store.DeleteAllSectionFTS(); err != nil {
		t.Fatal(err)
	}
	if err := p.Store.DeleteAllNodesFTS(); err != nil {
		t.Fatal(err)
	}
	if empty, _ := p.Store.NodesFTSIsEmpty(); !empty {
		t.Fatal("setup: nodes_fts not empty after DeleteAllNodesFTS")
	}
	if empty, _ := p.Store.NodesIsEmpty(); empty {
		t.Fatal("setup: base nodes unexpectedly empty — crash state needs base populated")
	}

	// Change a.md, then reindex on the SAME open stores: fullBuild=true (FTS empty)
	// + baseEmpty=false (nodes populated) → deletes run AND FTS rebuilds.
	writeWSFile(t, filepath.Join(proj, "a.md"), v2)
	if err := w.IndexAll(); err != nil {
		t.Fatal(err)
	}
	got, err := p.Store.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	// FTS must reflect the change after the rebuild — search the same open store.
	if hits := wsSearchCount(t, w, v2Term); hits == 0 {
		t.Errorf("crash-recovery: search %q = 0 hits, want >0 (rebuild did not pick up the changed file)", v2Term)
	}
	if hits := wsSearchCount(t, w, v1Term); hits != 0 {
		t.Errorf("crash-recovery: search %q = %d hits, want 0 (deletes skipped → stale base row rebuilt into FTS)", v1Term, hits)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Reference: clean cold-start of the final content. Base counts must match —
	// proves the load-bearing deletes ran (no duplicate edges / stale refs).
	refRoot := t.TempDir()
	refProj := filepath.Join(refRoot, "proj")
	writeWSFile(t, filepath.Join(refProj, "a.md"), v2)
	writeWSFile(t, filepath.Join(refProj, "b.md"), stable)
	want := indexWorkspaceProj(t, refRoot)

	if want.UnresolvedCount != 0 {
		t.Fatalf("fixture invariant: clean v2 build should have 0 unresolved refs, got %d", want.UnresolvedCount)
	}
	if got.NodeCount != want.NodeCount {
		t.Errorf("NodeCount: crash-recovery=%d, clean rebuild=%d (stale nodes survived)", got.NodeCount, want.NodeCount)
	}
	if got.EdgeCount != want.EdgeCount {
		t.Errorf("EdgeCount: crash-recovery=%d, clean rebuild=%d (duplicate edges from plain InsertEdges)", got.EdgeCount, want.EdgeCount)
	}
	if got.UnresolvedCount != want.UnresolvedCount {
		t.Errorf("UnresolvedCount: crash-recovery=%d, clean rebuild=%d (stale unresolved refs survived)", got.UnresolvedCount, want.UnresolvedCount)
	}
}

// wsSearchCount runs a workspace FTS search against an ALREADY open + indexed
// Workspace and returns the hit count. It must NOT reopen: store.Open's
// bootstrapSchema re-creates dropped FTS triggers, which would mask a skipped
// CreateFTSTriggers recreate. Searching the live store is also what the running
// MCP server does.
func wsSearchCount(t *testing.T, w *Workspace, query string) int {
	t.Helper()
	results, err := w.Search(query, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	return len(results)
}

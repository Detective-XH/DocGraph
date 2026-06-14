package workspace

import (
	"path/filepath"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
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
	assertFTSHits(t, w, oldTerm, true, "cold-start: FTS rebuild did not populate the index")

	// Rewrite the searchable term, then reindex on the SAME open workspace
	// (fullBuild=false → the recreated live triggers must keep the FTS current).
	writeWSFile(t, docPath, "---\nstatus: draft\n---\n# Doc\n\nThe body now mentions "+newTerm+" instead.\n")
	if err := w.IndexAll(); err != nil {
		t.Fatal(err)
	}
	assertFTSHits(t, w, newTerm, true, "warm reindex: triggers not recreated → incremental change missed FTS")
	assertFTSHits(t, w, oldTerm, false, "warm reindex: stale FTS posting survived the update")
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
	if err := p.Store.Fts.DeleteAllSectionFTS(); err != nil {
		t.Fatal(err)
	}
	if err := p.Store.Fts.DeleteAllNodesFTS(); err != nil {
		t.Fatal(err)
	}
	if empty, _ := p.Store.Fts.NodesFTSIsEmpty(); !empty {
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
	assertFTSHits(t, w, v2Term, true, "crash-recovery: rebuild did not pick up the changed file")
	assertFTSHits(t, w, v1Term, false, "crash-recovery: deletes skipped → stale base row rebuilt into FTS")
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
	assertStatsMatch(t, got, want, "crash-recovery")
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

// assertStatsMatch asserts that got.NodeCount, EdgeCount, and UnresolvedCount all
// equal the corresponding fields in want. label is included in every failure message.
func assertStatsMatch(t *testing.T, got, want store.Stats, label string) {
	t.Helper()
	if got.NodeCount != want.NodeCount {
		t.Errorf("NodeCount: %s=%d, clean rebuild=%d (stale nodes survived)", label, got.NodeCount, want.NodeCount)
	}
	if got.EdgeCount != want.EdgeCount {
		t.Errorf("EdgeCount: %s=%d, clean rebuild=%d (duplicate edges from plain InsertEdges)", label, got.EdgeCount, want.EdgeCount)
	}
	if got.UnresolvedCount != want.UnresolvedCount {
		t.Errorf("UnresolvedCount: %s=%d, clean rebuild=%d (stale unresolved refs survived)", label, got.UnresolvedCount, want.UnresolvedCount)
	}
}

// assertFTSHits asserts the FTS hit count for term in w is non-zero (wantNonZero=true)
// or zero (wantNonZero=false). msg is included in the failure message for context.
// Non-fatal (t.Errorf) to preserve the original collect-all-failures behavior of
// the call sites, which continue past a failed FTS check to later assertions.
func assertFTSHits(t *testing.T, w *Workspace, term string, wantNonZero bool, msg string) {
	t.Helper()
	hits := wsSearchCount(t, w, term)
	if wantNonZero && hits == 0 {
		t.Errorf("%s: search %q = 0 hits, want >0", msg, term)
	}
	if !wantNonZero && hits != 0 {
		t.Errorf("%s: search %q = %d hits, want 0", msg, term, hits)
	}
}

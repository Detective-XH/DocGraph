package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

// TestIndexStore_ForceGuardPreservesCrashRecoveryDeletes locks the safety
// invariant of the --force delete-skip optimization (indexer.go): the eager
// per-file stale-row deletes are skipped ONLY when force==true (the DB was just
// wiped by removeIndexDB), NEVER merely because fullBuild==true.
//
// The hazard is crash recovery. A prior build can write all base rows and then
// lose its FTS (a non-graceful exit between the bulk load and the FTS rebuild),
// so the next run sees an empty FTS — fullBuild==true — over a POPULATED base.
// If the guard keyed off fullBuild instead of force, an incremental run in that
// state would skip the load-bearing deletes for a changed file, and the
// non-idempotent inserts (plain INSERT edges/unresolved_refs, INSERT OR IGNORE
// nodes) would leave duplicate edges, stale nodes, and dangling unresolved refs.
//
// This reproduces that exact state with the DeleteAll*FTS test seams and asserts
// the incremental result is row-equivalent to a clean rebuild of the same final
// content. A regression to an `!fullBuild` guard makes the counts diverge.
func TestIndexStore_ForceGuardPreservesCrashRecoveryDeletes(t *testing.T) {
	// v1 → v2 change BOTH a heading (Beta→Gamma: new node + contains edge) and a
	// link (other.md → none: an unresolved ref that must be removed), so a skipped
	// delete is observable in NodeCount, EdgeCount, AND UnresolvedCount.
	const v1 = "# Title\n\n## Beta\n\nSee [other](other.md).\n"
	const v2 = "# Title\n\n## Gamma\n\nNo link here.\n"

	// Crash-recovery store: full build of v1, lose the FTS, then incremental v2.
	recDir := t.TempDir()
	docPath := filepath.Join(recDir, "a.md")
	writeFileT(t, docPath, v1)

	recStore := openTempStoreT(t)
	if err := indexStore(recDir, recStore, true); err != nil {
		t.Fatalf("initial full build: %v", err)
	}
	// Simulate the crash-recovery state: base tables intact, both FTS indexes empty
	// → the next run computes fullBuild=true over a populated DB.
	if err := recStore.Fts.DeleteAllNodesFTS(); err != nil {
		t.Fatal(err)
	}
	if err := recStore.Fts.DeleteAllSectionFTS(); err != nil {
		t.Fatal(err)
	}
	if empty, _ := recStore.Fts.NodesFTSIsEmpty(); !empty {
		t.Fatal("setup precondition: nodes FTS must be empty so the next run computes fullBuild=true")
	}
	// Change the file, then run an INCREMENTAL (force=false) re-index. fullBuild is
	// true here (FTS empty) but force is false, so the per-file deletes MUST run.
	writeFileT(t, docPath, v2)
	if err := indexStore(recDir, recStore, false); err != nil {
		t.Fatalf("incremental re-index over crash-recovery state: %v", err)
	}

	// Reference store: a clean --force build of the final (v2) content.
	refStore := openTempStoreT(t)
	if err := indexStore(recDir, refStore, true); err != nil {
		t.Fatalf("reference full build: %v", err)
	}

	got, err := recStore.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	want, err := refStore.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	// Sanity: keep the test non-vacuous. v2 has no outbound links, so a correct
	// build leaves zero unresolved refs; the v1 build had one. If the deletes were
	// skipped, v1's ref would survive and the UnresolvedCount check below fires.
	if want.UnresolvedCount != 0 {
		t.Fatalf("test fixture invariant broken: clean v2 build should have 0 unresolved refs, got %d", want.UnresolvedCount)
	}
	// Row-equivalence: an incremental run that correctly ran the deletes lands on
	// exactly the clean-rebuild state. Divergence ⇒ stale rows survived ⇒ the guard
	// wrongly keyed off fullBuild (true here) instead of force (false here).
	if got.NodeCount != want.NodeCount {
		t.Errorf("NodeCount: crash-recovery incremental=%d, clean rebuild=%d (stale nodes survived)", got.NodeCount, want.NodeCount)
	}
	if got.EdgeCount != want.EdgeCount {
		t.Errorf("EdgeCount: crash-recovery incremental=%d, clean rebuild=%d (duplicate edges from plain InsertEdges)", got.EdgeCount, want.EdgeCount)
	}
	if got.UnresolvedCount != want.UnresolvedCount {
		t.Errorf("UnresolvedCount: crash-recovery incremental=%d, clean rebuild=%d (stale unresolved refs survived)", got.UnresolvedCount, want.UnresolvedCount)
	}
	if got.FileCount != want.FileCount {
		t.Errorf("FileCount: crash-recovery incremental=%d, clean rebuild=%d", got.FileCount, want.FileCount)
	}
}

func openTempStoreT(t *testing.T) *store.Store {
	t.Helper()
	dbDir := filepath.Join(t.TempDir(), ".docgraph")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dbDir, "docgraph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

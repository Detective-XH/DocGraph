package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
	"github.com/Detective-XH/docgraph/internal/workspace"
)

// openPruneTestStore opens a fresh in-memory-style store in a temp dir for
// prune tests. The caller is responsible for calling st.Close().
func openPruneTestStore(t *testing.T) (dir string, st *store.Store) {
	t.Helper()
	dir = t.TempDir()
	dbDir := filepath.Join(dir, ".docgraph")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var err error
	st, err = store.Open(filepath.Join(dbDir, "docgraph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return dir, st
}

// indexPruneTestFile writes content to relPath under dir, then calls indexStore
// to index it, so that it is reachable via Search and GetFileHash.
func indexPruneTestFile(t *testing.T, dir string, st *store.Store, relPath, content string) {
	t.Helper()
	absPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := indexStore(dir, st, false); err != nil {
		t.Fatalf("indexStore: %v", err)
	}
}

// nodeCountSQL opens the DB file directly to count nodes for a given file_path.
// This is a white-box test helper to verify FTS + cascade correctness.
func nodeCountSQL(t *testing.T, dir, relPath string) int {
	t.Helper()
	dbPath := filepath.Join(dir, ".docgraph", "docgraph.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db for node count: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM nodes WHERE file_path = ?`, relPath).Scan(&n); err != nil {
		t.Fatalf("count nodes for %s: %v", relPath, err)
	}
	return n
}

// TestPruneDeletedFiles_RemovesVanishedFile is the primary correctness test.
// It indexes a file, removes it from disk, calls pruneDeletedFiles, and asserts
// that Search finds 0 results for the unique token, GetFileHash returns "", and
// the nodes table has 0 rows for that file path.
func TestPruneDeletedFiles_RemovesVanishedFile(t *testing.T) {
	dir, st := openPruneTestStore(t)
	const relPath = "vanished.md"
	// Use a unique token >=3 chars so FTS trigram path is exercised (not the LIKE fallback).
	const uniqueToken = "xyzprunetoken"
	content := "# Vanished Doc\n\n" + uniqueToken + " is a unique marker for this document.\n"

	indexPruneTestFile(t, dir, st, relPath, content)

	// (a) Before prune: Search returns >= 1 result for the unique token.
	before, err := st.Search(uniqueToken, "", 10)
	if err != nil {
		t.Fatalf("Search before prune: %v", err)
	}
	found := false
	for _, r := range before {
		if r.Node.FilePath == relPath {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected to find %q in search results before prune; got %d results", relPath, len(before))
	}

	// Also confirm GetFileHash is non-empty before prune.
	hashBefore, err := st.GetFileHash(relPath)
	if err != nil {
		t.Fatalf("GetFileHash before prune: %v", err)
	}
	if hashBefore == "" {
		t.Fatal("expected non-empty hash before prune")
	}

	// Remove the file from disk.
	if err := os.Remove(filepath.Join(dir, relPath)); err != nil {
		t.Fatal(err)
	}

	// Call pruneDeletedFiles with the file in the changed list.
	pruned := pruneDeletedFiles(dir, st, []string{relPath})
	if pruned != 1 {
		t.Fatalf("expected pruned=1, got %d", pruned)
	}

	// (a) After prune: Search returns 0 results for the unique token from that file.
	after, err := st.Search(uniqueToken, "", 10)
	if err != nil {
		t.Fatalf("Search after prune: %v", err)
	}
	for _, r := range after {
		if r.Node.FilePath == relPath {
			t.Errorf("file %q still found in search results after prune", relPath)
		}
	}

	// (a) GetFileHash returns "" after prune.
	hashAfter, err := st.GetFileHash(relPath)
	if err != nil {
		t.Fatalf("GetFileHash after prune: %v", err)
	}
	if hashAfter != "" {
		t.Errorf("expected empty hash after prune, got %q", hashAfter)
	}

	// (a) Nodes table has 0 rows for that file_path.
	if n := nodeCountSQL(t, dir, relPath); n != 0 {
		t.Errorf("expected 0 nodes after prune, got %d", n)
	}
}

// TestPruneDeletedFiles_SkipsExistingFile asserts that a file still on disk that
// appears in the changed list is NOT pruned.
func TestPruneDeletedFiles_SkipsExistingFile(t *testing.T) {
	dir, st := openPruneTestStore(t)
	const relPath = "still_there.md"
	const uniqueToken = "xyzkeeptoken"
	content := "# Still There\n\n" + uniqueToken + " marker stays on disk.\n"

	indexPruneTestFile(t, dir, st, relPath, content)

	// File is still on disk — prune should leave it alone.
	pruned := pruneDeletedFiles(dir, st, []string{relPath})
	if pruned != 0 {
		t.Fatalf("expected pruned=0 for on-disk file, got %d", pruned)
	}

	// (b) Search still finds the file.
	results, err := st.Search(uniqueToken, "", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	found := false
	for _, r := range results {
		if r.Node.FilePath == relPath {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected to find %q in search results after no-op prune", relPath)
	}
}

// TestPruneDeletedFiles_NeverIndexed asserts that passing a path that was never
// indexed (not in the DB) is a no-op with count 0 and no error.
func TestPruneDeletedFiles_NeverIndexed(t *testing.T) {
	dir, st := openPruneTestStore(t)
	const relPath = "ghost.md"
	// Do not write or index the file — it is not on disk and not in the DB.

	pruned := pruneDeletedFiles(dir, st, []string{relPath})
	if pruned != 0 {
		t.Fatalf("expected pruned=0 for never-indexed path, got %d", pruned)
	}
}

// TestReconcileDeletedFiles_GapCloser (T1) is the discriminating test: it closes
// the downtime-deletion gap that the event-driven pruneDeletedFiles cannot see.
// Index X.md + Y.md, assert X's node EXISTS first (proves the test would catch a
// no-op), delete X.md from disk, reconcile, then assert X is GONE and Y survives.
// Two files < pruneBlastRadiusMinFiles so the ratio gate is skipped and X is pruned.
func TestReconcileDeletedFiles_GapCloser(t *testing.T) {
	dir, st := openPruneTestStore(t)
	const (
		xPath = "vanished.md"
		yPath = "present.md"
	)
	indexPruneTestFile(t, dir, st, yPath, "# Present\n\nstays on disk.\n")
	indexPruneTestFile(t, dir, st, xPath, "# Vanished\n\ngap-closer marker.\n")

	// Pre-condition: X's node EXISTS before reconcile — proves the test discriminates.
	if n := nodeCountSQL(t, dir, xPath); n == 0 {
		t.Fatalf("pre-condition failed: expected X (%s) to have >0 nodes before reconcile", xPath)
	}

	// Delete X.md from disk while no watcher saw it (the downtime-deletion gap).
	if err := os.Remove(filepath.Join(dir, xPath)); err != nil {
		t.Fatal(err)
	}

	pruned := reconcileDeletedFiles(dir, st)
	if pruned != 1 {
		t.Fatalf("expected reconcile to prune 1 file, got %d", pruned)
	}

	// X is GONE, Y survives.
	if n := nodeCountSQL(t, dir, xPath); n != 0 {
		t.Errorf("expected 0 nodes for deleted X (%s) after reconcile, got %d", xPath, n)
	}
	if n := nodeCountSQL(t, dir, yPath); n == 0 {
		t.Errorf("expected present file Y (%s) to survive reconcile, got 0 nodes", yPath)
	}
}

// TestReconcileDeletedFiles_BlastRadius (T2) pins the >50% blast-radius refusal.
// Index >=pruneBlastRadiusMinFiles files, delete >50% of them from disk, and assert
// reconcile returns 0 (refused) and every node is INTACT. Guard 2 fires on > 0.5,
// so with 10 files we delete 6 (0.6 > 0.5) to land strictly above the threshold.
func TestReconcileDeletedFiles_BlastRadius(t *testing.T) {
	dir, st := openPruneTestStore(t)
	const total = pruneBlastRadiusMinFiles // 10 — at/above the floor so the ratio gate is live
	const toDelete = 6                     // 6/10 = 0.6 > 0.5 → refused
	paths := make([]string, total)
	for i := range total {
		paths[i] = "doc" + string(rune('0'+i)) + ".md"
		indexPruneTestFile(t, dir, st, paths[i], "# Doc\n\nblast-radius marker.\n")
	}
	for i := range toDelete {
		if err := os.Remove(filepath.Join(dir, paths[i])); err != nil {
			t.Fatal(err)
		}
	}

	pruned := reconcileDeletedFiles(dir, st)
	if pruned != 0 {
		t.Fatalf("expected reconcile to REFUSE (return 0) above blast radius, got %d", pruned)
	}

	// Every node is intact — including the deleted-on-disk ones (refusal = no prune).
	for _, p := range paths {
		if n := nodeCountSQL(t, dir, p); n == 0 {
			t.Errorf("expected %s nodes intact after refused reconcile, got 0", p)
		}
	}
}

// TestReconcileDeletedFiles_TreeGone (T3) verifies Guard 1: when the project root is
// not accessible, reconcile is a no-op (returns 0, no panic, no prune). To make the
// test DISCRIMINATING, the DB has 1 indexed file (< pruneBlastRadiusMinFiles, so Guard
// 2 can't also catch it) — with Guard 1 removed, 1 file < 10 → ratio gate skipped →
// the file WOULD be pruned. The surviving node proves Guard 1 is what stopped it.
func TestReconcileDeletedFiles_TreeGone(t *testing.T) {
	dir, st := openPruneTestStore(t)
	const relPath = "lonely.md"
	indexPruneTestFile(t, dir, st, relPath, "# Lonely\n\ntree-gone marker.\n")
	if n := nodeCountSQL(t, dir, relPath); n == 0 {
		t.Fatalf("pre-condition failed: expected %s to have >0 nodes", relPath)
	}

	bogusRoot := filepath.Join(dir, "does", "not", "exist")
	pruned := reconcileDeletedFiles(bogusRoot, st)
	if pruned != 0 {
		t.Fatalf("expected reconcile to skip (return 0) for inaccessible root, got %d", pruned)
	}

	// The indexed file SURVIVES — Guard 1 short-circuited before any per-file Stat.
	if n := nodeCountSQL(t, dir, relPath); n == 0 {
		t.Errorf("expected %s to survive reconcile against a missing tree, got 0 nodes", relPath)
	}
}

// TestReconcileDeletedFiles_PresentFileKept (T4) proves the per-file Stat keeps files
// that are present on disk: an unmodified, still-present file is never pruned.
func TestReconcileDeletedFiles_PresentFileKept(t *testing.T) {
	dir, st := openPruneTestStore(t)
	const relPath = "kept.md"
	indexPruneTestFile(t, dir, st, relPath, "# Kept\n\npresent-file marker.\n")

	pruned := reconcileDeletedFiles(dir, st)
	if pruned != 0 {
		t.Fatalf("expected reconcile to prune nothing for a present file, got %d", pruned)
	}
	if n := nodeCountSQL(t, dir, relPath); n == 0 {
		t.Errorf("expected present file %s to keep its nodes, got 0", relPath)
	}
}

// TestReconcileDeletedFiles_BelowFloor (T5) pins the absolute-floor semantics: with
// fewer than pruneBlastRadiusMinFiles indexed files, the >50% ratio gate is SKIPPED,
// so a tiny project where most files were deleted DOES get reconciled. Here 2 files,
// delete both (100% > 50%) — below the floor the deletes still happen.
func TestReconcileDeletedFiles_BelowFloor(t *testing.T) {
	dir, st := openPruneTestStore(t)
	const (
		aPath = "a.md"
		bPath = "b.md"
	)
	indexPruneTestFile(t, dir, st, aPath, "# A\n\nbelow-floor marker A.\n")
	indexPruneTestFile(t, dir, st, bPath, "# B\n\nbelow-floor marker B.\n")
	// Sanity: 2 < floor, so the ratio gate must NOT engage even at 100% deletion.
	if total := 2; total >= pruneBlastRadiusMinFiles {
		t.Fatalf("test assumes 2 < pruneBlastRadiusMinFiles (%d)", pruneBlastRadiusMinFiles)
	}

	if err := os.Remove(filepath.Join(dir, aPath)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, bPath)); err != nil {
		t.Fatal(err)
	}

	pruned := reconcileDeletedFiles(dir, st)
	if pruned != 2 {
		t.Fatalf("expected reconcile to prune both files below the floor, got %d", pruned)
	}
	if n := nodeCountSQL(t, dir, aPath); n != 0 {
		t.Errorf("expected 0 nodes for %s after below-floor reconcile, got %d", aPath, n)
	}
	if n := nodeCountSQL(t, dir, bPath); n != 0 {
		t.Errorf("expected 0 nodes for %s after below-floor reconcile, got %d", bPath, n)
	}
}

// TestReconcileWorkspaceProjects_PerProjectIsolation covers the --workspace reconcile path
// the single-store tests never exercise: the per-project loop, project-relative paths
// (filepath.Join(proj.Path, f.Path)), and cross-project isolation. Deleting a file in
// project A must prune ONLY A's nodes; project B is untouched. This is the runtime coverage
// the --workspace doSync branch otherwise lacked (this machine serves --path .).
func TestReconcileWorkspaceProjects_PerProjectIsolation(t *testing.T) {
	dirA, stA := openPruneTestStore(t)
	dirB, stB := openPruneTestStore(t)
	indexPruneTestFile(t, dirA, stA, "a.md", "# A\n\nproject A doc.\n")
	indexPruneTestFile(t, dirA, stA, "keep-a.md", "# KeepA\n\nstays on disk.\n")
	indexPruneTestFile(t, dirB, stB, "b.md", "# B\n\nproject B doc.\n")

	// Delete a.md from project A's disk only.
	if err := os.Remove(filepath.Join(dirA, "a.md")); err != nil {
		t.Fatal(err)
	}

	projects := []*workspace.Project{
		{Path: dirA, Store: stA},
		{Path: dirB, Store: stB},
	}
	if pruned := reconcileWorkspaceProjects(projects); pruned != 1 {
		t.Fatalf("expected exactly 1 pruned (a.md in project A), got %d", pruned)
	}
	if n := nodeCountSQL(t, dirA, "a.md"); n != 0 {
		t.Errorf("project A: a.md should be pruned, got %d nodes", n)
	}
	if n := nodeCountSQL(t, dirA, "keep-a.md"); n == 0 {
		t.Error("project A: keep-a.md must survive (present on disk)")
	}
	if n := nodeCountSQL(t, dirB, "b.md"); n == 0 {
		t.Error("project B: b.md must be untouched (different project, no deletion)")
	}
}

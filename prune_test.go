package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
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

package store

import (
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func sectionChunk(nodeID, filePath, contentHash, sectionHash, headingPath, text string, startLine, endLine int) SectionChunk {
	return SectionChunk{
		NodeID:      nodeID,
		FilePath:    filePath,
		StartLine:   startLine,
		EndLine:     endLine,
		ContentHash: contentHash,
		SectionHash: sectionHash,
		HeadingPath: headingPath,
		Text:        text,
	}
}

func getMetaValue(t *testing.T, st *Store, key string) (string, bool) {
	t.Helper()
	var val string
	err := st.db.QueryRow(`SELECT value FROM project_metadata WHERE key = ?`, key).Scan(&val)
	if err != nil {
		return "", false
	}
	return val, true
}

// ── Test 1: Migration 004 applies on fresh DB; idempotent on second open ─────

func TestMigration004_FreshDB(t *testing.T) {
	db := openRawDB(t)

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations on fresh DB: %v", err)
	}

	// 7 migration rows (001–007).
	if n := countMigrationRows(db); n != 7 {
		t.Errorf("expected 7 migration rows, got %d", n)
	}

	// PRAGMA user_version = 7.
	if v := getUserVersion(db); v != 7 {
		t.Errorf("expected user_version=7, got %d", v)
	}

	// section_chunks table must exist.
	if !tableExists(db, "section_chunks") {
		t.Error("section_chunks table not found after migration 004")
	}

	// document_metadata and governance_metadata tables must exist (F-21).
	if !tableExists(db, "document_metadata") {
		t.Error("document_metadata table not found after migration 005")
	}
	if !tableExists(db, "governance_metadata") {
		t.Error("governance_metadata table not found after migration 006")
	}
	if !tableExists(db, "research_metadata") {
		t.Error("research_metadata table not found after migration 007")
	}

	// Idempotent: run again, still 7 rows, no error.
	if err := RunMigrations(db); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}
	if n := countMigrationRows(db); n != 7 {
		t.Errorf("expected 7 migration rows after double run, got %d", n)
	}
}

// ── Test 2: FK cascade: delete node → chunk disappears ───────────────────────

func TestMigration004_FKCascade(t *testing.T) {
	st := tempStore(t)

	node := testNode("doc-cascade.md", "document", "Cascade Doc", "doc-cascade.md")
	if err := st.InsertNodes([]Node{node}); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}

	chunk := sectionChunk("doc-cascade.md", "doc-cascade.md", "chash", "shash", "", "body text", 1, 10)
	if err := st.UpsertSectionChunks([]SectionChunk{chunk}); err != nil {
		t.Fatalf("UpsertSectionChunks: %v", err)
	}

	// Verify chunk is present.
	got, found, err := st.GetSectionChunk("doc-cascade.md")
	if err != nil || !found || got == nil {
		t.Fatalf("chunk should exist before cascade: found=%v err=%v", found, err)
	}

	// Delete the node — FK cascade should remove the chunk.
	if _, err := st.db.Exec(`DELETE FROM nodes WHERE id = ?`, "doc-cascade.md"); err != nil {
		t.Fatalf("delete node: %v", err)
	}

	_, found, err = st.GetSectionChunk("doc-cascade.md")
	if err != nil {
		t.Fatalf("GetSectionChunk after cascade: %v", err)
	}
	if found {
		t.Error("expected chunk to be deleted via FK cascade, but it still exists")
	}
}

// ── Test 3: UpsertSectionChunks round-trip: all fields match ─────────────────

func TestUpsertSectionChunks_RoundTrip(t *testing.T) {
	st := tempStore(t)

	nodes := []Node{
		testNode("a.md", "document", "Doc A", "a.md"),
		testNode("a.md#intro", "heading", "Introduction", "a.md"),
		testNode("b.md", "document", "Doc B", "b.md"),
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}

	chunks := []SectionChunk{
		sectionChunk("a.md", "a.md", "chash-a", "shash-a", "", "document body", 1, 50),
		sectionChunk("a.md#intro", "a.md", "chash-a", "shash-intro", "Introduction", "intro text", 5, 15),
		sectionChunk("b.md", "b.md", "chash-b", "shash-b", "", "doc b body", -1, -1),
	}
	if err := st.UpsertSectionChunks(chunks); err != nil {
		t.Fatalf("UpsertSectionChunks: %v", err)
	}

	for _, want := range chunks {
		got, found, err := st.GetSectionChunk(want.NodeID)
		if err != nil {
			t.Fatalf("GetSectionChunk(%s): %v", want.NodeID, err)
		}
		if !found {
			t.Fatalf("GetSectionChunk(%s): not found", want.NodeID)
		}
		if got.NodeID != want.NodeID {
			t.Errorf("NodeID: got %q want %q", got.NodeID, want.NodeID)
		}
		if got.FilePath != want.FilePath {
			t.Errorf("FilePath: got %q want %q", got.FilePath, want.FilePath)
		}
		if got.StartLine != want.StartLine {
			t.Errorf("StartLine: got %d want %d", got.StartLine, want.StartLine)
		}
		if got.EndLine != want.EndLine {
			t.Errorf("EndLine: got %d want %d", got.EndLine, want.EndLine)
		}
		if got.ContentHash != want.ContentHash {
			t.Errorf("ContentHash: got %q want %q", got.ContentHash, want.ContentHash)
		}
		if got.SectionHash != want.SectionHash {
			t.Errorf("SectionHash: got %q want %q", got.SectionHash, want.SectionHash)
		}
		if got.HeadingPath != want.HeadingPath {
			t.Errorf("HeadingPath: got %q want %q", got.HeadingPath, want.HeadingPath)
		}
		if got.Text != want.Text {
			t.Errorf("Text: got %q want %q", got.Text, want.Text)
		}
	}
}

// ── Test 4: UpsertSectionChunks overwrites existing row ──────────────────────

func TestUpsertSectionChunks_Overwrite(t *testing.T) {
	st := tempStore(t)

	if err := st.InsertNodes([]Node{testNode("x.md", "document", "X", "x.md")}); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}

	c1 := sectionChunk("x.md", "x.md", "hash1", "section1", "", "original text", 1, 5)
	if err := st.UpsertSectionChunks([]SectionChunk{c1}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	c2 := sectionChunk("x.md", "x.md", "hash2", "section2", "", "updated text", 1, 8)
	if err := st.UpsertSectionChunks([]SectionChunk{c2}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, found, err := st.GetSectionChunk("x.md")
	if err != nil || !found {
		t.Fatalf("GetSectionChunk: found=%v err=%v", found, err)
	}
	if got.ContentHash != "hash2" {
		t.Errorf("ContentHash: got %q want %q", got.ContentHash, "hash2")
	}
	if got.Text != "updated text" {
		t.Errorf("Text: got %q want %q", got.Text, "updated text")
	}
}

// ── Test 5: GetSectionChunk returns (nil, false, nil) when not found ──────────

func TestGetSectionChunk_NotFound(t *testing.T) {
	st := tempStore(t)

	got, found, err := st.GetSectionChunk("nonexistent")
	if err != nil {
		t.Fatalf("GetSectionChunk: %v", err)
	}
	if found {
		t.Error("expected found=false, got true")
	}
	if got != nil {
		t.Errorf("expected nil chunk, got %+v", got)
	}
}

// ── Test 6: DeleteSectionChunksByFile removes only target file's rows ─────────

func TestDeleteSectionChunksByFile(t *testing.T) {
	st := tempStore(t)

	nodes := []Node{
		testNode("alpha.md", "document", "Alpha", "alpha.md"),
		testNode("alpha.md#s1", "heading", "Section 1", "alpha.md"),
		testNode("beta.md", "document", "Beta", "beta.md"),
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}

	chunks := []SectionChunk{
		sectionChunk("alpha.md", "alpha.md", "ha", "sa", "", "alpha body", 1, 20),
		sectionChunk("alpha.md#s1", "alpha.md", "ha", "sa1", "Section 1", "section text", 5, 10),
		sectionChunk("beta.md", "beta.md", "hb", "sb", "", "beta body", 1, 30),
	}
	if err := st.UpsertSectionChunks(chunks); err != nil {
		t.Fatalf("UpsertSectionChunks: %v", err)
	}

	// Delete alpha.md chunks.
	if err := st.DeleteSectionChunksByFile("alpha.md"); err != nil {
		t.Fatalf("DeleteSectionChunksByFile: %v", err)
	}

	// alpha.md chunks should be gone.
	for _, nodeID := range []string{"alpha.md", "alpha.md#s1"} {
		_, found, err := st.GetSectionChunk(nodeID)
		if err != nil {
			t.Fatalf("GetSectionChunk(%s): %v", nodeID, err)
		}
		if found {
			t.Errorf("expected chunk %s to be deleted, but it still exists", nodeID)
		}
	}

	// beta.md chunk must still exist.
	got, found, err := st.GetSectionChunk("beta.md")
	if err != nil {
		t.Fatalf("GetSectionChunk(beta.md): %v", err)
	}
	if !found || got == nil {
		t.Error("beta.md chunk should still exist after deleting alpha.md chunks")
	}
}

// ── Test 7: Reindex markers present in project_metadata after all migrations ──
// Migration 005 runs after 004 and overwrites reindex_scope to "metadata", so
// a full RunMigrations (all 6) will show scope="metadata" not "sections".

func TestMigration004_ReindexMarkers(t *testing.T) {
	st := tempStore(t)

	val, found := getMetaValue(t, st, MetaKeyReindexRequired)
	if !found {
		t.Fatal("reindex_required marker not found in project_metadata")
	}
	if val != "true" {
		t.Errorf("reindex_required: got %q want %q", val, "true")
	}

	// Migration 005 overwrites the scope written by 004 ("sections" → "metadata").
	scope, found := getMetaValue(t, st, MetaKeyReindexScope)
	if !found {
		t.Fatal("reindex_scope marker not found in project_metadata")
	}
	if scope != "metadata" {
		t.Errorf("reindex_scope: got %q want %q", scope, "metadata")
	}

	reason, found := getMetaValue(t, st, MetaKeyReindexReason)
	if !found {
		t.Fatal("reindex_reason marker not found in project_metadata")
	}
	if reason == "" {
		t.Error("reindex_reason should not be empty")
	}
}

// ── Test 8: UpsertSectionChunks with empty slice is a no-op ──────────────────

func TestUpsertSectionChunks_Empty(t *testing.T) {
	st := tempStore(t)

	if err := st.UpsertSectionChunks(nil); err != nil {
		t.Fatalf("UpsertSectionChunks(nil): %v", err)
	}
	if err := st.UpsertSectionChunks([]SectionChunk{}); err != nil {
		t.Fatalf("UpsertSectionChunks([]): %v", err)
	}
}

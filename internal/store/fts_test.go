package store

import (
	"strings"
	"testing"
)

func ftsMatchCount(t *testing.T, st *Store, term string) int {
	t.Helper()
	var n int
	if err := st.db.QueryRow(
		`SELECT count(*) FROM section_chunks_fts WHERE section_chunks_fts MATCH ?`,
		`"`+term+`"`,
	).Scan(&n); err != nil {
		t.Fatalf("fts match %q: %v", term, err)
	}
	return n
}

// TestSectionFTSTriggersMatchSchema guards against drift between the recreate-DDL
// const and the bootstrap copy in SchemaSQL: every CREATE TRIGGER in the const
// must appear verbatim in SchemaSQL, so a rebuilt index is identical to a freshly
// bootstrapped one.
func TestSectionFTSTriggersMatchSchema(t *testing.T) {
	for _, stmt := range strings.SplitAfter(sectionChunksFTSTriggersSQL, "END;") {
		s := strings.TrimSpace(stmt)
		if s == "" {
			continue
		}
		if !strings.Contains(SchemaSQL, s) {
			t.Errorf("trigger DDL drift — SchemaSQL is missing:\n%s", s)
		}
	}
}

func TestSectionFTSIsEmpty(t *testing.T) {
	st := tempStore(t)
	if empty, err := st.Fts.SectionFTSIsEmpty(); err != nil || !empty {
		t.Fatalf("fresh store: empty=%v err=%v, want empty=true", empty, err)
	}
	if err := st.InsertNodes([]Node{testNode("a.md", "document", "Doc A", "a.md")}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSectionChunks([]SectionChunk{
		sectionChunk("a.md", "a.md", "ch", "sh", "", "alpha bravo", 1, 9),
	}); err != nil {
		t.Fatal(err)
	}
	if empty, err := st.Fts.SectionFTSIsEmpty(); err != nil || empty {
		t.Fatalf("after insert: empty=%v err=%v, want empty=false", empty, err)
	}
}

// TestRebuildSectionFTS_EquivalentAndUpdateSafe verifies the bulk-rebuild path
// produces the same searchable FTS as the trigger path, AND that an ON CONFLICT
// DO UPDATE issued while the triggers are dropped does NOT corrupt the FTS — the
// exact failure ("database disk image is malformed") that dropping only the
// _insert trigger caused on a corpus with duplicate section node_ids.
func TestRebuildSectionFTS_EquivalentAndUpdateSafe(t *testing.T) {
	nodes := []Node{
		testNode("a.md", "document", "Doc A", "a.md"),
		testNode("a.md#h", "heading", "Heading", "a.md"),
		testNode("b.md", "document", "Doc B", "b.md"),
	}
	chunks := []SectionChunk{
		sectionChunk("a.md", "a.md", "ch", "sh", "", "alpha bravo searchterm", 1, 9),
		sectionChunk("a.md#h", "a.md", "ch", "sh2", "Heading", "charlie searchterm delta", 2, 5),
		sectionChunk("b.md", "b.md", "ch2", "sh3", "", "echo foxtrot", 1, 3),
	}

	// Baseline via the default trigger path.
	base := tempStore(t)
	if err := base.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	if err := base.UpsertSectionChunks(chunks); err != nil {
		t.Fatal(err)
	}
	want := ftsMatchCount(t, base, "searchterm") // a.md + a.md#h
	if want != 2 {
		t.Fatalf("baseline 'searchterm' = %d, want 2 (test corpus invariant)", want)
	}

	// Rebuild path: drop triggers, bulk-load (incl. an ON CONFLICT UPDATE), rebuild.
	st := tempStore(t)
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	if err := st.Fts.DropSectionFTSTriggers(); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSectionChunks(chunks); err != nil {
		t.Fatal(err)
	}
	// Re-upsert one chunk → ON CONFLICT(node_id) DO UPDATE fires the (dropped)
	// _update trigger path. Must not corrupt the FTS.
	if err := st.UpsertSectionChunks(chunks[:1]); err != nil {
		t.Fatalf("re-upsert under dropped triggers: %v", err)
	}
	// Production order: recreate triggers FIRST (FTS still empty), then rebuild as
	// the last write. A live AFTER INSERT trigger must NOT double-index during the
	// FTS-only 'rebuild'.
	if err := st.Fts.CreateSectionFTSTriggers(); err != nil {
		t.Fatal(err)
	}
	if err := st.Fts.RebuildSectionFTS(); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	if got := ftsMatchCount(t, st, "searchterm"); got != want {
		t.Errorf("rebuilt FTS 'searchterm' = %d, want %d (must equal trigger path; double-index would double this)", got, want)
	}

	// Triggers restored → a post-rebuild insert populates the FTS incrementally.
	if err := st.InsertNodes([]Node{testNode("c.md", "document", "Doc C", "c.md")}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSectionChunks([]SectionChunk{
		sectionChunk("c.md", "c.md", "ch3", "sh4", "", "golf searchterm hotel", 1, 2),
	}); err != nil {
		t.Fatal(err)
	}
	if got := ftsMatchCount(t, st, "searchterm"); got != want+1 {
		t.Errorf("after post-rebuild insert 'searchterm' = %d, want %d (trigger not restored?)", got, want+1)
	}
}

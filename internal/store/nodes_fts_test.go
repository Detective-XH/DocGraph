package store

import (
	"strings"
	"testing"
)

func nodesFTSMatchCount(t *testing.T, st *Store, term string) int {
	t.Helper()
	var n int
	if err := st.db.QueryRow(
		`SELECT count(*) FROM nodes_fts WHERE nodes_fts MATCH ?`,
		`"`+term+`"`,
	).Scan(&n); err != nil {
		t.Fatalf("nodes_fts match %q: %v", term, err)
	}
	return n
}

// TestNodesFTSTriggersMatchSchema guards against drift between the recreate-DDL
// const (nodesFTSTriggersSQL) and the bootstrap copy in SchemaSQL: every
// CREATE TRIGGER in the const must appear verbatim in SchemaSQL, so a rebuilt
// index is identical to a freshly bootstrapped one.
func TestNodesFTSTriggersMatchSchema(t *testing.T) {
	for _, stmt := range strings.SplitAfter(nodesFTSTriggersSQL, "END;") {
		s := strings.TrimSpace(stmt)
		if s == "" {
			continue
		}
		if !strings.Contains(SchemaSQL, s) {
			t.Errorf("trigger DDL drift — SchemaSQL is missing:\n%s", s)
		}
	}
}

func TestNodesFTSIsEmpty(t *testing.T) {
	st := tempStore(t)
	if empty, err := st.NodesFTSIsEmpty(); err != nil || !empty {
		t.Fatalf("fresh store: empty=%v err=%v, want empty=true", empty, err)
	}
	if err := st.InsertNodes([]Node{testNode("a.md", "document", "Doc A", "a.md")}); err != nil {
		t.Fatal(err)
	}
	if empty, err := st.NodesFTSIsEmpty(); err != nil || empty {
		t.Fatalf("after insert: empty=%v err=%v, want empty=false", empty, err)
	}
}

// nodeWithMeta builds a node carrying searchable metadata + body_excerpt, to prove
// the rebuilt FTS indexes BOTH the (formerly broken) metadata column and the body.
func nodeWithMeta(id, name, body, meta string) Node {
	n := testNode(id, "document", name, id)
	n.BodyExcerpt = body
	n.Metadata = meta
	return n
}

// TestRebuildNodesFTS_EquivalentAndSearchable verifies the bulk-rebuild path
// produces the same searchable FTS as the trigger path — including the metadata
// column, whose pre-rename misnaming (metadata_text) made 'rebuild' fail outright
// with "no such column: T.metadata_text". Also covers the rebuild's idempotency
// (running it twice must not double-index).
func TestRebuildNodesFTS_EquivalentAndSearchable(t *testing.T) {
	nodes := []Node{
		nodeWithMeta("a.md", "Alpha", "alpha bodyterm searchterm", `{"k":"metaterm"}`),
		nodeWithMeta("a.md#h", "Heading", "charlie searchterm delta", "plainmeta"),
		nodeWithMeta("b.md", "Beta", "echo foxtrot", "betameta"),
	}

	// Baseline via the default trigger path.
	base := tempStore(t)
	if err := base.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	wantBody := nodesFTSMatchCount(t, base, "searchterm") // a.md + a.md#h
	wantMeta := nodesFTSMatchCount(t, base, "metaterm")   // a.md only
	if wantBody != 2 {
		t.Fatalf("baseline 'searchterm' = %d, want 2 (test corpus invariant)", wantBody)
	}
	if wantMeta != 1 {
		t.Fatalf("baseline 'metaterm' = %d, want 1 — metadata column not indexed by trigger?", wantMeta)
	}

	// Rebuild path: drop triggers, bulk-load, recreate triggers, rebuild.
	st := tempStore(t)
	if err := st.DropNodesFTSTriggers(); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	// Production order: recreate triggers FIRST (FTS still empty), then rebuild as
	// the last write. A live AFTER INSERT trigger must NOT double-index during the
	// FTS-only 'rebuild'.
	if err := st.CreateNodesFTSTriggers(); err != nil {
		t.Fatal(err)
	}
	if err := st.RebuildNodesFTS(); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	if got := nodesFTSMatchCount(t, st, "searchterm"); got != wantBody {
		t.Errorf("rebuilt FTS 'searchterm' = %d, want %d (double-index would double this)", got, wantBody)
	}
	if got := nodesFTSMatchCount(t, st, "metaterm"); got != wantMeta {
		t.Errorf("rebuilt FTS 'metaterm' = %d, want %d — metadata column rebuild broken", got, wantMeta)
	}

	// Rebuild is idempotent — a second pass must not change counts.
	if err := st.RebuildNodesFTS(); err != nil {
		t.Fatalf("second rebuild: %v", err)
	}
	if got := nodesFTSMatchCount(t, st, "searchterm"); got != wantBody {
		t.Errorf("after 2nd rebuild 'searchterm' = %d, want %d (not idempotent)", got, wantBody)
	}

	// Triggers restored → a post-rebuild insert populates the FTS incrementally.
	if err := st.InsertNodes([]Node{nodeWithMeta("c.md", "Gamma", "golf searchterm hotel", "gammameta")}); err != nil {
		t.Fatal(err)
	}
	if got := nodesFTSMatchCount(t, st, "searchterm"); got != wantBody+1 {
		t.Errorf("after post-rebuild insert 'searchterm' = %d, want %d (trigger not restored?)", got, wantBody+1)
	}
}

// TestRebuildNodesFTS_SelfHealsEmpty mirrors the section self-heal: base rows
// present but the FTS index empty (a crash between bulk load and rebuild) must be
// recoverable by a plain rebuild, and NodesFTSIsEmpty must report the empty state
// (proving the gate probes the index, not the content table).
func TestRebuildNodesFTS_SelfHealsEmpty(t *testing.T) {
	st := tempStore(t)
	nodes := []Node{
		nodeWithMeta("a.md", "Alpha", "alpha searchterm", "m1"),
		nodeWithMeta("b.md", "Beta", "beta searchterm", "m2"),
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatal(err)
	}
	// Simulate the crash state: empty the FTS index, base rows intact.
	if err := st.DeleteAllNodesFTS(); err != nil {
		t.Fatal(err)
	}
	if empty, err := st.NodesFTSIsEmpty(); err != nil || !empty {
		t.Fatalf("after delete-all: empty=%v err=%v, want empty=true (gate must see the empty index)", empty, err)
	}
	if got := nodesFTSMatchCount(t, st, "searchterm"); got != 0 {
		t.Fatalf("after delete-all 'searchterm' = %d, want 0", got)
	}
	// Self-heal: rebuild from the intact base table.
	if err := st.RebuildNodesFTS(); err != nil {
		t.Fatalf("self-heal rebuild: %v", err)
	}
	if got := nodesFTSMatchCount(t, st, "searchterm"); got != 2 {
		t.Errorf("after self-heal 'searchterm' = %d, want 2", got)
	}
	if empty, err := st.NodesFTSIsEmpty(); err != nil || empty {
		t.Fatalf("after self-heal: empty=%v err=%v, want empty=false", empty, err)
	}
}

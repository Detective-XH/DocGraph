package store

import (
	"strings"
	"testing"
)

// TestFTS5ColumnFilter verifies that `nodes_fts MATCH 'name : "term"'` restricts
// matching to the name column only and does NOT return nodes where the term appears
// only in body_excerpt or metadata.
func TestFTS5ColumnFilter(t *testing.T) {
	st := tempStore(t)
	nodes := []Node{
		// name contains "apikey" → should match
		{ID: "a.md#h1", Kind: "heading", Name: "APIKey Configuration",
			QualifiedName: "a.md#h1", FilePath: "a.md",
			StartLine: 1, EndLine: 5, Level: 1, UpdatedAt: 1},
		// name does NOT contain "apikey"; body_excerpt does → must NOT match
		{ID: "b.md", Kind: "document", Name: "Security Overview",
			QualifiedName: "b.md", FilePath: "b.md",
			BodyExcerpt: "apikey rotation policy",
			StartLine:   1, EndLine: 100, UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	rows, err := st.db.Query(
		`SELECT n.name FROM nodes_fts
		 JOIN nodes n ON n.rowid = nodes_fts.rowid
		 WHERE nodes_fts MATCH ?`,
		`name : "apikey"`)
	if err != nil {
		t.Fatalf("FTS column filter query: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	// "APIKey Configuration" must appear (name match)
	found := false
	for _, n := range names {
		if strings.Contains(strings.ToLower(n), "apikey") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected name-column match; got %v", names)
	}

	// "Security Overview" must NOT appear (body-only match)
	for _, n := range names {
		if n == "Security Overview" {
			t.Errorf("body-only match leaked into results: %v", names)
		}
	}
}

package store

import "testing"

// fixtureFilenameVsHeading sets up the canonical P6 case: a README.md DOCUMENT
// whose title is "DocGraph" (so FTS on "README" never retrieves it — its indexed
// text is title + body, not the path), competing against a heading in another file
// that DOES contain the token "README". Without the filename collector, search
// "README" returns only the heading and the document is invisible.
func fixtureFilenameVsHeading(t *testing.T) *Store {
	t.Helper()
	st := tempStore(t)
	nodes := []Node{
		{ID: "README.md", Kind: "document", Name: "DocGraph", QualifiedName: "README.md",
			FilePath: "README.md", StartLine: 1, EndLine: 50,
			BodyExcerpt: "documentation knowledge graph mcp server for llm agents", UpdatedAt: 1},
		{ID: "guide.md#sec", Kind: "heading", Name: "Using the README file", QualifiedName: "guide.md#Using the README file",
			FilePath: "guide.md", StartLine: 5, EndLine: 20, Level: 2,
			BodyExcerpt: "", UpdatedAt: 1},
	}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	if err := st.UpsertSectionChunks([]SectionChunk{
		sectionChunk("guide.md#sec", "guide.md", "h1", "doc", "Using the README file",
			"how to read the README file shipped in this project", 5, 20),
	}); err != nil {
		t.Fatalf("UpsertSectionChunks: %v", err)
	}
	return st
}

// TestFilenameBoostSurfacesDocumentByBasename — search("README") must return the
// README.md document AND rank it above a heading that merely mentions the token.
// This fails without collectFilenameCandidates: the document is never retrieved.
func TestFilenameBoostSurfacesDocumentByBasename(t *testing.T) {
	st := fixtureFilenameVsHeading(t)

	for _, q := range []string{"README", "readme"} { // case-insensitive
		res, err := st.Searcher.Search(q, "", 10)
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}
		doc := indexOfDoc(res, "README.md")
		if doc < 0 {
			t.Fatalf("Search(%q): README.md document not surfaced (filename collector missed it)", q)
		}
		if doc != 0 {
			t.Fatalf("Search(%q): README.md must rank first (filename match), got index %d of %d results",
				q, doc, len(res))
		}
	}
}

// TestFilenameBoostInertForPhraseQuery (calibration) — a multi-word query can
// never equal a basename, so the collector must NOT fire: README.md (which no
// text field matches) stays invisible, and ranking for phrase/topic search is
// unchanged. This is the guard that the boost is inert outside find-by-name.
func TestFilenameBoostInertForPhraseQuery(t *testing.T) {
	st := fixtureFilenameVsHeading(t)

	res, err := st.Searcher.Search("README file", "", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// The filename tier is skipped for a whitespace query, so ranking is pure
	// text relevance: the heading matches BOTH terms, the README.md document only
	// the "readme" trigram in its qualified_name — so the heading must rank ABOVE
	// the document. (README.md may still appear, just not filename-elevated.)
	h, d := indexOfDoc(res, "guide.md#sec"), indexOfDoc(res, "README.md")
	if h < 0 {
		t.Fatal("phrase query should still retrieve the matching heading")
	}
	if d >= 0 && h > d {
		t.Fatalf("phrase query must NOT filename-elevate README.md: heading at %d, doc at %d", h, d)
	}
}

package workspace

import (
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// fanoutCorpus sets up a 2-project workspace in a temp dir and runs IndexAll.
// Layout:
//
//	<root>/
//	  proj-a/
//	    docs/a1.md   (tags: [alpha,shared]; entity: Acme Corp)
//	    notes/a2.md  (tags: [shared])
//	  proj-b/
//	    docs/b1.md   (tags: [beta,shared]; entity: Globex; H1="Globex Docs")
//
// Returns the open *Workspace. The caller is responsible for calling w.Close().
func fanoutCorpus(t *testing.T) *Workspace {
	t.Helper()
	root := t.TempDir()

	writeWSFile(t, root+"/proj-a/docs/a1.md", `---
tags: [alpha, shared]
entities:
  - name: Acme Corp
    type: organization
---

# Alpha Docs

Some text about Acme Corp.
`)
	writeWSFile(t, root+"/proj-a/notes/a2.md", `---
tags: [shared]
---

# Alpha Notes

Additional text.
`)
	writeWSFile(t, root+"/proj-b/docs/b1.md", `---
tags: [beta, shared]
entities:
  - name: Globex
    type: organization
---

# Globex Docs

Some text about Globex.
`)

	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	w.NoGitignore = true

	if err := w.IndexAll(); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	return w
}

// TestGetAllStats_AggregatesPerProject verifies GetAllStats returns one entry
// per project with at least one node.
func TestGetAllStats_AggregatesPerProject(t *testing.T) {
	w := fanoutCorpus(t)
	t.Cleanup(func() { w.Close() })

	stats, err := w.GetAllStats()
	if err != nil {
		t.Fatalf("GetAllStats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 project entries, got %d: %v", len(stats), stats)
	}
	for _, proj := range []string{"proj-a", "proj-b"} {
		s, ok := stats[proj]
		if !ok {
			t.Errorf("expected key %q in stats map", proj)
			continue
		}
		if s.NodeCount == 0 {
			t.Errorf("project %q: NodeCount == 0, expected > 0", proj)
		}
	}
}

// TestGetAllFiles_FanOutAndPrefixFilter verifies GetAllFiles with an empty
// filter returns files for both projects, and that a prefix filter ("notes/")
// matches proj-a but returns an empty slice for proj-b (which has no "notes/"
// files). The map key is always present for non-erroring projects.
func TestGetAllFiles_FanOutAndPrefixFilter(t *testing.T) {
	w := fanoutCorpus(t)
	t.Cleanup(func() { w.Close() })

	// Empty filter: both projects contribute files.
	all, err := w.GetAllFiles("")
	if err != nil {
		t.Fatalf("GetAllFiles(\"\"): %v", err)
	}
	for _, proj := range []string{"proj-a", "proj-b"} {
		files, ok := all[proj]
		if !ok {
			t.Errorf("empty filter: expected key %q in result map", proj)
			continue
		}
		if len(files) == 0 {
			t.Errorf("empty filter: project %q returned 0 files", proj)
		}
	}

	// "notes/" prefix: proj-a has a notes/ file; proj-b does not.
	filtered, err := w.GetAllFiles("notes/")
	if err != nil {
		t.Fatalf("GetAllFiles(\"notes/\"): %v", err)
	}
	aFiles := filtered["proj-a"]
	if len(aFiles) == 0 {
		t.Errorf("prefix filter notes/: expected >=1 file for proj-a, got 0")
	}
	// proj-b has no notes/ files; GetAllFiles always sets the key so the
	// value is an empty (nil) slice, not a missing key.
	bFiles := filtered["proj-b"]
	if len(bFiles) != 0 {
		t.Errorf("prefix filter notes/: expected 0 files for proj-b, got %d: %v", len(bFiles), bFiles)
	}
}

// TestGetAllTopLevelDirs_DedupSorted verifies that GetAllTopLevelDirs deduplicates
// and sorts top-level path segments across both projects. The corpus produces
// "docs" (from both proj-a and proj-b) and "notes" (proj-a only), so the
// deduplicated sorted result must be exactly ["docs","notes"].
func TestGetAllTopLevelDirs_DedupSorted(t *testing.T) {
	w := fanoutCorpus(t)
	t.Cleanup(func() { w.Close() })

	dirs, err := w.GetAllTopLevelDirs()
	if err != nil {
		t.Fatalf("GetAllTopLevelDirs: %v", err)
	}
	want := []string{"docs", "notes"}
	if len(dirs) != len(want) {
		t.Fatalf("expected %v, got %v", want, dirs)
	}
	for i, d := range dirs {
		if d != want[i] {
			t.Errorf("dirs[%d]: got %q, want %q", i, d, want[i])
		}
	}
}

// TestFindNodeByPath_AnnotatesProject verifies FindNodeByPath returns the node
// for proj-b's b1.md and annotates it with the project name, and that a
// missing path yields (nil, "", nil).
func TestFindNodeByPath_AnnotatesProject(t *testing.T) {
	w := fanoutCorpus(t)
	t.Cleanup(func() { w.Close() })

	n, projName, err := w.FindNodeByPath("docs/b1.md")
	if err != nil {
		t.Fatalf("FindNodeByPath: %v", err)
	}
	if n == nil {
		t.Fatal("expected non-nil node for docs/b1.md, got nil")
	}
	if projName != "proj-b" {
		t.Errorf("projectName: got %q, want %q", projName, "proj-b")
	}
	if n.ProjectName != "proj-b" {
		t.Errorf("node.ProjectName: got %q, want %q", n.ProjectName, "proj-b")
	}
	if !strings.HasPrefix(n.QualifiedName, "[proj-b] ") {
		t.Errorf("QualifiedName: expected prefix \"[proj-b] \", got %q", n.QualifiedName)
	}

	// Missing path must return (nil, "", nil) — no error.
	n2, proj2, err2 := w.FindNodeByPath("no/such/file.md")
	if err2 != nil {
		t.Fatalf("FindNodeByPath missing: unexpected error: %v", err2)
	}
	if n2 != nil || proj2 != "" {
		t.Errorf("FindNodeByPath missing: expected (nil, \"\", nil), got (%v, %q)", n2, proj2)
	}
}

// TestFindNodeByName_DocumentOnly verifies FindNodeByName returns the
// document node whose Name matches the H1 title used in b1.md ("Globex Docs").
// FindNodeByName searches document-kind nodes only.
func TestFindNodeByName_DocumentOnly(t *testing.T) {
	w := fanoutCorpus(t)
	t.Cleanup(func() { w.Close() })

	// The parser sets doc Name = first H1; b1.md's H1 is "Globex Docs".
	n, projName, err := w.FindNodeByName("Globex Docs")
	if err != nil {
		t.Fatalf("FindNodeByName: %v", err)
	}
	if n == nil {
		t.Fatal("expected non-nil node for \"Globex Docs\", got nil (document Name must equal H1 text)")
	}
	if projName != "proj-b" {
		t.Errorf("projectName: got %q, want %q", projName, "proj-b")
	}
	if n.ProjectName != "proj-b" {
		t.Errorf("node.ProjectName: got %q, want %q", n.ProjectName, "proj-b")
	}
	if !strings.HasPrefix(n.QualifiedName, "[proj-b] ") {
		t.Errorf("QualifiedName: expected prefix \"[proj-b] \", got %q", n.QualifiedName)
	}

	// Missing name must return (nil, "", nil) — no error.
	n2, proj2, err2 := w.FindNodeByName("NoSuchDocument")
	if err2 != nil {
		t.Fatalf("FindNodeByName missing: unexpected error: %v", err2)
	}
	if n2 != nil || proj2 != "" {
		t.Errorf("FindNodeByName missing: expected (nil, \"\", nil), got (%v, %q)", n2, proj2)
	}
}

// TestGetEntityStats_AggregatesAcrossProjects verifies that GetEntityStats sums
// entities from both projects. The corpus declares "Acme Corp" (proj-a) and
// "Globex" (proj-b), so TotalEntities must be >= 2.
func TestGetEntityStats_AggregatesAcrossProjects(t *testing.T) {
	w := fanoutCorpus(t)
	t.Cleanup(func() { w.Close() })

	stats, err := w.GetEntityStats()
	if err != nil {
		t.Fatalf("GetEntityStats: %v", err)
	}
	if stats.TotalEntities < 2 {
		t.Errorf("TotalEntities: got %d, expected >= 2 (Acme Corp + Globex)", stats.TotalEntities)
	}
}

// TestFindProject_FoundAndMissing verifies FindProject returns the correct
// Project for a known name and nil for an unknown name.
func TestFindProject_FoundAndMissing(t *testing.T) {
	w := fanoutCorpus(t)
	t.Cleanup(func() { w.Close() })

	p := w.FindProject("proj-a")
	if p == nil {
		t.Fatal("FindProject(\"proj-a\"): expected non-nil, got nil")
	}
	if p.Name != "proj-a" {
		t.Errorf("FindProject(\"proj-a\"): Name = %q, want %q", p.Name, "proj-a")
	}

	missing := w.FindProject("nope")
	if missing != nil {
		t.Errorf("FindProject(\"nope\"): expected nil, got %+v", missing)
	}
}

// TestGetAllStats_SkipsErroringProject verifies that GetAllStats silently omits
// a project whose Store has been closed (causing GetStats to error), without
// affecting results from the healthy project.
func TestGetAllStats_SkipsErroringProject(t *testing.T) {
	w := fanoutCorpus(t)

	// Close proj-b's store to make its GetStats error.
	projB := w.FindProject("proj-b")
	if projB == nil {
		t.Fatal("proj-b not found")
	}
	projB.Store.Close()

	stats, err := w.GetAllStats()
	if err != nil {
		t.Fatalf("GetAllStats: %v", err)
	}
	// The erroring project must be omitted.
	if _, ok := stats["proj-b"]; ok {
		t.Error("expected proj-b to be omitted from stats (its Store is closed)")
	}
	// The healthy project must still be present.
	if s, ok := stats["proj-a"]; !ok {
		t.Error("expected proj-a to be present in stats")
	} else if s.NodeCount == 0 {
		t.Error("proj-a NodeCount == 0, expected > 0")
	}

	// Remove proj-b from w.Projects so w.Close() does not double-close it.
	filtered := w.Projects[:0]
	for _, p := range w.Projects {
		if p.Name != "proj-b" {
			filtered = append(filtered, p)
		}
	}
	w.Projects = filtered
	w.Close()
}

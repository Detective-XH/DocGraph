package workspace

import (
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestIndexAllPopulatesEntityGraph guards the serve --workspace indexing path
// (IndexAll → indexProjectOpts) against the drift where entity extraction was
// added to indexStore (the CLI path) but never to the workspace path, so the
// live serve --workspace server populated zero entities and entity_type=/
// entity_id= search filters silently returned nothing. It also exercises the
// per-file DeleteEntityData + tail PruneOrphanEntities reindex path.
func TestIndexAllPopulatesEntityGraph(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project-a")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := `---
entities:
  - name: Acme Corp
    type: organization
  - name: John Smith
    type: person
---

# Entity Doc

Body referencing the organization and the person.
`
	docPath := filepath.Join(projectDir, "entities.md")
	if err := os.WriteFile(docPath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })

	if err := w.IndexAll(); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}

	p := w.FindProject("project-a")
	if p == nil {
		t.Fatal("project-a was not opened")
	}

	entities, mentions, err := p.Store.Entity.GetEntityStats()
	if err != nil {
		t.Fatalf("GetEntityStats: %v", err)
	}
	if entities != 2 {
		t.Fatalf("expected 2 entities after IndexAll, got %d (workspace path is not populating the entity graph)", entities)
	}
	if mentions != 2 {
		t.Fatalf("expected 2 entity mentions after IndexAll, got %d", mentions)
	}

	// Reindex after dropping one entity from frontmatter: the changed file's stale
	// entity_mentions row must be deleted and the now-orphaned entity pruned.
	doc2 := `---
entities:
  - name: Acme Corp
    type: organization
---

# Entity Doc

Body referencing only the organization now; this edit changes the file hash.
`
	if err := os.WriteFile(docPath, []byte(doc2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.IndexAll(); err != nil {
		t.Fatalf("IndexAll (reindex): %v", err)
	}

	entities, mentions, err = p.Store.Entity.GetEntityStats()
	if err != nil {
		t.Fatalf("GetEntityStats after reindex: %v", err)
	}
	if entities != 1 {
		t.Fatalf("expected 1 entity after reindex (orphan pruned), got %d", entities)
	}
	if mentions != 1 {
		t.Fatalf("expected 1 entity mention after reindex, got %d", mentions)
	}
}

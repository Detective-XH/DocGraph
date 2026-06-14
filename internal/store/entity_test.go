package store

import (
	"testing"
)

// makeTestNode inserts a minimal document node so entity_mentions FKs can be satisfied.
func makeTestNode(t *testing.T, st *Store, id string) {
	t.Helper()
	nodes := []Node{testNode(id, "document", id, id+".md")}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
}

// TestInsertEntities_Upsert verifies that inserting the same
// (entity_type, canonical_name_normalized) twice preserves the original UUID
// and updates the aliases field.
func TestInsertEntities_Upsert(t *testing.T) {
	st := tempStore(t)

	first := []Entity{
		{
			ID:                      "uuid-first",
			EntityType:              "person",
			CanonicalName:           "Alice",
			CanonicalNameNormalized: "alice",
			Aliases:                 []string{"A"},
			Properties:              map[string]string{"role": "admin"},
		},
	}
	if err := st.Entity.InsertEntities(first); err != nil {
		t.Fatalf("InsertEntities first: %v", err)
	}
	// The canonical ID should be reflected back into the slice.
	if first[0].ID == "" {
		t.Fatal("InsertEntities did not populate canonical ID")
	}
	originalID := first[0].ID

	// Second upsert: different UUID, same type+normalized_name.
	second := []Entity{
		{
			ID:                      "uuid-second",
			EntityType:              "person",
			CanonicalName:           "Alice",
			CanonicalNameNormalized: "alice",
			Aliases:                 []string{"A", "Al"},
			Properties:              map[string]string{"role": "user"},
		},
	}
	if err := st.Entity.InsertEntities(second); err != nil {
		t.Fatalf("InsertEntities second: %v", err)
	}

	// After upsert, the ID in second[0] must equal the preserved original.
	if second[0].ID != originalID {
		t.Fatalf("expected canonical ID %q after upsert, got %q", originalID, second[0].ID)
	}

	// Aliases should be updated to the new value.
	got, err := st.Entity.GetEntityByID(originalID)
	if err != nil {
		t.Fatalf("GetEntityByID: %v", err)
	}
	if len(got.Aliases) != 2 {
		t.Fatalf("expected 2 aliases after upsert, got %d", len(got.Aliases))
	}

	// Only one row should exist in entities.
	entities, _, err := st.Entity.GetEntityStats()
	if err != nil {
		t.Fatalf("GetEntityStats: %v", err)
	}
	if entities != 1 {
		t.Fatalf("expected 1 entity, got %d", entities)
	}
}

// TestInsertEntityMentions_IgnoreDuplicate verifies that inserting the same
// (entity_id, node_id, line) twice leaves only one row.
func TestInsertEntityMentions_IgnoreDuplicate(t *testing.T) {
	st := tempStore(t)

	makeTestNode(t, st, "doc-dup")

	ents := []Entity{{
		ID:                      "ent-dup",
		EntityType:              "org",
		CanonicalName:           "Acme",
		CanonicalNameNormalized: "acme",
	}}
	if err := st.Entity.InsertEntities(ents); err != nil {
		t.Fatalf("InsertEntities: %v", err)
	}
	entityID := ents[0].ID

	m := Mention{
		EntityID:    entityID,
		NodeID:      "doc-dup",
		FilePath:    "doc-dup.md",
		Line:        5,
		Context:     "Acme Corp founded in 1990",
		MentionType: "reference",
	}

	if err := st.Entity.InsertEntityMentions([]Mention{m}); err != nil {
		t.Fatalf("InsertEntityMentions first: %v", err)
	}
	// Insert the identical row again — should be silently ignored.
	if err := st.Entity.InsertEntityMentions([]Mention{m}); err != nil {
		t.Fatalf("InsertEntityMentions duplicate: %v", err)
	}

	mentions, err := st.Entity.GetEntityMentions("doc-dup")
	if err != nil {
		t.Fatalf("GetEntityMentions: %v", err)
	}
	if len(mentions) != 1 {
		t.Fatalf("expected 1 mention after dedup insert, got %d", len(mentions))
	}
}

// TestDeleteEntityData_PrunesOrphans verifies that after deleting all mentions
// for a file, orphan entities with no remaining mentions are removed.
func TestDeleteEntityData_PrunesOrphans(t *testing.T) {
	st := tempStore(t)

	makeTestNode(t, st, "doc-prune")

	ents := []Entity{{
		ID:                      "ent-orphan",
		EntityType:              "concept",
		CanonicalName:           "Orphan",
		CanonicalNameNormalized: "orphan",
	}}
	if err := st.Entity.InsertEntities(ents); err != nil {
		t.Fatalf("InsertEntities: %v", err)
	}
	entityID := ents[0].ID

	m := Mention{
		EntityID: entityID, NodeID: "doc-prune",
		FilePath: "doc-prune.md", Line: 1,
	}
	if err := st.Entity.InsertEntityMentions([]Mention{m}); err != nil {
		t.Fatalf("InsertEntityMentions: %v", err)
	}

	// Sanity check: entity and mention present.
	e, m2, err := st.Entity.GetEntityStats()
	if err != nil {
		t.Fatalf("GetEntityStats before delete: %v", err)
	}
	if e != 1 || m2 != 1 {
		t.Fatalf("expected 1 entity 1 mention before delete, got %d/%d", e, m2)
	}

	// Delete mentions for the file — should prune the orphan entity too.
	if err := st.Entity.DeleteEntityData("doc-prune.md"); err != nil {
		t.Fatalf("DeleteEntityData: %v", err)
	}

	e, m2, err = st.Entity.GetEntityStats()
	if err != nil {
		t.Fatalf("GetEntityStats after delete: %v", err)
	}
	if e != 0 {
		t.Fatalf("expected 0 entities after orphan pruning, got %d", e)
	}
	if m2 != 0 {
		t.Fatalf("expected 0 mentions after delete, got %d", m2)
	}
}

// TestGetEntityStats_Empty verifies (0, 0, nil) on a fresh DB.
func TestGetEntityStats_Empty(t *testing.T) {
	st := tempStore(t)
	entities, mentions, err := st.Entity.GetEntityStats()
	if err != nil {
		t.Fatalf("GetEntityStats on empty DB: %v", err)
	}
	if entities != 0 || mentions != 0 {
		t.Fatalf("expected (0,0), got (%d,%d)", entities, mentions)
	}
}

// TestGetEntityStats_WithData verifies correct counts after inserts.
func TestGetEntityStats_WithData(t *testing.T) {
	st := tempStore(t)
	makeTestNode(t, st, "doc-stats")

	ents := []Entity{
		{
			ID: "ent-a", EntityType: "person",
			CanonicalName: "Bob", CanonicalNameNormalized: "bob",
		},
		{
			ID: "ent-b", EntityType: "person",
			CanonicalName: "Carol", CanonicalNameNormalized: "carol",
		},
	}
	if err := st.Entity.InsertEntities(ents); err != nil {
		t.Fatalf("InsertEntities: %v", err)
	}

	mentions := []Mention{
		{EntityID: ents[0].ID, NodeID: "doc-stats", FilePath: "doc-stats.md", Line: 1},
		{EntityID: ents[1].ID, NodeID: "doc-stats", FilePath: "doc-stats.md", Line: 2},
	}
	if err := st.Entity.InsertEntityMentions(mentions); err != nil {
		t.Fatalf("InsertEntityMentions: %v", err)
	}

	entities, ms, err := st.Entity.GetEntityStats()
	if err != nil {
		t.Fatalf("GetEntityStats: %v", err)
	}
	if entities != 2 {
		t.Fatalf("expected 2 entities, got %d", entities)
	}
	if ms != 2 {
		t.Fatalf("expected 2 mentions, got %d", ms)
	}
}

// TestCollectEntityFilteredCandidates_ByID verifies that SearchWithOptions with
// Entity.EntityID set surfaces the document that mentions the entity.
func TestCollectEntityFilteredCandidates_ByID(t *testing.T) {
	st := tempStore(t)

	// Insert a document node with a name the FTS index can find.
	nodes := []Node{testNode("search-doc.md", "document", "EntitySearch Document", "search-doc.md")}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}

	ents := []Entity{{
		ID: "ent-search", EntityType: "concept",
		CanonicalName: "TargetConcept", CanonicalNameNormalized: "targetconcept",
	}}
	if err := st.Entity.InsertEntities(ents); err != nil {
		t.Fatalf("InsertEntities: %v", err)
	}
	entityID := ents[0].ID

	if err := st.Entity.InsertEntityMentions([]Mention{{
		EntityID: entityID, NodeID: "search-doc.md",
		FilePath: "search-doc.md", Line: 3,
	}}); err != nil {
		t.Fatalf("InsertEntityMentions: %v", err)
	}

	results, err := st.Searcher.SearchWithOptions(SearchOptions{
		Query:  "EntitySearch Document",
		Limit:  10,
		Entity: EntitySearchOptions{EntityID: entityID},
	})
	if err != nil {
		t.Fatalf("SearchWithOptions by EntityID: %v", err)
	}
	found := false
	for _, r := range results {
		if r.Node.ID == "search-doc.md" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected search-doc.md in results when filtering by EntityID, got %d results", len(results))
	}
}

// TestCollectEntityFilteredCandidates_ByType verifies the two-step
// EntityType -> entity IDs -> node IDs path.
func TestCollectEntityFilteredCandidates_ByType(t *testing.T) {
	st := tempStore(t)

	nodes := []Node{testNode("type-doc.md", "document", "TypeSearch Document", "type-doc.md")}
	if err := st.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}

	ents := []Entity{{
		ID: "ent-type", EntityType: "technology",
		CanonicalName: "GoLang", CanonicalNameNormalized: "golang",
	}}
	if err := st.Entity.InsertEntities(ents); err != nil {
		t.Fatalf("InsertEntities: %v", err)
	}

	if err := st.Entity.InsertEntityMentions([]Mention{{
		EntityID: ents[0].ID, NodeID: "type-doc.md",
		FilePath: "type-doc.md", Line: 1,
	}}); err != nil {
		t.Fatalf("InsertEntityMentions: %v", err)
	}

	results, err := st.Searcher.SearchWithOptions(SearchOptions{
		Query:  "TypeSearch Document",
		Limit:  10,
		Entity: EntitySearchOptions{EntityType: "technology"},
	})
	if err != nil {
		t.Fatalf("SearchWithOptions by EntityType: %v", err)
	}
	found := false
	for _, r := range results {
		if r.Node.ID == "type-doc.md" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected type-doc.md in results when filtering by EntityType, got %d results", len(results))
	}
}

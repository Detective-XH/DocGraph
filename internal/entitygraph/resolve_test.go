package entitygraph

import (
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

func TestNormalizeName(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Acme Corp", "acme corp"},
		{"  John Smith  ", "john smith"},
		{"UPPER CASE", "upper case"},
		{"already lower", "already lower"},
		{"", ""},
		{"  ", ""},
	}
	for _, tc := range cases {
		got := NormalizeName(tc.input)
		if got != tc.want {
			t.Errorf("NormalizeName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestDedup_NoDuplicates(t *testing.T) {
	result := &ExtractResult{
		Entities: []store.Entity{
			{ID: "id-1", EntityType: "person", CanonicalName: "Alice", CanonicalNameNormalized: "alice"},
			{ID: "id-2", EntityType: "org", CanonicalName: "Acme", CanonicalNameNormalized: "acme"},
		},
		Mentions: []store.Mention{
			{EntityID: "id-1", NodeID: "doc.md"},
			{EntityID: "id-2", NodeID: "doc.md"},
		},
	}
	Dedup(result)

	if len(result.Entities) != 2 {
		t.Errorf("expected 2 entities after dedup with no duplicates, got %d", len(result.Entities))
	}
	if result.Mentions[0].EntityID != "id-1" {
		t.Errorf("mention[0].EntityID should still be id-1, got %q", result.Mentions[0].EntityID)
	}
}

func TestDedup_MergesAliases(t *testing.T) {
	result := &ExtractResult{
		Entities: []store.Entity{
			{ID: "id-1", EntityType: "org", CanonicalName: "Acme", CanonicalNameNormalized: "acme", Aliases: []string{"ACME"}},
			{ID: "id-2", EntityType: "org", CanonicalName: "Acme", CanonicalNameNormalized: "acme", Aliases: []string{"Acme Corp"}},
		},
		Mentions: []store.Mention{
			{EntityID: "id-1", NodeID: "doc.md"},
			{EntityID: "id-2", NodeID: "doc.md"},
		},
	}
	Dedup(result)

	if len(result.Entities) != 1 {
		t.Fatalf("expected 1 entity after dedup, got %d", len(result.Entities))
	}
	e := result.Entities[0]
	if e.ID != "id-1" {
		t.Errorf("expected surviving UUID to be id-1 (first-seen), got %q", e.ID)
	}
	// Aliases from both entries should be merged: ["ACME", "Acme Corp"]
	if len(e.Aliases) != 2 {
		t.Errorf("expected 2 merged aliases, got %d: %v", len(e.Aliases), e.Aliases)
	}

	// Both mentions should now point to id-1.
	for i, m := range result.Mentions {
		if m.EntityID != "id-1" {
			t.Errorf("mention[%d].EntityID = %q, want id-1", i, m.EntityID)
		}
	}
}

func TestDedup_DifferentTypesNotMerged(t *testing.T) {
	// Same name, different type → separate entities.
	result := &ExtractResult{
		Entities: []store.Entity{
			{ID: "id-1", EntityType: "person", CanonicalName: "Alpha", CanonicalNameNormalized: "alpha"},
			{ID: "id-2", EntityType: "org", CanonicalName: "Alpha", CanonicalNameNormalized: "alpha"},
		},
		Mentions: []store.Mention{
			{EntityID: "id-1"},
			{EntityID: "id-2"},
		},
	}
	Dedup(result)

	if len(result.Entities) != 2 {
		t.Errorf("expected 2 entities (different types), got %d", len(result.Entities))
	}
}

func TestDedup_AliasDedup(t *testing.T) {
	// Overlapping aliases should not produce duplicates in the merged list.
	result := &ExtractResult{
		Entities: []store.Entity{
			{ID: "id-1", EntityType: "", CanonicalName: "X", CanonicalNameNormalized: "x", Aliases: []string{"Alias1", "SharedAlias"}},
			{ID: "id-2", EntityType: "", CanonicalName: "X", CanonicalNameNormalized: "x", Aliases: []string{"SharedAlias", "Alias2"}},
		},
		Mentions: nil,
	}
	Dedup(result)

	if len(result.Entities) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(result.Entities))
	}
	// Should be 3 unique aliases: Alias1, SharedAlias, Alias2
	if len(result.Entities[0].Aliases) != 3 {
		t.Errorf("expected 3 unique aliases, got %d: %v", len(result.Entities[0].Aliases), result.Entities[0].Aliases)
	}
}

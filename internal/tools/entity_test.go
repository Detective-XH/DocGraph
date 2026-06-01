package tools

import (
	"errors"
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

// mockEntityReader resolves entity UUIDs from a fixed table, proving
// appendEntitySection renders names/aliases through the EntityReader interface
// with no real *store.Store. Unknown IDs return sql-style not-found.
type mockEntityReader struct {
	byID  map[string]*store.Entity
	calls int
}

func (m *mockEntityReader) GetEntityByID(id string) (*store.Entity, error) {
	m.calls++
	if e, ok := m.byID[id]; ok {
		return e, nil
	}
	return nil, errors.New("not found")
}

var _ EntityReader = (*mockEntityReader)(nil)

func TestAppendEntitySection(t *testing.T) {
	r := &mockEntityReader{byID: map[string]*store.Entity{
		"uuid-1": {ID: "uuid-1", CanonicalName: "Ada Lovelace", Aliases: []string{"Ada", "A. Lovelace"}},
		"uuid-2": {ID: "uuid-2", CanonicalName: "Acme Corp"},
	}}

	t.Run("resolves name + aliases, falls back to UUID", func(t *testing.T) {
		mentions := []store.Mention{
			{EntityID: "uuid-1", MentionType: "reference", Line: 12},
			{EntityID: "uuid-2", MentionType: "definition"},
			{EntityID: "uuid-unknown", MentionType: "wikilink", Line: 3},
		}
		out := appendEntitySection(r, mentions)
		for _, want := range []string{
			"### Entity References",
			"- Ada Lovelace (aka Ada, A. Lovelace) [reference] line 12",
			"- Acme Corp [definition]\n",              // no aliases, no line
			"- entity:uuid-unknown [wikilink] line 3", // unresolved → UUID fallback
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("output missing %q\n--- output ---\n%s", want, out)
			}
		}
	})

	t.Run("dedupes repeated entities to one lookup each", func(t *testing.T) {
		r := &mockEntityReader{byID: map[string]*store.Entity{
			"uuid-1": {ID: "uuid-1", CanonicalName: "Ada Lovelace"},
		}}
		mentions := []store.Mention{
			{EntityID: "uuid-1", MentionType: "reference", Line: 1},
			{EntityID: "uuid-1", MentionType: "reference", Line: 5},
			{EntityID: "uuid-1", MentionType: "definition", Line: 9},
		}
		_ = appendEntitySection(r, mentions)
		if r.calls != 1 {
			t.Fatalf("expected 1 lookup for a repeated entity, got %d", r.calls)
		}
	})

	t.Run("empty mentions -> empty string", func(t *testing.T) {
		if got := appendEntitySection(r, nil); got != "" {
			t.Fatalf("expected empty string for no mentions, got %q", got)
		}
	})

	t.Run("nil reader -> UUID fallback, no panic", func(t *testing.T) {
		out := appendEntitySection(nil, []store.Mention{{EntityID: "uuid-1", MentionType: "reference"}})
		if !strings.Contains(out, "- entity:uuid-1 [reference]") {
			t.Fatalf("nil reader should fall back to UUID, got:\n%s", out)
		}
	})
}

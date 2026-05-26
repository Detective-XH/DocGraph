package entitygraph

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/parser"
	"github.com/Detective-XH/docgraph/internal/store"
)

// buildResult is a small helper that constructs a minimal ParseResult
// with optional metadata JSON and raw links.
func buildResult(metadataJSON string, links []parser.RawLink) *parser.ParseResult {
	return &parser.ParseResult{
		DocNode: store.Node{
			ID:       "docs/test.md",
			FilePath: "docs/test.md",
			Metadata: metadataJSON,
		},
		RawLinks: links,
	}
}

func TestFromParseResult_EmptyResult(t *testing.T) {
	res := buildResult("", nil)
	got := FromParseResult(res, nil)
	if len(got.Entities) != 0 {
		t.Errorf("expected 0 entities, got %d", len(got.Entities))
	}
	if len(got.Mentions) != 0 {
		t.Errorf("expected 0 mentions, got %d", len(got.Mentions))
	}
}

func TestFromParseResult_FrontmatterEntities(t *testing.T) {
	meta := `{"entities":[{"name":"Acme Corp","type":"organization","aliases":["Acme","ACME Corporation"]},{"name":"John Smith","type":"person"}]}`
	res := buildResult(meta, nil)
	got := FromParseResult(res, nil)

	if len(got.Entities) != 2 {
		t.Fatalf("expected 2 entities, got %d", len(got.Entities))
	}
	if len(got.Mentions) != 2 {
		t.Fatalf("expected 2 mentions, got %d", len(got.Mentions))
	}

	// Entity order: Acme Corp first, John Smith second.
	acme := got.Entities[0]
	if acme.CanonicalName != "Acme Corp" {
		t.Errorf("expected canonical name 'Acme Corp', got %q", acme.CanonicalName)
	}
	if acme.EntityType != "organization" {
		t.Errorf("expected type 'organization', got %q", acme.EntityType)
	}
	if acme.CanonicalNameNormalized != "acme corp" {
		t.Errorf("expected normalized 'acme corp', got %q", acme.CanonicalNameNormalized)
	}
	if len(acme.Aliases) != 2 {
		t.Errorf("expected 2 aliases, got %d", len(acme.Aliases))
	}
	if acme.ID == "" {
		t.Error("expected non-empty UUID")
	}

	john := got.Entities[1]
	if john.CanonicalName != "John Smith" {
		t.Errorf("expected 'John Smith', got %q", john.CanonicalName)
	}
	if len(john.Aliases) != 0 {
		t.Errorf("expected no aliases for John Smith, got %d", len(john.Aliases))
	}

	// Mentions must use definition type and point to doc node.
	for _, m := range got.Mentions {
		if m.MentionType != "definition" {
			t.Errorf("expected mention_type 'definition', got %q", m.MentionType)
		}
		if m.NodeID != "docs/test.md" {
			t.Errorf("expected node_id 'docs/test.md', got %q", m.NodeID)
		}
		if m.FilePath != "docs/test.md" {
			t.Errorf("expected file_path 'docs/test.md', got %q", m.FilePath)
		}
	}

	// Mention.EntityID must match the corresponding entity.
	if got.Mentions[0].EntityID != got.Entities[0].ID {
		t.Error("mention[0].EntityID does not match entity[0].ID")
	}
	if got.Mentions[1].EntityID != got.Entities[1].ID {
		t.Error("mention[1].EntityID does not match entity[1].ID")
	}
}

func TestFromParseResult_MaxEntities(t *testing.T) {
	// Build a metadata JSON with 600 entities — should be capped at 500.
	var items []string
	for i := 0; i < 600; i++ {
		items = append(items, fmt.Sprintf(`{"name":"Entity%d","type":"person"}`, i))
	}
	meta := `{"entities":[` + strings.Join(items, ",") + `]}`
	res := buildResult(meta, nil)
	got := FromParseResult(res, nil)

	if len(got.Entities) != MaxEntities {
		t.Errorf("expected %d entities (cap), got %d", MaxEntities, len(got.Entities))
	}
	if len(got.Mentions) != MaxEntities {
		t.Errorf("expected %d mentions (cap), got %d", MaxEntities, len(got.Mentions))
	}
}

func TestFromParseResult_WikilinkEntities(t *testing.T) {
	links := []parser.RawLink{
		{Kind: "wikilink", Target: "Acme Corp", Line: 5, FromNodeID: "docs/test.md"},
		{Kind: "markdown_link", Target: "other.md", Line: 6, FromNodeID: "docs/test.md"},
	}
	res := buildResult("", links)
	got := FromParseResult(res, map[string]bool{}) // empty = no constraint

	// Only the wikilink should produce an entity.
	if len(got.Entities) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(got.Entities))
	}
	e := got.Entities[0]
	if e.CanonicalName != "Acme Corp" {
		t.Errorf("expected 'Acme Corp', got %q", e.CanonicalName)
	}
	if e.EntityType != "" {
		t.Errorf("expected empty entity_type for wikilink, got %q", e.EntityType)
	}
	if got.Mentions[0].MentionType != "wikilink" {
		t.Errorf("expected mention_type 'wikilink', got %q", got.Mentions[0].MentionType)
	}
	if got.Mentions[0].Line != 5 {
		t.Errorf("expected line 5, got %d", got.Mentions[0].Line)
	}
}

func TestFromParseResult_AllowedTypesFiltersWikilinks(t *testing.T) {
	links := []parser.RawLink{
		{Kind: "wikilink", Target: "Allowed", Line: 1, FromNodeID: "docs/test.md"},
		{Kind: "wikilink", Target: "Blocked", Line: 2, FromNodeID: "docs/test.md"},
	}
	res := buildResult("", links)
	// Only "Allowed" is in the allowed set.
	got := FromParseResult(res, map[string]bool{"Allowed": true})

	if len(got.Entities) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(got.Entities))
	}
	if got.Entities[0].CanonicalName != "Allowed" {
		t.Errorf("expected 'Allowed', got %q", got.Entities[0].CanonicalName)
	}
}

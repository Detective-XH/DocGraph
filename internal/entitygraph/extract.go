package entitygraph

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Detective-XH/docgraph/internal/parser"
	"github.com/Detective-XH/docgraph/internal/store"
)

// frontmatterEntity mirrors the YAML structure of one item in `entities:`.
type frontmatterEntity struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Aliases []string `json:"aliases"`
}

// FromParseResult extracts entity candidates from a single parsed document.
// Sources: explicit frontmatter "entities:" list and wikilinks whose targets
// match entries in allowedTypes (or all wikilinks when allowedTypes is empty).
// No LLM, network, or file I/O.
func FromParseResult(res *parser.ParseResult, allowedTypes map[string]bool) ExtractResult {
	now := time.Now().Unix()
	var result ExtractResult

	// --- A. Frontmatter entities: ---
	fmEntities := parseFrontmatterEntities(res.DocNode.Metadata)
	for _, fe := range fmEntities {
		if len(result.Entities) >= MaxEntities {
			break
		}
		name := strings.TrimSpace(fe.Name)
		if name == "" {
			continue
		}
		aliases := capAliases(fe.Aliases)
		entityID := newUUID()
		result.Entities = append(result.Entities, store.Entity{
			ID:                      entityID,
			EntityType:              fe.Type,
			CanonicalName:           name,
			CanonicalNameNormalized: NormalizeName(name),
			Aliases:                 aliases,
			PackID:                  "",
			UpdatedAt:               now,
		})
		result.Mentions = append(result.Mentions, store.Mention{
			EntityID:    entityID,
			NodeID:      res.DocNode.ID,
			FilePath:    res.DocNode.FilePath,
			Line:        0,
			Context:     "",
			MentionType: "definition",
			UpdatedAt:   now,
		})
	}

	// --- B. Wikilinks ---
	for _, link := range res.RawLinks {
		if len(result.Entities) >= MaxEntities {
			break
		}
		if link.Kind != "wikilink" {
			continue
		}
		target := strings.TrimSpace(link.Target)
		if target == "" {
			continue
		}
		// allowedTypes empty = no constraint; otherwise target must be present
		if len(allowedTypes) > 0 && !allowedTypes[target] {
			continue
		}
		entityID := newUUID()
		result.Entities = append(result.Entities, store.Entity{
			ID:                      entityID,
			EntityType:              "",
			CanonicalName:           target,
			CanonicalNameNormalized: NormalizeName(target),
			Aliases:                 nil,
			PackID:                  "",
			UpdatedAt:               now,
		})
		result.Mentions = append(result.Mentions, store.Mention{
			EntityID:    entityID,
			NodeID:      link.FromNodeID,
			FilePath:    res.DocNode.FilePath,
			Line:        link.Line,
			Context:     "",
			MentionType: "wikilink",
			UpdatedAt:   now,
		})
	}

	return result
}

// parseFrontmatterEntities unmarshals the JSON-encoded DocNode.Metadata and
// extracts the "entities" slice. Returns nil on any error or missing key.
func parseFrontmatterEntities(metadataJSON string) []frontmatterEntity {
	if metadataJSON == "" {
		return nil
	}
	var fm map[string]json.RawMessage
	if err := json.Unmarshal([]byte(metadataJSON), &fm); err != nil {
		return nil
	}
	raw, ok := fm["entities"]
	if !ok {
		return nil
	}
	var items []frontmatterEntity
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	return items
}

// capAliases returns a copy of aliases capped at MaxAliases.
func capAliases(aliases []string) []string {
	if len(aliases) == 0 {
		return nil
	}
	if len(aliases) > MaxAliases {
		aliases = aliases[:MaxAliases]
	}
	cp := make([]string, len(aliases))
	copy(cp, aliases)
	return cp
}

// newUUID generates a random UUID v4.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

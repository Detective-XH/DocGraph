package entitygraph

import (
	"strings"
	"unicode"
)

// NormalizeName returns a lowercase-trimmed canonical form of name.
// Internal whitespace runs are collapsed to a single space.
func NormalizeName(name string) string {
	s := strings.TrimSpace(strings.ToLower(name))
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
}

// Dedup merges duplicate entities in result by (entity_type, canonical_name_normalized).
// The UUID PK of the first-seen entry is kept so FK references remain stable.
// Aliases from all duplicate entries are merged (deduplicated, capped at MaxAliases).
// Mention.EntityID values are rewritten to point to the surviving entity.
func Dedup(result *ExtractResult) {
	if len(result.Entities) == 0 {
		return
	}

	type key struct{ entityType, canonName string }

	// index: dedup key → position in the surviving entity slice
	index := make(map[key]int, len(result.Entities))
	// remap: old UUID → surviving UUID
	remap := make(map[string]string, len(result.Entities))

	survived := result.Entities[:0:len(result.Entities)]
	survived = survived[:0]

	for _, e := range result.Entities {
		k := key{e.EntityType, e.CanonicalNameNormalized}
		if pos, exists := index[k]; exists {
			// Duplicate — merge aliases into the first-seen entry.
			remap[e.ID] = survived[pos].ID
			survived[pos].Aliases = mergeAliases(survived[pos].Aliases, e.Aliases)
		} else {
			index[k] = len(survived)
			remap[e.ID] = e.ID
			survived = append(survived, e)
		}
	}

	// Rewrite Mention.EntityID using the remap table.
	for i := range result.Mentions {
		if newID, ok := remap[result.Mentions[i].EntityID]; ok {
			result.Mentions[i].EntityID = newID
		}
	}

	result.Entities = survived
}

// mergeAliases returns a deduplicated union of a and b, capped at MaxAliases.
func mergeAliases(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) > MaxAliases {
		out = out[:MaxAliases]
	}
	return out
}

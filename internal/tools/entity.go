package tools

import (
	"fmt"
	"strings"

	"github.com/Detective-XH/docgraph/internal/store"
)

// EntityReader resolves an entity UUID to its full record. appendEntitySection
// uses it to render human-readable canonical names + aliases instead of the bare
// entity UUIDs stored on mentions. Satisfied by the entity sub-store reached via
// store.Store.Entity (passed at the call sites in handleNode/handleContext); the
// sub-store type is unexported, so the satisfaction is enforced there rather than
// by a package-level var assertion.
type EntityReader interface {
	GetEntityByID(entityID string) (*store.Entity, error)
}

// appendEntitySection formats entity mention data as a Markdown section string.
// Each mention's entity UUID is resolved (via r) to its canonical name and
// aliases; an unresolvable UUID falls back to the prior "entity:<uuid>" form so
// the section degrades gracefully. Returns "" when mentions is empty.
func appendEntitySection(r EntityReader, mentions []store.Mention) string {
	if len(mentions) == 0 {
		return ""
	}

	// Resolve each distinct entity once — a doc commonly mentions the same
	// entity on several lines.
	labels := make(map[string]string)
	label := func(entityID string) string {
		if l, ok := labels[entityID]; ok {
			return l
		}
		l := "entity:" + entityID
		if r != nil {
			if e, err := r.GetEntityByID(entityID); err == nil && e != nil && e.CanonicalName != "" {
				l = e.CanonicalName
				if len(e.Aliases) > 0 {
					l += " (aka " + strings.Join(e.Aliases, ", ") + ")"
				}
			}
		}
		labels[entityID] = l
		return l
	}

	var sb strings.Builder
	sb.WriteString("\n### Entity References\n")
	for _, m := range mentions {
		fmt.Fprintf(&sb, "- %s [%s]", label(m.EntityID), m.MentionType)
		if m.Line > 0 {
			fmt.Fprintf(&sb, " line %d", m.Line)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// parseEntityFilters extracts entity filter parameters from MCP args.
func parseEntityFilters(args map[string]any) store.EntitySearchOptions {
	return store.EntitySearchOptions{
		EntityType: sanitizeArg(getStringArg(args, "entity_type", ""), 100),
		EntityID:   sanitizeArg(getStringArg(args, "entity_id", ""), 200),
	}
}

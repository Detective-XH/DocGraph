package tools

import (
	"fmt"
	"strings"

	"github.com/Detective-XH/docgraph/internal/store"
)

// appendEntitySection formats entity mention data as a Markdown section string.
// Returns "" when mentions is empty.
func appendEntitySection(mentions []store.Mention) string {
	if len(mentions) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n### Entity References\n")
	for _, m := range mentions {
		sb.WriteString(fmt.Sprintf("- entity:%s [%s]", m.EntityID, m.MentionType))
		if m.Line > 0 {
			sb.WriteString(fmt.Sprintf(" line %d", m.Line))
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

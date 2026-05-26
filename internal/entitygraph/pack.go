package entitygraph

import (
	"github.com/Detective-XH/docgraph/internal/domainpacks"
)

// AllowedTypes returns the set of allowed entity_type values drawn from packs
// whose Domain == "entity". Vocabulary values are taken from the Aliases of
// any Field with Key == "entity_type". An empty map means no vocabulary
// constraint — all entity types declared in frontmatter are accepted.
func AllowedTypes(packs []domainpacks.Pack) map[string]bool {
	allowed := make(map[string]bool)
	for _, pack := range packs {
		if pack.Domain != "entity" {
			continue
		}
		for _, field := range pack.Fields {
			if field.Key != "entity_type" {
				continue
			}
			for _, alias := range field.Aliases {
				if alias != "" {
					allowed[alias] = true
				}
			}
		}
	}
	// Empty map = unconstrained (v0.1.11 behaviour: no entity pack registered yet).
	return allowed
}

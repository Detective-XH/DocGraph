package entitygraph

import (
	"github.com/Detective-XH/docgraph/internal/domainpacks"
	"github.com/Detective-XH/docgraph/internal/parser"
	"github.com/Detective-XH/docgraph/internal/store"
)

// EntityWriter is the narrow persistence surface IndexFile needs: just the two
// entity-graph writes. Depending on this interface (rather than the concrete
// *store.Store) keeps entitygraph decoupled from the rest of the store API and
// makes IndexFile trivially mockable. *store.Store satisfies it via the thin
// forwarders in internal/store/forwarders.go.
type EntityWriter interface {
	InsertEntities([]store.Entity) error
	InsertEntityMentions([]store.Mention) error
}

var _ EntityWriter = (*store.Store)(nil)

// IndexFile extracts entity data from one parsed document and persists it.
// Called per changed file inside the indexStore loop after metadata upserts.
func IndexFile(st EntityWriter, relPath string, res *parser.ParseResult) error {
	packs := domainpacks.Packs()
	allowed := AllowedTypes(packs)
	result := FromParseResult(res, allowed)
	Dedup(&result)

	if len(result.Entities) == 0 {
		return nil
	}

	// Capture local UUIDs before InsertEntities overwrites them with the
	// canonical DB UUID (upsert on UNIQUE conflict reads back the existing PK).
	preIDs := make([]string, len(result.Entities))
	for i, e := range result.Entities {
		preIDs[i] = e.ID
	}

	if err := st.InsertEntities(result.Entities); err != nil {
		return err
	}

	if len(result.Mentions) > 0 {
		// Remap mention EntityIDs: local UUID → canonical DB UUID.
		idMap := make(map[string]string, len(result.Entities))
		for i, e := range result.Entities {
			idMap[preIDs[i]] = e.ID
		}
		for i := range result.Mentions {
			if canonical, ok := idMap[result.Mentions[i].EntityID]; ok {
				result.Mentions[i].EntityID = canonical
			}
		}
		return st.InsertEntityMentions(result.Mentions)
	}
	return nil
}

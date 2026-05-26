package entitygraph

import (
	"github.com/Detective-XH/docgraph/internal/domainpacks"
	"github.com/Detective-XH/docgraph/internal/parser"
	"github.com/Detective-XH/docgraph/internal/store"
)

// IndexFile extracts entity data from one parsed document and persists it.
// Called per changed file inside the indexStore loop after metadata upserts.
func IndexFile(st *store.Store, relPath string, res *parser.ParseResult) error {
	packs := domainpacks.Packs()
	allowed := AllowedTypes(packs)
	result := FromParseResult(res, allowed)
	Dedup(&result)

	if len(result.Entities) == 0 {
		return nil
	}
	if err := st.InsertEntities(result.Entities); err != nil {
		return err
	}
	if len(result.Mentions) > 0 {
		return st.InsertEntityMentions(result.Mentions)
	}
	return nil
}

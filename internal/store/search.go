package store

import (
	"sort"
)

// SearchIntent records the local routing decision used by SearchWithOptions.
// The intent is advisory: it selects candidate modules and boosts, but it does
// not change the stable public Search(query, kind, limit) contract.
type SearchIntent string

const (
	SearchIntentTopic   SearchIntent = "topic"
	SearchIntentExact   SearchIntent = "exact"
	SearchIntentSection SearchIntent = "section"
)

// SearchOptions keeps search quality upgrades extensible without adding new MCP tools.
// Governance, research, and entity filters are additive optional constraints.
type SearchOptions struct {
	Query string
	Kind  string
	Limit int
	// IncludeCode opts code_doc-pack results back in. Default false:
	// docgraph_search returns documentation only, so an enabled code_doc pack
	// does not flood doc queries with .go internals. The filter excludes any
	// node whose file is a code_file — the file-level code_file node AND its
	// kind="heading" children (test funcs, doc comments) — since kind alone
	// cannot tell a code heading from a markdown heading. It is a no-op when
	// the pack is off. Passing Kind == "code_file" implies IncludeCode (see
	// newSearchRequest), otherwise that filter would null out the request.
	IncludeCode bool
	Intent      SearchIntent
	Governance  GovernanceSearchOptions
	Research    ResearchSearchOptions
	Entity      EntitySearchOptions
	// ProjectFilter scopes workspace fan-out to a single project (directory name).
	// Empty means query all projects. No-op in single-store mode.
	ProjectFilter string
}

// EntitySearchOptions carries entity/source graph filter constraints.
// Empty fields are ignored so existing callers keep their default behavior.
type EntitySearchOptions struct {
	EntityType string
	EntityID   string
}

// GovernanceSearchOptions carries governance retrieval constraints. Empty
// fields are ignored so existing callers keep their default behavior while newer
// tools can opt into policy-aware filtering without adding a top-level MCP tool.
type GovernanceSearchOptions struct {
	Status          string
	Sensitivity     string
	CanonicalSource string
	AllowedAudience string
	AsOfDate        string
}

// ResearchSearchOptions carries research provenance constraints. These
// fields intentionally mirror the typed projection columns so future domain
// packs can map their own fields into the same retrieval policy layer.
type ResearchSearchOptions struct {
	ClaimID       string
	SourceType    string
	Confidence    string
	AnalystStatus string
}

// HasMetadataFilters reports whether SearchWithOptions must enforce typed
// governance/research metadata constraints in addition to relevance ranking.
// Entity filters are handled separately by collectEntityFilteredCandidates
// and must NOT set this flag — they use a different collection path.
func (opts SearchOptions) HasMetadataFilters() bool {
	return opts.Governance.Status != "" ||
		opts.Governance.Sensitivity != "" ||
		opts.Governance.CanonicalSource != "" ||
		opts.Governance.AllowedAudience != "" ||
		opts.Governance.AsOfDate != "" ||
		opts.Research.ClaimID != "" ||
		opts.Research.SourceType != "" ||
		opts.Research.Confidence != "" ||
		opts.Research.AnalystStatus != ""
}

// searchStore owns the search/retrieval domain (FTS, LIKE, ranking, and
// metadata-filtered candidate collection over nodes/section_chunks). It shares
// Store's *baseDB, so every method reaches the DB via se.db exactly as the former
// (s *Store) receivers did. Reached through Store.Searcher.
type searchStore struct {
	*baseDB
}

type searchCandidate struct {
	Node        Node
	Score       float64
	BestRank    float64
	Sources     map[string]bool
	HeadingPath string
	SectionText string
	Governance  *GovernanceRecord
	Research    *ResearchRecord
	History     *FileHistory
	// Filename marks an exact find-this-doc-by-name basename match
	// (collectFilenameCandidates) — a separate, dominant sort tier, not a score.
	Filename bool
}

func (se *searchStore) Search(query string, kind string, limit int) ([]SearchResult, error) {
	return se.SearchWithOptions(SearchOptions{Query: query, Kind: kind, Limit: limit})
}

func (se *searchStore) SearchWithOptions(opts SearchOptions) ([]SearchResult, error) {
	req := newSearchRequest(opts)
	if req.Query == "" {
		return nil, nil
	}
	if len(req.Query) > 1000 {
		req.Query = req.Query[:1000]
	}
	req.Terms = queryTerms(req.Query)
	// FTS5 uses a trigram tokenizer (>=3 chars), so any query whose terms are
	// all sub-trigram yields zero FTS rows — route it to the LIKE fallback.
	// This covers a single 2-char term AND the multi-term case (e.g. two 2-char
	// CJK words), which would otherwise hit FTS MATCH and silently return nothing.
	req.Short = len(req.Query) < 3 || allTermsSubTrigram(req.Terms)
	if req.Intent == "" {
		req.Intent = inferSearchIntent(req.Query, req.Kind)
	}
	req.ExpandedTerms = se.expandQueryTerms(req)

	candidates := make(map[string]*searchCandidate)
	if err := se.collectAllCandidates(req, candidates); err != nil {
		return nil, err
	}

	cands, govByID, researchByID, histByPath, graphByID, err := se.batchLoadMetadata(req, candidates)
	if err != nil {
		return nil, err
	}

	scored, err := se.filterAndScore(req, cands, govByID, researchByID, histByPath, graphByID)
	if err != nil {
		return nil, err
	}

	// An exact filename match sorts ahead of every text/graph-ranked result — a
	// separate find-this-doc-by-name tier, not a score that competes with (and can
	// lose to) a heading that merely mentions the token. Inert for non-filename
	// queries: no candidate sets Filename, so the tier is a no-op and the prior
	// Score(=-Rank)/FilePath/StartLine order is preserved exactly.
	sort.Slice(scored, func(i, j int) bool {
		a, b := scored[i], scored[j]
		if a.Filename != b.Filename {
			return a.Filename
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Node.FilePath != b.Node.FilePath {
			return a.Node.FilePath < b.Node.FilePath
		}
		return a.Node.StartLine < b.Node.StartLine
	})

	results := make([]SearchResult, 0, len(scored))
	for _, c := range scored {
		results = append(results, SearchResult{Node: c.Node, Rank: -c.Score})
	}
	if len(results) > req.Limit {
		results = results[:req.Limit]
	}
	return results, nil
}

// collectAllCandidates runs all candidate collectors in order, populating the
// shared candidates map. Collector call order is preserved exactly — each
// collector may accumulate score onto an existing candidate and order matters.
func (se *searchStore) collectAllCandidates(req searchRequest, candidates map[string]*searchCandidate) error {
	if err := se.collectExactCandidates(req, candidates); err != nil {
		return err
	}
	if err := se.collectFilenameCandidates(req, candidates); err != nil {
		return err
	}
	if err := se.collectNodeCandidates(req, candidates); err != nil {
		return err
	}
	if err := se.collectSectionCandidates(req, candidates); err != nil {
		return err
	}
	if err := se.collectTagCandidates(req, candidates); err != nil {
		return err
	}
	if err := se.collectDefinitionContextCandidates(req, candidates); err != nil {
		return err
	}
	if req.HasFilters {
		if err := se.collectMetadataFilteredCandidates(req, candidates); err != nil {
			return err
		}
	}
	if req.Entity.EntityType != "" || req.Entity.EntityID != "" {
		// Cross-domain read into the entity graph. searchStore embeds *baseDB only
		// (not *Store), so it cannot reach Store.Entity. Construct a throwaway
		// entityStore over the SAME shared baseDB — behaviour-identical to the
		// former s.Entity.collectEntityFilteredCandidates, since Open() wires both
		// sub-stores from one base and this collector touches only es.db.
		if err := (&entityStore{baseDB: se.baseDB}).collectEntityFilteredCandidates(req, candidates); err != nil {
			return err
		}
	}
	return nil
}

// batchLoadMetadata builds the candidate slice and fetches all ranking inputs
// (governance, research, file history, graph signals) in set-based queries.
// The previous per-candidate approach cost ~12,500 SQLite round-trips on a
// saturated search; TestSearchBatchEquivalence asserts byte-identical values.
func (se *searchStore) batchLoadMetadata(req searchRequest, candidates map[string]*searchCandidate) (
	cands []*searchCandidate,
	govByID map[string]*GovernanceRecord,
	researchByID map[string]*ResearchRecord,
	histByPath map[string]*FileHistory,
	graphByID map[string]graphSig,
	err error,
) {
	cands = make([]*searchCandidate, 0, len(candidates))
	metaIDSet := make(map[string]struct{}, len(candidates))
	pathSet := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		cands = append(cands, c)
		metaIDSet[retrievalDocID(c.Node)] = struct{}{}
		pathSet[c.Node.FilePath] = struct{}{}
	}
	metaIDs := setKeys(metaIDSet)
	govByID, err = se.getGovernanceMetadataBatch(metaIDs)
	if err != nil {
		return
	}
	researchByID, err = se.getResearchMetadataBatch(metaIDs)
	if err != nil {
		return
	}
	histByPath, err = se.getFileHistoryBatch(setKeys(pathSet))
	if err != nil {
		return
	}
	graphByID, err = se.graphSignalsBatch(req, cands)
	return
}

// filterAndScore applies metadata filters and scoring to each candidate,
// returning only the candidates that pass the metadata filter.
func (se *searchStore) filterAndScore(
	req searchRequest,
	cands []*searchCandidate,
	govByID map[string]*GovernanceRecord,
	researchByID map[string]*ResearchRecord,
	histByPath map[string]*FileHistory,
	graphByID map[string]graphSig,
) ([]*searchCandidate, error) {
	scored := make([]*searchCandidate, 0, len(cands))
	for _, c := range cands {
		docID := retrievalDocID(c.Node)
		c.Governance = govByID[docID]
		c.Research = researchByID[docID]
		c.History = histByPath[c.Node.FilePath]
		if !metadataMatchesRequest(req, c) {
			continue
		}
		se.applyFieldRanking(req, c)
		sig := graphByID[c.Node.ID]
		applyGraphScore(c, sig.incoming, sig.outgoing, sig.tagMatches)
		se.applyMetadataReranking(req, c)
		se.applyHistoryReranking(req, c)
		scored = append(scored, c)
	}
	return scored, nil
}

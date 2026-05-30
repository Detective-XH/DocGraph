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
}

func (s *Store) Search(query string, kind string, limit int) ([]SearchResult, error) {
	return s.SearchWithOptions(SearchOptions{Query: query, Kind: kind, Limit: limit})
}

func (s *Store) SearchWithOptions(opts SearchOptions) ([]SearchResult, error) {
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
	req.ExpandedTerms = s.expandQueryTerms(req)

	candidates := make(map[string]*searchCandidate)
	if err := s.collectExactCandidates(req, candidates); err != nil {
		return nil, err
	}
	if err := s.collectNodeCandidates(req, candidates); err != nil {
		return nil, err
	}
	if err := s.collectSectionCandidates(req, candidates); err != nil {
		return nil, err
	}
	if err := s.collectTagCandidates(req, candidates); err != nil {
		return nil, err
	}
	if err := s.collectDefinitionContextCandidates(req, candidates); err != nil {
		return nil, err
	}
	if req.HasFilters {
		if err := s.collectMetadataFilteredCandidates(req, candidates); err != nil {
			return nil, err
		}
	}
	if req.Entity.EntityType != "" || req.Entity.EntityID != "" {
		if err := s.Entity.collectEntityFilteredCandidates(req, candidates); err != nil {
			return nil, err
		}
	}

	results := make([]SearchResult, 0, len(candidates))
	for _, c := range candidates {
		if err := s.loadRetrievalMetadata(c); err != nil {
			return nil, err
		}
		if !metadataMatchesRequest(req, c) {
			continue
		}
		s.applyFieldRanking(req, c)
		if err := s.applyGraphReranking(req, c); err != nil {
			return nil, err
		}
		s.applyMetadataReranking(req, c)
		s.applyHistoryReranking(req, c)
		results = append(results, SearchResult{Node: c.Node, Rank: -c.Score})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Rank == results[j].Rank {
			if results[i].Node.FilePath == results[j].Node.FilePath {
				return results[i].Node.StartLine < results[j].Node.StartLine
			}
			return results[i].Node.FilePath < results[j].Node.FilePath
		}
		return results[i].Rank < results[j].Rank
	})
	if len(results) > req.Limit {
		results = results[:req.Limit]
	}
	return results, nil
}

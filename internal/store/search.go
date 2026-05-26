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

// SearchOptions keeps F-24 search quality upgrades extensible without adding
// new MCP tools. F-25 can add governance-aware ranking inputs here while the
// existing tool surface continues to call Store.Search.
type SearchOptions struct {
	Query      string
	Kind       string
	Limit      int
	Intent     SearchIntent
	Governance GovernanceSearchOptions
	Research   ResearchSearchOptions
}

// GovernanceSearchOptions carries F-25 governance retrieval constraints. Empty
// fields are ignored so existing callers keep the pre-F-25 behavior while newer
// tools can opt into policy-aware filtering without adding a top-level MCP tool.
type GovernanceSearchOptions struct {
	Status          string
	Sensitivity     string
	CanonicalSource string
	AllowedAudience string
	AsOfDate        string
}

// ResearchSearchOptions carries F-25 research provenance constraints. These
// fields intentionally mirror the typed projection columns so future domain
// packs can map their own fields into the same retrieval policy layer.
type ResearchSearchOptions struct {
	ClaimID       string
	SourceType    string
	Confidence    string
	AnalystStatus string
}

// HasMetadataFilters reports whether SearchWithOptions must enforce typed
// metadata constraints in addition to relevance ranking.
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
	req.Short = len(req.Query) < 3 || (len(req.Terms) == 1 && len([]rune(req.Terms[0])) < 3)
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

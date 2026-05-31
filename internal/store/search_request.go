package store

import (
	"database/sql"
	"strings"
	"time"
	"unicode"
)

type searchRequest struct {
	Query          string
	Kind           string
	Limit          int
	Intent         SearchIntent
	Terms          []string
	ExpandedTerms  []string
	Short          bool
	IncludeCode    bool
	CandidateLimit int
	Governance     GovernanceSearchOptions
	Research       ResearchSearchOptions
	Entity         EntitySearchOptions
	HasFilters     bool
	AsOf           time.Time
}

func newSearchRequest(opts SearchOptions) searchRequest {
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	candidateLimit := limit * 8
	if candidateLimit < 40 {
		candidateLimit = 40
	}
	if candidateLimit > 200 {
		candidateLimit = 200
	}
	governance := trimGovernanceOptions(opts.Governance)
	research := trimResearchOptions(opts.Research)
	entity := trimEntityOptions(opts.Entity)
	asOf := time.Now().UTC()
	if governance.AsOfDate != "" {
		if parsed, ok := parseSearchDate(governance.AsOfDate); ok {
			asOf = parsed
		} else {
			governance.AsOfDate = ""
		}
	}
	kind := strings.TrimSpace(opts.Kind)
	// Asking for code_file explicitly is itself an opt-in to code results;
	// otherwise the kind!='code_file' filter below would null out the request.
	includeCode := opts.IncludeCode || kind == "code_file"
	return searchRequest{
		Query:          strings.TrimSpace(opts.Query),
		Kind:           kind,
		Limit:          limit,
		Intent:         opts.Intent,
		IncludeCode:    includeCode,
		CandidateLimit: candidateLimit,
		Governance:     governance,
		Research:       research,
		Entity:         entity,
		HasFilters: SearchOptions{
			Governance: governance,
			Research:   research,
		}.HasMetadataFilters(),
		AsOf: dateOnly(asOf),
	}
}

func trimGovernanceOptions(opts GovernanceSearchOptions) GovernanceSearchOptions {
	return GovernanceSearchOptions{
		Status:          strings.TrimSpace(opts.Status),
		Sensitivity:     strings.TrimSpace(opts.Sensitivity),
		CanonicalSource: strings.TrimSpace(opts.CanonicalSource),
		AllowedAudience: strings.TrimSpace(opts.AllowedAudience),
		AsOfDate:        strings.TrimSpace(opts.AsOfDate),
	}
}

func trimResearchOptions(opts ResearchSearchOptions) ResearchSearchOptions {
	return ResearchSearchOptions{
		ClaimID:       strings.TrimSpace(opts.ClaimID),
		SourceType:    strings.TrimSpace(opts.SourceType),
		Confidence:    strings.TrimSpace(opts.Confidence),
		AnalystStatus: strings.TrimSpace(opts.AnalystStatus),
	}
}

func trimEntityOptions(opts EntitySearchOptions) EntitySearchOptions {
	return EntitySearchOptions{
		EntityType: strings.TrimSpace(opts.EntityType),
		EntityID:   strings.TrimSpace(opts.EntityID),
	}
}

func inferSearchIntent(query, kind string) SearchIntent {
	q := strings.TrimSpace(query)
	if kind == "heading" || strings.Contains(q, "#") || strings.Contains(q, " > ") {
		return SearchIntentSection
	}
	if strings.HasSuffix(q, ".md") || strings.Contains(q, ".md#") || strings.HasPrefix(q, "\"") {
		return SearchIntentExact
	}
	return SearchIntentTopic
}

func queryTerms(query string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, raw := range strings.Fields(query) {
		term := normalizeSearchTerm(raw)
		if term == "" || seen[term] {
			continue
		}
		seen[term] = true
		out = append(out, term)
	}
	return out
}

// allTermsSubTrigram reports whether every query term is shorter than the FTS5
// trigram tokenizer's 3-char minimum. Such queries produce no trigrams, so FTS
// MATCH returns nothing and the LIKE fallback must handle them. Returns false
// for an empty term list (nothing to fall back on).
func allTermsSubTrigram(terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	for _, t := range terms {
		if len([]rune(t)) >= 3 {
			return false
		}
	}
	return true
}

func normalizeSearchTerm(term string) string {
	term = strings.TrimFunc(strings.ToLower(term), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '/' || r == '.')
	})
	return term
}

// expandQueryTermsLike is the reference LIKE-based implementation kept for
// differential calibration testing. Not used in production; called only by tests.
func (s *Store) expandQueryTermsLike(req searchRequest) []string {
	if len(req.Terms) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(req.Terms))
	for _, t := range req.Terms {
		seen[t] = true
	}
	var out []string
	addTerm := func(term string) {
		term = normalizeSearchTerm(term)
		if term == "" || seen[term] {
			return
		}
		seen[term] = true
		out = append(out, term)
	}

	for _, term := range req.Terms {
		if len(out) >= 32 {
			break
		}
		pattern := "%" + escapeLike(term) + "%"
		rows, err := s.db.Query(`
			SELECT DISTINCT name
			FROM nodes
			WHERE kind IN ('heading','definition','tag')
			  AND lower(name) LIKE ? ESCAPE '\'
			ORDER BY length(name), name
			LIMIT 12`, pattern)
		if err != nil {
			continue
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				continue
			}
			for _, t := range queryTerms(name) {
				addTerm(t)
				if len(out) >= 32 {
					break
				}
			}
			if len(out) >= 32 {
				break
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			continue
		}
	}
	return out
}

func (s *Store) expandQueryTerms(req searchRequest) []string {
	if len(req.Terms) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(req.Terms))
	for _, t := range req.Terms {
		seen[t] = true
	}
	var out []string
	addTerm := func(term string) {
		term = normalizeSearchTerm(term)
		if term == "" || seen[term] {
			return
		}
		seen[term] = true
		out = append(out, term)
	}

	for _, term := range req.Terms {
		if len(out) >= 32 {
			break
		}
		var rows *sql.Rows
		var err error
		if len([]rune(term)) < 3 {
			// FTS5 trigram requires ≥3 runes; fall back to LIKE for sub-trigram terms.
			pattern := "%" + escapeLike(term) + "%"
			rows, err = s.db.Query(`
				SELECT DISTINCT name
				FROM nodes
				WHERE kind IN ('heading','definition','tag')
				  AND lower(name) LIKE ? ESCAPE '\'
				ORDER BY length(name), name
				LIMIT 12`, pattern)
		} else {
			ftsArg := buildExpansionFTSQuery(term)
			if ftsArg == "" {
				continue
			}
			rows, err = s.db.Query(`
				SELECT DISTINCT n.name
				FROM nodes_fts
				JOIN nodes n ON n.rowid = nodes_fts.rowid
				WHERE nodes_fts MATCH ?
				  AND n.kind IN ('heading','definition','tag')
				ORDER BY length(n.name), n.name
				LIMIT 12`, ftsArg)
		}
		if err != nil {
			continue
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				continue
			}
			for _, t := range queryTerms(name) {
				addTerm(t)
				if len(out) >= 32 {
					break
				}
			}
			if len(out) >= 32 {
				break
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			continue
		}
	}
	return out
}

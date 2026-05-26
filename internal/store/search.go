package store

import (
	"database/sql"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
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

type searchRequest struct {
	Query          string
	Kind           string
	Limit          int
	Intent         SearchIntent
	Terms          []string
	ExpandedTerms  []string
	Short          bool
	CandidateLimit int
	Governance     GovernanceSearchOptions
	Research       ResearchSearchOptions
	HasFilters     bool
	AsOf           time.Time
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
	asOf := time.Now().UTC()
	if governance.AsOfDate != "" {
		if parsed, ok := parseSearchDate(governance.AsOfDate); ok {
			asOf = parsed
		} else {
			governance.AsOfDate = ""
		}
	}
	return searchRequest{
		Query:          strings.TrimSpace(opts.Query),
		Kind:           strings.TrimSpace(opts.Kind),
		Limit:          limit,
		Intent:         opts.Intent,
		CandidateLimit: candidateLimit,
		Governance:     governance,
		Research:       research,
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

func normalizeSearchTerm(term string) string {
	term = strings.TrimFunc(strings.ToLower(term), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '/' || r == '.')
	})
	return term
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
		}
		rows.Close()
	}
	return out
}

func (s *Store) collectExactCandidates(req searchRequest, candidates map[string]*searchCandidate) error {
	if req.Intent != SearchIntentExact && req.Intent != SearchIntentSection {
		return nil
	}
	q := strings.Trim(req.Query, `"`)
	rows, err := s.db.Query(`
		SELECT id, kind, name, qualified_name, file_path, start_line, end_line, level, metadata, body_excerpt, updated_at
		FROM nodes
		WHERE (id = ? OR file_path = ? OR qualified_name = ? OR lower(name) = lower(?))
		  AND (? = '' OR kind = ?)
		LIMIT ?`, q, q, q, q, req.Kind, req.Kind, req.CandidateLimit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return err
		}
		addCandidate(candidates, n, "exact", 0).Score += 80
	}
	return rows.Err()
}

func (s *Store) collectNodeCandidates(req searchRequest, candidates map[string]*searchCandidate) error {
	if req.Short {
		return s.collectNodeLikeCandidates(req, candidates)
	}
	ftsQuery := buildFTSQuery(append(req.Terms, req.ExpandedTerms...))
	if ftsQuery == "" {
		return nil
	}
	rows, err := s.db.Query(`
		SELECT n.id, n.kind, n.name, n.qualified_name, n.file_path,
		       n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at,
		       bm25(nodes_fts, 8.0, 5.0, 2.0, 3.0) AS rank
		FROM nodes_fts
		JOIN nodes n ON n.rowid = nodes_fts.rowid
		WHERE nodes_fts MATCH ?
		  AND (? = '' OR n.kind = ?)
		ORDER BY rank
		LIMIT ?`, ftsQuery, req.Kind, req.Kind, req.CandidateLimit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		n, rank, err := scanRankedNode(rows)
		if err != nil {
			return err
		}
		c := addCandidate(candidates, n, "node_fts", rank)
		c.Score += ftsRankBoost(rank)
	}
	return rows.Err()
}

func (s *Store) collectNodeLikeCandidates(req searchRequest, candidates map[string]*searchCandidate) error {
	pattern := "%" + escapeLike(req.Query) + "%"
	rows, err := s.db.Query(`
		SELECT id, kind, name, qualified_name, file_path, start_line, end_line, level, metadata, body_excerpt, updated_at
		FROM nodes
		WHERE (name LIKE ? ESCAPE '\' OR qualified_name LIKE ? ESCAPE '\' OR body_excerpt LIKE ? ESCAPE '\' OR metadata LIKE ? ESCAPE '\')
		  AND (? = '' OR kind = ?)
		ORDER BY name
		LIMIT ?`, pattern, pattern, pattern, pattern, req.Kind, req.Kind, req.CandidateLimit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return err
		}
		addCandidate(candidates, n, "node_like", 0).Score += 4
	}
	return rows.Err()
}

func (s *Store) collectSectionCandidates(req searchRequest, candidates map[string]*searchCandidate) error {
	if req.Short {
		return s.collectSectionLikeCandidates(req, candidates)
	}
	ftsQuery := buildFTSQuery(append(req.Terms, req.ExpandedTerms...))
	if ftsQuery == "" {
		return nil
	}
	rows, err := s.db.Query(`
		SELECT n.id, n.kind, n.name, n.qualified_name, n.file_path,
		       n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at,
		       sc.heading_path, sc.text,
		       bm25(section_chunks_fts, 6.0, 1.0) AS rank
		FROM section_chunks_fts
		JOIN section_chunks sc ON sc.rowid = section_chunks_fts.rowid
		JOIN nodes n ON n.id = sc.node_id
		WHERE section_chunks_fts MATCH ?
		  AND (? = '' OR n.kind = ?)
		ORDER BY rank
		LIMIT ?`, ftsQuery, req.Kind, req.Kind, req.CandidateLimit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		n, headingPath, text, rank, err := scanRankedSectionNode(rows)
		if err != nil {
			return err
		}
		c := addCandidate(candidates, n, "section_fts", rank)
		c.HeadingPath = headingPath
		c.SectionText = text
		c.Score += ftsRankBoost(rank)
	}
	return rows.Err()
}

func (s *Store) collectSectionLikeCandidates(req searchRequest, candidates map[string]*searchCandidate) error {
	pattern := "%" + escapeLike(req.Query) + "%"
	rows, err := s.db.Query(`
		SELECT n.id, n.kind, n.name, n.qualified_name, n.file_path,
		       n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at,
		       sc.heading_path, sc.text
		FROM section_chunks sc
		JOIN nodes n ON n.id = sc.node_id
		WHERE (sc.heading_path LIKE ? ESCAPE '\' OR sc.text LIKE ? ESCAPE '\')
		  AND (? = '' OR n.kind = ?)
		ORDER BY n.file_path, n.start_line
		LIMIT ?`, pattern, pattern, req.Kind, req.Kind, req.CandidateLimit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		n, headingPath, text, err := scanSectionNode(rows)
		if err != nil {
			return err
		}
		c := addCandidate(candidates, n, "section_like", 0)
		c.HeadingPath = headingPath
		c.SectionText = text
		c.Score += 4
	}
	return rows.Err()
}

func (s *Store) collectTagCandidates(req searchRequest, candidates map[string]*searchCandidate) error {
	if req.Kind != "" && req.Kind != "document" {
		return nil
	}
	for _, term := range append(req.Terms, req.ExpandedTerms...) {
		rows, err := s.db.Query(`
			SELECT DISTINCT n.id, n.kind, n.name, n.qualified_name, n.file_path,
			       n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at
			FROM nodes t
			JOIN edges e ON e.target = t.id AND e.kind = 'tagged'
			JOIN nodes n ON n.id = e.source
			WHERE t.kind = 'tag'
			  AND lower(t.name) = lower(?)
			  AND (? = '' OR n.kind = ?)
			LIMIT ?`, term, req.Kind, req.Kind, req.CandidateLimit)
		if err != nil {
			return err
		}
		for rows.Next() {
			n, err := scanNode(rows)
			if err != nil {
				rows.Close()
				return err
			}
			addCandidate(candidates, n, "tag", 0).Score += 24
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}

func (s *Store) collectDefinitionContextCandidates(req searchRequest, candidates map[string]*searchCandidate) error {
	if req.Kind != "" && req.Kind != "definition" && req.Kind != "heading" && req.Kind != "document" {
		return nil
	}
	for _, term := range req.Terms {
		pattern := "%" + escapeLike(term) + "%"
		rows, err := s.db.Query(`
			SELECT DISTINCT n.id, n.kind, n.name, n.qualified_name, n.file_path,
			       n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at
			FROM nodes d
			LEFT JOIN edges e ON e.target = d.id AND e.kind = 'contains'
			JOIN nodes n ON n.id = CASE
				WHEN ? = 'definition' THEN d.id
				WHEN ? = 'document' THEN d.file_path
				WHEN e.source IS NOT NULL THEN e.source
				ELSE d.file_path
			END
			WHERE d.kind = 'definition'
			  AND lower(d.name) LIKE ? ESCAPE '\'
			  AND (? = '' OR n.kind = ?)
			LIMIT ?`, req.Kind, req.Kind, pattern, req.Kind, req.Kind, req.CandidateLimit)
		if err != nil {
			return err
		}
		for rows.Next() {
			n, err := scanNode(rows)
			if err != nil {
				rows.Close()
				return err
			}
			addCandidate(candidates, n, "definition_context", 0).Score += 18
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}

func (s *Store) collectMetadataFilteredCandidates(req searchRequest, candidates map[string]*searchCandidate) error {
	nodes, err := s.getNodesByRetrievalFilters(req)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		matches, headingPath, sectionText, err := s.nodeMatchesRetrievalQuery(req, n)
		if err != nil {
			return err
		}
		if !matches {
			continue
		}
		c := addCandidate(candidates, n, "metadata_filter", 0)
		c.Score += 16
		c.HeadingPath = headingPath
		c.SectionText = sectionText
	}
	return nil
}

func (s *Store) getNodesByRetrievalFilters(req searchRequest) ([]Node, error) {
	args := []interface{}{}
	where := []string{"n.kind = 'document'"}
	if req.Governance.Status != "" ||
		req.Governance.Sensitivity != "" ||
		req.Governance.CanonicalSource != "" ||
		req.Governance.AllowedAudience != "" {
		where = append(where, "gm.node_id IS NOT NULL")
	}
	if req.Research.ClaimID != "" ||
		req.Research.SourceType != "" ||
		req.Research.Confidence != "" ||
		req.Research.AnalystStatus != "" {
		where = append(where, "rm.node_id IS NOT NULL")
	}
	if req.Governance.Status != "" {
		where = append(where, "lower(gm.status) = lower(?)")
		args = append(args, req.Governance.Status)
	}
	if req.Governance.Sensitivity != "" {
		where = append(where, "lower(gm.sensitivity) = lower(?)")
		args = append(args, req.Governance.Sensitivity)
	}
	if req.Governance.CanonicalSource != "" {
		where = append(where, "lower(gm.canonical_source) = lower(?)")
		args = append(args, req.Governance.CanonicalSource)
	}
	if req.Governance.AsOfDate != "" {
		where = append(where, "(gm.node_id IS NULL OR gm.effective_date = '' OR date(gm.effective_date) <= date(?))")
		args = append(args, req.Governance.AsOfDate)
		where = append(where, "(rm.node_id IS NULL OR rm.valid_until = '' OR date(rm.valid_until) >= date(?))")
		args = append(args, req.Governance.AsOfDate)
	}
	if req.Research.ClaimID != "" {
		where = append(where, "lower(rm.claim_id) = lower(?)")
		args = append(args, req.Research.ClaimID)
	}
	if req.Research.SourceType != "" {
		where = append(where, "lower(rm.source_type) = lower(?)")
		args = append(args, req.Research.SourceType)
	}
	if req.Research.Confidence != "" {
		where = append(where, "lower(rm.confidence) = lower(?)")
		args = append(args, req.Research.Confidence)
	}
	if req.Research.AnalystStatus != "" {
		where = append(where, "lower(rm.analyst_status) = lower(?)")
		args = append(args, req.Research.AnalystStatus)
	}

	q := `
		SELECT n.id, n.kind, n.name, n.qualified_name, n.file_path,
		       n.start_line, n.end_line, n.level, n.metadata, n.body_excerpt, n.updated_at
		FROM nodes n
		LEFT JOIN governance_metadata gm ON gm.node_id = n.id
		LEFT JOIN research_metadata rm ON rm.node_id = n.id
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY n.id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) nodeMatchesRetrievalQuery(req searchRequest, n Node) (bool, string, string, error) {
	if req.Kind != "" && n.Kind != req.Kind {
		return false, "", "", nil
	}
	text := strings.Join([]string{n.Name, n.QualifiedName, n.Metadata, n.BodyExcerpt}, "\n")
	if queryMatchesText(req, text) {
		return true, "", "", nil
	}
	chunk, ok, err := s.GetSectionChunk(n.ID)
	if err != nil {
		return false, "", "", err
	}
	if ok && queryMatchesText(req, chunk.HeadingPath+"\n"+chunk.Text) {
		return true, chunk.HeadingPath, chunk.Text, nil
	}
	return false, "", "", nil
}

func (s *Store) applyFieldRanking(req searchRequest, c *searchCandidate) {
	c.Score += weightedTextScore(req.Query, req.Terms, c.Node.Name, 12)
	c.Score += weightedTextScore(req.Query, req.Terms, c.HeadingPath, 10)
	c.Score += weightedTextScore(req.Query, req.Terms, c.Node.QualifiedName, 6)
	c.Score += weightedTextScore(req.Query, req.Terms, c.Node.Metadata, 5)
	c.Score += weightedTextScore(req.Query, req.Terms, c.Node.BodyExcerpt, 3)
	c.Score += weightedTextScore(req.Query, req.Terms, c.SectionText, 2)

	if len(req.ExpandedTerms) > 0 {
		c.Score += weightedTermScore(req.ExpandedTerms, c.Node.Name, 2)
		c.Score += weightedTermScore(req.ExpandedTerms, c.HeadingPath, 2)
		c.Score += weightedTermScore(req.ExpandedTerms, c.SectionText, 0.75)
	}

	if c.Node.Kind == "heading" && c.Sources["section_fts"] {
		c.Score += 8
	}
	if c.Node.Kind == "definition" {
		c.Score += 5
	}
	switch req.Intent {
	case SearchIntentExact:
		if strings.EqualFold(strings.Trim(req.Query, `"`), c.Node.Name) ||
			strings.EqualFold(strings.Trim(req.Query, `"`), c.Node.FilePath) ||
			strings.EqualFold(strings.Trim(req.Query, `"`), c.Node.QualifiedName) {
			c.Score += 40
		}
	case SearchIntentSection:
		if c.Node.Kind == "heading" {
			c.Score += 12
		}
	}
}

func (s *Store) applyGraphReranking(req searchRequest, c *searchCandidate) error {
	incoming, outgoing, tagMatches, err := s.graphSignals(req, c.Node)
	if err != nil {
		return err
	}
	c.Score += math.Min(math.Log1p(float64(incoming))*3, 12)
	c.Score += math.Min(math.Log1p(float64(outgoing))*1.25, 5)
	c.Score += float64(tagMatches) * 8
	return nil
}

func (s *Store) applyMetadataReranking(req searchRequest, c *searchCandidate) {
	if c.Governance != nil {
		c.Score += governanceRetrievalScore(c.Governance, req.Governance.AllowedAudience, req.AsOf)
	}
	if c.Research != nil {
		c.Score += researchRetrievalScore(c.Research, req.AsOf)
	}
}

func (s *Store) graphSignals(req searchRequest, n Node) (incoming, outgoing, tagMatches int, err error) {
	refKinds := "('references','wikilinks_to','related_to','embeds')"
	if n.Kind == "document" {
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM edges e JOIN nodes t ON t.id = e.target WHERE t.file_path = ? AND e.kind IN `+refKinds, n.FilePath).Scan(&incoming); err != nil {
			return
		}
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM edges e JOIN nodes src ON src.id = e.source WHERE src.file_path = ? AND e.kind IN `+refKinds, n.FilePath).Scan(&outgoing); err != nil {
			return
		}
	} else {
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM edges WHERE target = ? AND kind IN `+refKinds, n.ID).Scan(&incoming); err != nil {
			return
		}
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM edges WHERE source = ? AND kind IN `+refKinds, n.ID).Scan(&outgoing); err != nil {
			return
		}
	}

	for _, term := range append(req.Terms, req.ExpandedTerms...) {
		sourceID := n.ID
		if n.Kind != "document" {
			sourceID = n.FilePath
		}
		var count int
		if scanErr := s.db.QueryRow(`
			SELECT COUNT(*)
			FROM edges e
			JOIN nodes t ON t.id = e.target
			WHERE e.source = ?
			  AND e.kind = 'tagged'
			  AND t.kind = 'tag'
			  AND lower(t.name) = lower(?)`, sourceID, term).Scan(&count); scanErr != nil {
			err = scanErr
			return
		}
		tagMatches += count
	}
	return
}

func addCandidate(candidates map[string]*searchCandidate, n Node, source string, rank float64) *searchCandidate {
	c, ok := candidates[n.ID]
	if !ok {
		c = &searchCandidate{
			Node:     n,
			BestRank: rank,
			Sources:  make(map[string]bool),
		}
		candidates[n.ID] = c
	} else if rank < c.BestRank {
		c.BestRank = rank
	}
	c.Sources[source] = true
	return c
}

func weightedTextScore(query string, terms []string, text string, weight float64) float64 {
	text = strings.ToLower(text)
	if text == "" {
		return 0
	}
	score := 0.0
	q := strings.ToLower(strings.TrimSpace(query))
	if q != "" && strings.Contains(text, q) {
		score += weight * 3
	}
	score += weightedTermScore(terms, text, weight)
	return score
}

func weightedTermScore(terms []string, text string, weight float64) float64 {
	text = strings.ToLower(text)
	if text == "" {
		return 0
	}
	score := 0.0
	for _, term := range terms {
		if term != "" && strings.Contains(text, term) {
			score += weight
		}
	}
	return score
}

func (s *Store) loadRetrievalMetadata(c *searchCandidate) error {
	docID := c.Node.ID
	if c.Node.Kind != "document" {
		docID = c.Node.FilePath
	}
	gov, err := s.GetGovernanceMetadata(docID)
	if err != nil {
		return err
	}
	research, err := s.GetResearchMetadata(docID)
	if err != nil {
		return err
	}
	c.Governance = gov
	c.Research = research
	return nil
}

func metadataMatchesRequest(req searchRequest, c *searchCandidate) bool {
	gov := c.Governance
	research := c.Research
	if req.Governance.Status != "" && (gov == nil || !equalFold(gov.Status, req.Governance.Status)) {
		return false
	}
	if req.Governance.Sensitivity != "" && (gov == nil || !equalFold(gov.Sensitivity, req.Governance.Sensitivity)) {
		return false
	}
	if req.Governance.CanonicalSource != "" && (gov == nil || !equalFold(gov.CanonicalSource, req.Governance.CanonicalSource)) {
		return false
	}
	if req.Governance.AllowedAudience != "" && !audienceAllowed(gov, req.Governance.AllowedAudience) {
		return false
	}
	if req.Governance.AsOfDate != "" {
		if gov != nil && dateAfter(gov.EffectiveDate, req.AsOf) {
			return false
		}
		if research != nil && dateBefore(research.ValidUntil, req.AsOf) {
			return false
		}
	}
	if req.Research.ClaimID != "" && (research == nil || !equalFold(research.ClaimID, req.Research.ClaimID)) {
		return false
	}
	if req.Research.SourceType != "" && (research == nil || !equalFold(research.SourceType, req.Research.SourceType)) {
		return false
	}
	if req.Research.Confidence != "" && (research == nil || !equalFold(research.Confidence, req.Research.Confidence)) {
		return false
	}
	if req.Research.AnalystStatus != "" && (research == nil || !equalFold(research.AnalystStatus, req.Research.AnalystStatus)) {
		return false
	}
	return true
}

func governanceRetrievalScore(gov *GovernanceRecord, audience string, asOf time.Time) float64 {
	score := 0.0
	switch normalizedSignal(gov.Status) {
	case "approved", "accepted", "active", "current", "final", "ratified":
		score += 12
	case "draft", "proposal", "provisional", "review":
		score -= 4
	case "archived", "deprecated", "rejected", "retired", "superseded":
		score -= 14
	}
	switch normalizedSignal(gov.Sensitivity) {
	case "", "public":
		score += 3
	case "internal", "team":
		score += 1
	case "confidential", "restricted", "secret":
		score -= 8
	}
	switch normalizedSignal(gov.CanonicalSource) {
	case "true", "yes", "canonical", "official", "primary":
		score += 8
	case "false", "no", "duplicate", "non-canonical", "noncanonical":
		score -= 10
	default:
		if strings.TrimSpace(gov.CanonicalSource) != "" {
			score += 4
		}
	}
	if audience != "" {
		if audienceAllowed(gov, audience) {
			score += 4
		} else {
			score -= 16
		}
	}
	if dateAfter(gov.EffectiveDate, asOf) {
		score -= 12
	} else if strings.TrimSpace(gov.EffectiveDate) != "" {
		score += 3
	}
	if dateBefore(gov.ReviewDue, asOf) {
		score -= 8
	} else if strings.TrimSpace(gov.ReviewDue) != "" {
		score += 2
	}
	if strings.TrimSpace(gov.SupersededBy) != "" {
		score -= 16
	}
	return score
}

func researchRetrievalScore(research *ResearchRecord, asOf time.Time) float64 {
	score := 0.0
	switch normalizedSignal(research.Confidence) {
	case "very-high", "very_high", "high":
		score += 10
	case "medium", "moderate":
		score += 4
	case "low", "weak":
		score -= 8
	}
	switch normalizedSignal(research.SourceType) {
	case "primary", "official":
		score += 6
	case "internal", "expert", "verified":
		score += 4
	case "secondary":
		score += 1
	case "social", "rumor", "unverified":
		score -= 6
	}
	switch normalizedSignal(research.AnalystStatus) {
	case "approved", "verified", "peer-reviewed", "reviewed", "final":
		score += 6
	case "draft", "open", "unverified":
		score -= 4
	case "rejected", "superseded", "withdrawn":
		score -= 10
	}
	if dateBefore(research.ValidUntil, asOf) {
		score -= 12
	} else if strings.TrimSpace(research.ValidUntil) != "" {
		score += 3
	}
	if lastVerifiedFresh(research.LastVerified, asOf) {
		score += 2
	} else if dateBefore(research.LastVerified, asOf.AddDate(-1, 0, 0)) {
		score -= 3
	}
	return score
}

func queryMatchesText(req searchRequest, text string) bool {
	text = strings.ToLower(text)
	if text == "" {
		return false
	}
	if strings.Contains(text, strings.ToLower(req.Query)) {
		return true
	}
	for _, term := range req.Terms {
		if term == "" {
			continue
		}
		if !strings.Contains(text, strings.ToLower(term)) {
			return false
		}
	}
	return len(req.Terms) > 0
}

func audienceAllowed(gov *GovernanceRecord, requested string) bool {
	requested = normalizedSignal(requested)
	if requested == "" {
		return true
	}
	if gov == nil {
		return false
	}
	if normalizedSignal(gov.Sensitivity) == "public" {
		return true
	}
	audience := strings.TrimSpace(gov.AllowedAudience)
	if audience == "" {
		return false
	}
	for _, part := range splitMetadataList(audience) {
		part = normalizedSignal(part)
		if part == requested || part == "all" || part == "*" || part == "public" {
			return true
		}
	}
	return false
}

func splitMetadataList(value string) []string {
	replacer := strings.NewReplacer("[", " ", "]", " ", "\"", " ", "'", " ", ",", " ", ";", " ", "\n", " ")
	return strings.Fields(replacer.Replace(value))
}

func parseSearchDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if len(value) >= len("2006-01-02") {
		value = value[:len("2006-01-02")]
	}
	t, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, false
	}
	return dateOnly(t), true
}

func dateAfter(value string, ref time.Time) bool {
	t, ok := parseSearchDate(value)
	return ok && t.After(dateOnly(ref))
}

func dateBefore(value string, ref time.Time) bool {
	t, ok := parseSearchDate(value)
	return ok && t.Before(dateOnly(ref))
}

func lastVerifiedFresh(value string, ref time.Time) bool {
	t, ok := parseSearchDate(value)
	if !ok {
		return false
	}
	return !t.Before(dateOnly(ref).AddDate(-1, 0, 0)) && !t.After(dateOnly(ref))
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func normalizedSignal(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func equalFold(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func ftsRankBoost(rank float64) float64 {
	if rank < 0 {
		return math.Min(-rank*1000000, 8)
	}
	return 1 / (1 + rank)
}

func buildFTSQuery(terms []string) string {
	seen := make(map[string]bool)
	var quoted []string
	for _, term := range terms {
		term = normalizeSearchTerm(term)
		if term == "" || seen[term] {
			continue
		}
		seen[term] = true
		quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
		if len(quoted) >= 32 {
			break
		}
	}
	return strings.Join(quoted, " OR ")
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

type nodeScanner interface {
	Scan(dest ...any) error
}

func scanNode(rows nodeScanner) (Node, error) {
	var n Node
	err := rows.Scan(
		&n.ID, &n.Kind, &n.Name, &n.QualifiedName,
		&n.FilePath, &n.StartLine, &n.EndLine, &n.Level,
		&n.Metadata, &n.BodyExcerpt, &n.UpdatedAt,
	)
	return n, err
}

func scanRankedNode(rows *sql.Rows) (Node, float64, error) {
	var n Node
	var rank float64
	err := rows.Scan(
		&n.ID, &n.Kind, &n.Name, &n.QualifiedName,
		&n.FilePath, &n.StartLine, &n.EndLine, &n.Level,
		&n.Metadata, &n.BodyExcerpt, &n.UpdatedAt,
		&rank,
	)
	return n, rank, err
}

func scanSectionNode(rows *sql.Rows) (Node, string, string, error) {
	var n Node
	var headingPath, text string
	err := rows.Scan(
		&n.ID, &n.Kind, &n.Name, &n.QualifiedName,
		&n.FilePath, &n.StartLine, &n.EndLine, &n.Level,
		&n.Metadata, &n.BodyExcerpt, &n.UpdatedAt,
		&headingPath, &text,
	)
	return n, headingPath, text, err
}

func scanRankedSectionNode(rows *sql.Rows) (Node, string, string, float64, error) {
	var n Node
	var headingPath, text string
	var rank float64
	err := rows.Scan(
		&n.ID, &n.Kind, &n.Name, &n.QualifiedName,
		&n.FilePath, &n.StartLine, &n.EndLine, &n.Level,
		&n.Metadata, &n.BodyExcerpt, &n.UpdatedAt,
		&headingPath, &text, &rank,
	)
	return n, headingPath, text, rank, err
}

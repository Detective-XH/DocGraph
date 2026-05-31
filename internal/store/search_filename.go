package store

import (
	"path"
	"strings"
)

// collectFilenameCandidates surfaces a DOCUMENT whose file basename (extension
// stripped) exactly equals the query — the find-this-doc-by-name intent that FTS
// misses because a document node's indexed text is its title/body, not its path
// (e.g. search("README") must return README.md even though its H1 title is
// "DocGraph", so FTS on "README" never retrieves the document at all).
//
// Calibration: it fires only on a single-token query that EXACTLY equals a
// basename, so it is inert for every multi-word topic search — existing ranking
// is unchanged for any query that is not a bare filename. inferSearchIntent
// already routes a query ending in ".md" to collectExactCandidates (file_path
// match); this collector covers the extension-less basename that path-equality
// misses, and runs regardless of intent. The LIKE prefilter narrows to the few
// document rows; the exact basename equality is verified in Go (LIKE alone would
// also admit "myREADME.md" or "README.notes.md").
func (s *Store) collectFilenameCandidates(req searchRequest, candidates map[string]*searchCandidate) error {
	q := strings.TrimSpace(strings.Trim(req.Query, `"`))
	// A basename carries no whitespace, so a phrase query can never equal one —
	// skip the scan entirely, keeping this path inert for topic search.
	if q == "" || strings.ContainsAny(q, " \t\n") {
		return nil
	}
	if req.Kind != "" && req.Kind != "document" {
		return nil
	}
	like := "%" + escapeLike(strings.ToLower(q)) + ".%"
	rows, err := s.db.Query(`
		SELECT id, kind, name, qualified_name, file_path, start_line, end_line, level, metadata, body_excerpt, updated_at
		FROM nodes
		WHERE kind = 'document'
		  AND lower(file_path) LIKE ? ESCAPE '\'
		  AND (? OR NOT EXISTS (SELECT 1 FROM nodes cf WHERE cf.file_path = nodes.file_path AND cf.kind = 'code_file'))
		LIMIT ?`, like, req.IncludeCode, req.CandidateLimit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return err
		}
		base := strings.TrimSuffix(path.Base(n.FilePath), path.Ext(n.FilePath))
		if strings.EqualFold(base, q) {
			addCandidate(candidates, n, "filename", 0).Filename = true
		}
	}
	return rows.Err()
}

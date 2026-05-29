package store

import (
	"database/sql"
	"strings"
)

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

// likeTermClauses builds a per-term AND of substring matches across cols, for
// the sub-trigram LIKE fallback (FTS can't match short/CJK terms). Each term
// must appear in at least one column → docs containing ALL terms, in any field
// and any position (whole-query LIKE would wrongly require them adjacent).
// Built only from req.Terms, never ExpandedTerms — ANDing expansions would
// over-constrain. cols are fixed identifiers (no user input); terms bind as
// args, so the assembled SQL is injection-safe. Returns "" when terms is empty,
// signalling the caller to use its raw-query fallback.
func likeTermClauses(terms []string, cols []string) (string, []any) {
	var clauses []string
	var args []any
	for _, t := range terms {
		pattern := "%" + escapeLike(t) + "%"
		ors := make([]string, len(cols))
		for i, c := range cols {
			ors[i] = c + ` LIKE ? ESCAPE '\'`
			args = append(args, pattern)
		}
		clauses = append(clauses, "("+strings.Join(ors, " OR ")+")")
	}
	return strings.Join(clauses, " AND "), args
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

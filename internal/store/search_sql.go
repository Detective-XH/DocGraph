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

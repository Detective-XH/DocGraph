package store

import (
	"strings"
	"time"
)

// This file holds small, generic helpers that are shared across more than one
// responsibility group within the store package (search, quality, entity).
// They were previously colocated in search_*.go but are not search-specific;
// keeping them here makes their cross-cutting, utility nature explicit.

// splitMetadataList tokenises a frontmatter list value (which may be a raw
// YAML/JSON-ish string) into individual entries.
func splitMetadataList(value string) []string {
	replacer := strings.NewReplacer("[", " ", "]", " ", "\"", " ", "'", " ", ",", " ", ";", " ", "\n", " ")
	return strings.Fields(replacer.Replace(value))
}

// normalizedSignal lower-cases and trims a metadata signal for comparison.
func normalizedSignal(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// dateOnly truncates a timestamp to midnight UTC.
func dateOnly(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// dateBefore reports whether the parsed date in value falls before ref (day granularity).
func dateBefore(value string, ref time.Time) bool {
	t, ok := parseSearchDate(value)
	return ok && t.Before(dateOnly(ref))
}

// nodeScanner is satisfied by both *sql.Row and *sql.Rows.
type nodeScanner interface {
	Scan(dest ...any) error
}

// scanNode reads a full node row into a Node.
func scanNode(rows nodeScanner) (Node, error) {
	var n Node
	err := rows.Scan(
		&n.ID, &n.Kind, &n.Name, &n.QualifiedName,
		&n.FilePath, &n.StartLine, &n.EndLine, &n.Level,
		&n.Metadata, &n.BodyExcerpt, &n.UpdatedAt,
	)
	return n, err
}

// addCandidate inserts or updates a search candidate in the candidate map,
// tracking its best (lowest) FTS rank and the sources that surfaced it.
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

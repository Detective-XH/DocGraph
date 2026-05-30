package store

import (
	"fmt"
	"time"
)

// findStaleByGit returns doc.stale_by_git findings: document nodes whose git
// last_commit_at is non-zero and strictly older than opts.AsOf minus
// opts.StaleByGitAfterDays. This serves the "is this doc stale?" question when a
// document has no frontmatter review_due / valid_until deadline — the gap the
// governance/research stale finders cannot cover.
//
// Inert when absent: file_history rows exist only for git-tracked corpora with
// history collection on. The INNER JOIN means history-less documents (non-git
// trees, --no-history users, never-committed files) never reach this query, and
// the `fh.last_commit_at != 0` guard drops the zero-value timestamp so an absent
// commit time is never mistaken for the unix epoch (maximally old). A doc with no
// git history therefore contributes exactly zero findings — graceful degrade.
func (s *Store) findStaleByGit(opts DriftAuditOpts) ([]DriftFinding, error) {
	cutoff := opts.AsOf.AddDate(0, 0, -opts.StaleByGitAfterDays)

	rows, err := s.db.Query(`
		SELECT n.id, n.file_path, fh.last_commit_at, fh.commit_count
		FROM file_history fh
		JOIN nodes n ON n.id = fh.path
		WHERE n.kind = 'document'
		  AND fh.last_commit_at != 0
		  AND fh.last_commit_at < ?
		ORDER BY fh.last_commit_at, n.id
		LIMIT ?
	`, cutoff.Unix(), opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("findStaleByGit query: %w", err)
	}
	defer rows.Close()

	var findings []DriftFinding
	for rows.Next() {
		var nodeID, filePath string
		var lastCommitAt int64
		var commitCount int
		if err := rows.Scan(&nodeID, &filePath, &lastCommitAt, &commitCount); err != nil {
			return nil, fmt.Errorf("findStaleByGit scan: %w", err)
		}
		lastChanged := time.Unix(lastCommitAt, 0).UTC().Format("2006-01-02")
		findings = append(findings, DriftFinding{
			Code:     CodeStaleByGit,
			NodeID:   nodeID,
			FilePath: filePath,
			Severity: "info",
			Message:  fmt.Sprintf("No git changes in over %d days (last commit %s)", opts.StaleByGitAfterDays, lastChanged),
			Evidence: fmt.Sprintf("last_commit_at=%s, commit_count=%d", lastChanged, commitCount),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("findStaleByGit rows: %w", err)
	}
	return findings, nil
}

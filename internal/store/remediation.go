package store

import (
	"path/filepath"
	"strings"
)

// isFrontmatterCapable reports whether a document format can carry YAML
// frontmatter governance fields (status/owner/review_due). PDF and DOCX cannot:
// the PDF extractor reads only the Info dictionary and the DOCX extractor reads
// only Dublin Core core.xml — neither emits governance metadata, and docx
// hardcodes HasFrontmatter:false (see internal/extractor/{pdf,docx}.go). Scoring
// those formats for missing frontmatter is therefore a structural false positive,
// so the governance "missing_*" quality findings are suppressed for them.
func isFrontmatterCapable(filePath string) bool {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".pdf", ".docx":
		return false
	}
	return true
}

// ignoreHint suggests the .docgraphignore pattern to exclude a file. For binary
// fixture formats a glob (e.g. "*.pdf") is the natural exclusion; for text docs a
// blanket extension glob would over-exclude, so the specific path is suggested.
func ignoreHint(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".pdf", ".docx":
		return "*" + ext
	}
	return filePath
}

// remediationFor returns the actionable fix for a finding code: the concrete step
// an agent (or human) takes to resolve it. The drift/quality finders only state
// the symptom; this turns each into a closed loop. Text is honest about what the
// fix requires — e.g. a .docgraphignore exclusion only takes effect on the next
// re-index (a running server re-applies on save; otherwise `docgraph sync`).
// Unknown codes return "" so the renderer simply omits the Fix line.
func remediationFor(code, filePath string) string {
	switch code {
	// Git-history drift.
	case CodeStaleByGit:
		return "If intentionally static (e.g. a test fixture or archived asset), exclude it via .docgraphignore (add `" + ignoreHint(filePath) + "`): a running DocGraph server applies the exclusion on save (the node is pruned), or rebuild with `docgraph index --force <path>`. Otherwise update the document and commit the change."

	// Policy/process drift.
	case CodePolicyStaleReview:
		return "Re-review the document and set frontmatter `review_due:` to a future date, or archive it if no longer active."
	case CodePolicySupersedeReferenced:
		return "This superseded doc is still referenced. Repoint referrers to the canonical successor, or clear `superseded_by:` if it is in fact current."
	case CodePolicyDuplicate:
		return "Near-duplicate of another policy. Merge into one canonical doc and mark the other `canonical_source: false` (or set `superseded_by:`)."
	case CodePolicyNonCanonical:
		return "Marked non-canonical/superseded but still surfaced. Point readers to the canonical source or remove it."
	case CodePolicyConflicting:
		return "Two highly-similar docs give conflicting guidance. Reconcile them and designate one canonical."

	// Research/assessment drift.
	case CodeResearchStaleAssessment:
		return "Re-verify the assessment and refresh `last_verified:` / `valid_until:`, or mark it superseded."
	case CodeResearchUnverifiedEvidence:
		return "Re-verify the cited evidence and update `last_verified:`; attach sources if missing."
	case CodeResearchCompetingInterpretations:
		return "Multiple interpretations compete. Record the chosen one and link or supersede the alternatives."
	case CodeResearchSupersededClaim:
		return "A newer claim supersedes this one. Set `superseded_by:` and repoint dependents."
	case CodeResearchImpactedDeliverable:
		return "An upstream change impacts this deliverable. Review and update it, then refresh `last_verified:`."

	// Docs-code drift (code_doc pack).
	case CodeCodeMissingSymbol:
		return "Doc links to a code path that is not indexed. Fix the path, or enable/extend the `code_doc` pack so the target is indexed."
	case CodeCodeUndocumentedExport:
		return "Code file has no incoming doc references. Add a doc that links to it, or accept if intentionally internal."
	case CodeCodeUnanchoredFeature:
		return "Approved/review doc references no indexed code. Link it to the implementing code file(s)."

	// Metadata-quality issue codes (strings, not exported constants).
	case "missing_status":
		return "Set frontmatter `status:` (e.g. draft / active / approved)."
	case "missing_owner":
		return "Set frontmatter `owner:` to the responsible person or team."
	case "missing_review_due":
		return "Set frontmatter `review_due:` to the next review date (YYYY-MM-DD)."
	case "stale_review_due":
		return "Review the document and update `review_due:` to a future date (a git edit alone does not reset it)."
	case "non_canonical":
		return "Marked non-canonical/superseded. Point to the canonical doc or remove it."
	case "missing_evidence":
		return "Add `evidence:` citations to the research frontmatter."
	case "stale_research_validity":
		return "`valid_until:` has passed. Re-verify and extend it, or mark the doc superseded."
	case "stale_last_verified":
		return "`last_verified:` is over a year old. Re-verify the content and update the date."
	case "weak_citations":
		return "Add at least two evidence citations and link them from the document body."
	case "isolated_document":
		return "No incoming or outgoing references. Link it from/to related docs, or exclude it via .docgraphignore if it is not a graph participant."
	}
	return ""
}

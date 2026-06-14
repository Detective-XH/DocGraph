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

// remediationText maps a finding code to its actionable fix. These cases are
// pure data — a static string per code — so they live in a table rather than a
// switch. CodeStaleByGit is the sole code that interpolates the file path, so it
// is handled separately in remediationFor and is intentionally absent here.
// Keys are the exported drift codes plus the metadata-quality issue strings.
var remediationText = map[string]string{
	// Policy/process drift.
	CodePolicyStaleReview:         "Re-review the document and set frontmatter `review_due:` to a future date, or archive it if no longer active.",
	CodePolicySupersedeReferenced: "This superseded doc is still referenced. Repoint referrers to the canonical successor, or clear `superseded_by:` if it is in fact current.",
	CodePolicyDuplicate:           "Near-duplicate of another policy. Merge into one canonical doc and mark the other `canonical_source: false` (or set `superseded_by:`).",
	CodePolicyNonCanonical:        "Marked non-canonical/superseded but still surfaced. Point readers to the canonical source or remove it.",
	CodePolicyConflicting:         "Two highly-similar docs give conflicting guidance. Reconcile them and designate one canonical.",

	// Research/assessment drift.
	CodeResearchStaleAssessment:          "Re-verify the assessment and refresh `last_verified:` / `valid_until:`, or mark it superseded.",
	CodeResearchUnverifiedEvidence:       "Re-verify the cited evidence and update `last_verified:`; attach sources if missing.",
	CodeResearchCompetingInterpretations: "Multiple interpretations compete. Record the chosen one and link or supersede the alternatives.",
	CodeResearchSupersededClaim:          "A newer claim supersedes this one. Set `superseded_by:` and repoint dependents.",
	CodeResearchImpactedDeliverable:      "An upstream change impacts this deliverable. Review and update it, then refresh `last_verified:`.",

	// Docs-code drift (code_doc pack).
	CodeCodeMissingSymbol:      "Doc links to a code path that is not indexed. Fix the path, or enable/extend the `code_doc` pack so the target is indexed.",
	CodeCodeUndocumentedExport: "Code file has no incoming doc references. Add a doc that links to it, or accept if intentionally internal.",
	CodeCodeUnanchoredFeature:  "Approved/review doc references no indexed code. Link it to the implementing code file(s).",

	// Metadata-quality issue codes (strings, not exported constants).
	"missing_status":          "Set frontmatter `status:` (e.g. draft / active / approved).",
	"missing_owner":           "Set frontmatter `owner:` to the responsible person or team.",
	"missing_review_due":      "Set frontmatter `review_due:` to the next review date (YYYY-MM-DD).",
	"stale_review_due":        "Review the document and update `review_due:` to a future date (a git edit alone does not reset it).",
	"non_canonical":           "Marked non-canonical/superseded. Point to the canonical doc or remove it.",
	"missing_evidence":        "Add `evidence:` citations to the research frontmatter.",
	"stale_research_validity": "`valid_until:` has passed. Re-verify and extend it, or mark the doc superseded.",
	"stale_last_verified":     "`last_verified:` is over a year old. Re-verify the content and update the date.",
	"weak_citations":          "Add at least two evidence citations and link them from the document body.",
	"isolated_document":       "No incoming or outgoing references. Link it from/to related docs, or exclude it via .docgraphignore if it is not a graph participant.",
}

// remediationFor returns the actionable fix for a finding code: the concrete step
// an agent (or human) takes to resolve it. The drift/quality finders only state
// the symptom; this turns each into a closed loop. Text is honest about what the
// fix requires — e.g. a .docgraphignore exclusion only takes effect on the next
// re-index (a running server re-applies on save; otherwise `docgraph sync`).
// CodeStaleByGit interpolates filePath; every other code is a static table
// lookup. Unknown codes return "" so the renderer simply omits the Fix line.
func remediationFor(code, filePath string) string {
	if code == CodeStaleByGit {
		return "If intentionally static (e.g. a test fixture or archived asset), exclude it via .docgraphignore (add `" + ignoreHint(filePath) + "`): a running DocGraph server applies the exclusion on save (the node is pruned), or rebuild with `docgraph index --force <path>`. Otherwise update the document and commit the change."
	}
	return remediationText[code]
}

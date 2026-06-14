package store

import (
	"strings"
	"testing"
)

// TestRemediationForCharacterization locks the remediationFor contract before
// the switch->map refactor: every known finding code maps to a non-empty,
// actionable string; the one filePath-interpolating case (CodeStaleByGit)
// embeds the ignoreHint glob/path; and unknown codes return "". Written and
// confirmed green against the original switch so the map cannot silently drop,
// reorder, or corrupt a case.
func TestRemediationForCharacterization(t *testing.T) {
	const samplePath = "docs/policy/governance.md"

	// Every known code must yield a non-empty remediation. A dropped or
	// mistyped map key surfaces here.
	knownCodes := []string{
		CodeStaleByGit,
		CodePolicyStaleReview,
		CodePolicySupersedeReferenced,
		CodePolicyDuplicate,
		CodePolicyNonCanonical,
		CodePolicyConflicting,
		CodeResearchStaleAssessment,
		CodeResearchUnverifiedEvidence,
		CodeResearchCompetingInterpretations,
		CodeResearchSupersededClaim,
		CodeResearchImpactedDeliverable,
		CodeCodeMissingSymbol,
		CodeCodeUndocumentedExport,
		CodeCodeUnanchoredFeature,
		"missing_status",
		"missing_owner",
		"missing_review_due",
		"stale_review_due",
		"non_canonical",
		"missing_evidence",
		"stale_research_validity",
		"stale_last_verified",
		"weak_citations",
		"isolated_document",
	}
	for _, code := range knownCodes {
		if got := remediationFor(code, samplePath); got == "" {
			t.Errorf("remediationFor(%q) returned empty; expected an actionable remediation", code)
		}
	}

	// Spot-check exact text for one case per family — guards against gross
	// string corruption that a non-empty check would miss.
	exact := map[string]string{
		CodePolicyStaleReview:       "Re-review the document and set frontmatter `review_due:` to a future date, or archive it if no longer active.",
		CodeResearchSupersededClaim: "A newer claim supersedes this one. Set `superseded_by:` and repoint dependents.",
		CodeCodeUndocumentedExport:  "Code file has no incoming doc references. Add a doc that links to it, or accept if intentionally internal.",
		"missing_owner":             "Set frontmatter `owner:` to the responsible person or team.",
		"isolated_document":         "No incoming or outgoing references. Link it from/to related docs, or exclude it via .docgraphignore if it is not a graph participant.",
	}
	for code, want := range exact {
		if got := remediationFor(code, samplePath); got != want {
			t.Errorf("remediationFor(%q) =\n  %q\nwant\n  %q", code, got, want)
		}
	}

	// CodeStaleByGit is the only case that interpolates filePath via ignoreHint:
	// a binary fixture suggests an extension glob, a text doc the specific path.
	if got := remediationFor(CodeStaleByGit, "fixtures/sample.pdf"); !strings.Contains(got, "*.pdf") {
		t.Errorf("remediationFor(CodeStaleByGit, .pdf) should embed the %q glob, got:\n%s", "*.pdf", got)
	}
	if got := remediationFor(CodeStaleByGit, samplePath); !strings.Contains(got, samplePath) {
		t.Errorf("remediationFor(CodeStaleByGit, .md) should embed the path %q, got:\n%s", samplePath, got)
	}

	// Unknown codes return "" so the renderer omits the Fix line.
	if got := remediationFor("definitely_not_a_code", samplePath); got != "" {
		t.Errorf("remediationFor(unknown) = %q; want empty", got)
	}
}

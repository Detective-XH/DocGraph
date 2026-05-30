package tools

import (
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

// TestAppendDriftFindingsMarkdown_StaleByGitSurfaces proves the doc.stale_by_git
// finding survives the tool layer: appendDriftFindingsMarkdown is the formatter
// renderDriftAudit (the docgraph_context format=drift_audit path) uses for both
// single-store and workspace output. The store layer is proven to PRODUCE the
// finding (internal/store drift_audit_git_test.go); this proves the MCP surface
// RENDERS it — closing the only path the store-layer suite cannot exercise. The
// headline use case is a corpus with no domain packs and no frontmatter, so the
// finding must surface with packs off; nothing here enables a pack.
func TestAppendDriftFindingsMarkdown_StaleByGitSurfaces(t *testing.T) {
	findings := []store.DriftFinding{{
		Code:     store.CodeStaleByGit,
		NodeID:   "docs/old.md",
		FilePath: "docs/old.md",
		Severity: "info",
		Message:  "No git changes in over 365 days (last commit 2024-01-01)",
		Evidence: "last_commit_at=2024-01-01, commit_count=3",
	}}

	var sb strings.Builder
	appendDriftFindingsMarkdown(&sb, findings)
	out := sb.String()

	for _, want := range []string{
		"Total findings:** 1",
		store.CodeStaleByGit, // the code header must appear
		"docs/old.md",        // the finding's path
		"No git changes in over 365 days",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("drift_audit render missing %q — doc.stale_by_git did not survive the tool layer\n--- output ---\n%s", want, out)
		}
	}
}

// TestAppendDriftFindingsMarkdown_UnlistedCodeSurfaces guards the defensive
// fallback: a finding whose code is NOT in the curated codeOrder must still
// render (be appended), never be silently dropped. This is what protects any
// future finding code added to the store from vanishing at the MCP surface until
// someone remembers to update codeOrder.
func TestAppendDriftFindingsMarkdown_UnlistedCodeSurfaces(t *testing.T) {
	findings := []store.DriftFinding{{
		Code:     "future.unlisted_code",
		NodeID:   "docs/x.md",
		FilePath: "docs/x.md",
		Severity: "info",
		Message:  "a brand new finding kind",
	}}

	var sb strings.Builder
	appendDriftFindingsMarkdown(&sb, findings)
	out := sb.String()

	if !strings.Contains(out, "future.unlisted_code") || !strings.Contains(out, "a brand new finding kind") {
		t.Fatalf("an unlisted finding code was silently dropped at the render layer\n--- output ---\n%s", out)
	}
}

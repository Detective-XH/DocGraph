package store

import (
	"testing"
	"time"
)

// Git-history drift timestamps, anchored to staleTestAsOf (2026-01-01) with the
// default 365-day threshold → cutoff 2025-01-01.
var (
	// gitStaleTs is before the cutoff → flagged.
	gitStaleTs = time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC).Unix()
	// gitRecentTs is after the cutoff but before AsOf → not flagged.
	gitRecentTs = time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC).Unix()
)

// ---- findStaleByGit ----

// TestStaleByGit_Positive: a document whose last git commit predates the cutoff
// is flagged exactly once with the right code and path.
func TestStaleByGit_Positive(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "docs/old.md", "docs/old.md")
	if err := st.UpsertFileHistory(FileHistory{
		Path: "docs/old.md", CommitCount: 3, AuthorCount: 1, LastCommitAt: gitStaleTs,
	}); err != nil {
		t.Fatalf("UpsertFileHistory: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf, StaleByGitAfterDays: 365}
	findings, err := st.findStaleByGit(opts)
	if err != nil {
		t.Fatalf("findStaleByGit: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Code != CodeStaleByGit {
		t.Errorf("Code = %q, want %q", f.Code, CodeStaleByGit)
	}
	if f.FilePath != "docs/old.md" {
		t.Errorf("FilePath = %q, want docs/old.md", f.FilePath)
	}
	if f.NodeID != "docs/old.md" {
		t.Errorf("NodeID = %q, want docs/old.md", f.NodeID)
	}
	if f.Severity != "info" {
		t.Errorf("Severity = %q, want info", f.Severity)
	}
	if f.Message == "" {
		t.Error("Message must not be empty")
	}
	if f.Evidence == "" {
		t.Error("Evidence must not be empty")
	}
}

// TestStaleByGit_RecentExcluded: a document committed after the cutoff is not
// flagged — proves the threshold gate, not just "any history row".
func TestStaleByGit_RecentExcluded(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "docs/recent.md", "docs/recent.md")
	if err := st.UpsertFileHistory(FileHistory{
		Path: "docs/recent.md", CommitCount: 2, AuthorCount: 1, LastCommitAt: gitRecentTs,
	}); err != nil {
		t.Fatalf("UpsertFileHistory: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf, StaleByGitAfterDays: 365}
	findings, err := st.findStaleByGit(opts)
	if err != nil {
		t.Fatalf("findStaleByGit: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for recent commit, got %d: %+v", len(findings), findings)
	}
}

// TestStaleByGit_AbsentExcluded: a document with NO file_history row is never
// flagged. This is the inert-when-absent / --no-history graceful-degrade proof:
// non-git corpora and never-committed files contribute zero.
func TestStaleByGit_AbsentExcluded(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "docs/nohistory.md", "docs/nohistory.md")
	// Deliberately no UpsertFileHistory.

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf, StaleByGitAfterDays: 365}
	findings, err := st.findStaleByGit(opts)
	if err != nil {
		t.Fatalf("findStaleByGit: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for doc with no history, got %d: %+v", len(findings), findings)
	}
}

// TestStaleByGit_ZeroTimestampExcluded: a file_history row with last_commit_at==0
// (schema default — e.g. a degenerate/partial row) is never flagged. Guards the
// timestamp trap: epoch 0 must not be read as "maximally old".
func TestStaleByGit_ZeroTimestampExcluded(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "docs/zerots.md", "docs/zerots.md")
	if err := st.UpsertFileHistory(FileHistory{
		Path: "docs/zerots.md", CommitCount: 1, AuthorCount: 1, LastCommitAt: 0,
	}); err != nil {
		t.Fatalf("UpsertFileHistory: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf, StaleByGitAfterDays: 365}
	findings, err := st.findStaleByGit(opts)
	if err != nil {
		t.Fatalf("findStaleByGit: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for zero-timestamp row, got %d: %+v", len(findings), findings)
	}
}

// TestStaleByGit_NonDocumentExcluded: a stale file_history row whose node is a
// heading (not a document) is not flagged — the finder is document-scoped.
func TestStaleByGit_NonDocumentExcluded(t *testing.T) {
	st := newTestStore(t)
	if err := st.InsertNodes([]Node{{
		ID: "docs/h.md#intro", Kind: "heading", Name: "Intro", QualifiedName: "docs/h.md#intro",
		FilePath: "docs/h.md", StartLine: 1, EndLine: 5, UpdatedAt: 1,
	}}); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	if err := st.UpsertFileHistory(FileHistory{
		Path: "docs/h.md#intro", CommitCount: 5, AuthorCount: 2, LastCommitAt: gitStaleTs,
	}); err != nil {
		t.Fatalf("UpsertFileHistory: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf, StaleByGitAfterDays: 365}
	findings, err := st.findStaleByGit(opts)
	if err != nil {
		t.Fatalf("findStaleByGit: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for non-document node, got %d: %+v", len(findings), findings)
	}
}

// TestStaleByGit_BoundaryExact: pins the strict '<' cutoff. A doc whose last
// commit is exactly AT the cutoff second is fresh (not flagged); one second
// earlier is stale. Kills the '<' vs '<=' boundary mutant the far-from-cutoff
// fixtures leave alive.
func TestStaleByGit_BoundaryExact(t *testing.T) {
	st := newTestStore(t)
	// Mirror findStaleByGit's cutoff arithmetic exactly.
	cutoff := staleTestAsOf.AddDate(0, 0, -365).Unix()

	insertTestNode(t, st, "docs/at-cutoff.md", "docs/at-cutoff.md")
	if err := st.UpsertFileHistory(FileHistory{
		Path: "docs/at-cutoff.md", CommitCount: 1, AuthorCount: 1, LastCommitAt: cutoff,
	}); err != nil {
		t.Fatalf("UpsertFileHistory at-cutoff: %v", err)
	}
	insertTestNode(t, st, "docs/just-before.md", "docs/just-before.md")
	if err := st.UpsertFileHistory(FileHistory{
		Path: "docs/just-before.md", CommitCount: 1, AuthorCount: 1, LastCommitAt: cutoff - 1,
	}); err != nil {
		t.Fatalf("UpsertFileHistory just-before: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf, StaleByGitAfterDays: 365}
	findings, err := st.findStaleByGit(opts)
	if err != nil {
		t.Fatalf("findStaleByGit: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 finding (only the row strictly before cutoff), got %d: %+v", len(findings), findings)
	}
	if findings[0].FilePath != "docs/just-before.md" {
		t.Fatalf("expected the just-before-cutoff doc flagged, got %q — the at-cutoff doc must be fresh under strict '<'", findings[0].FilePath)
	}
}

// TestStaleByGit_Limit: limit=1 over two stale docs returns exactly 1 finding.
func TestStaleByGit_Limit(t *testing.T) {
	st := newTestStore(t)
	for _, id := range []string{"docs/a.md", "docs/b.md"} {
		insertTestNode(t, st, id, id)
		if err := st.UpsertFileHistory(FileHistory{
			Path: id, CommitCount: 1, AuthorCount: 1, LastCommitAt: gitStaleTs,
		}); err != nil {
			t.Fatalf("UpsertFileHistory(%s): %v", id, err)
		}
	}

	opts := DriftAuditOpts{Limit: 1, AsOf: staleTestAsOf, StaleByGitAfterDays: 365}
	findings, err := st.findStaleByGit(opts)
	if err != nil {
		t.Fatalf("findStaleByGit: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding with Limit=1, got %d", len(findings))
	}
}

// TestStaleByGit_DefaultThreshold: routed through GetDriftFindings with
// StaleByGitAfterDays unset (0), the default 365 is applied and the stale doc is
// flagged. Proves the default wiring, not just the direct finder.
func TestStaleByGit_DefaultThreshold(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "docs/defaulted.md", "docs/defaulted.md")
	if err := st.UpsertFileHistory(FileHistory{
		Path: "docs/defaulted.md", CommitCount: 4, AuthorCount: 2, LastCommitAt: gitStaleTs,
	}); err != nil {
		t.Fatalf("UpsertFileHistory: %v", err)
	}

	// StaleByGitAfterDays intentionally omitted → default 365 applied inside.
	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf}
	findings, err := st.GetDriftFindings(opts)
	if err != nil {
		t.Fatalf("GetDriftFindings: %v", err)
	}

	var count int
	for _, f := range findings {
		if f.Code == CodeStaleByGit && f.FilePath == "docs/defaulted.md" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 doc.stale_by_git finding via default threshold, got %d: %+v", count, findings)
	}
}

// TestGetDriftFindings_StaleByGit_RecentNotFlagged: a recently-committed doc is
// not flagged through the public API either — the negative case at integration.
func TestGetDriftFindings_StaleByGit_RecentNotFlagged(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "docs/fresh.md", "docs/fresh.md")
	if err := st.UpsertFileHistory(FileHistory{
		Path: "docs/fresh.md", CommitCount: 9, AuthorCount: 3, LastCommitAt: gitRecentTs,
	}); err != nil {
		t.Fatalf("UpsertFileHistory: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf}
	findings, err := st.GetDriftFindings(opts)
	if err != nil {
		t.Fatalf("GetDriftFindings: %v", err)
	}
	for _, f := range findings {
		if f.Code == CodeStaleByGit {
			t.Fatalf("expected no doc.stale_by_git finding for a recent commit, got %+v", f)
		}
	}
}

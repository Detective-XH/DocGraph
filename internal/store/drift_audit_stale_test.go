package store

import (
	"testing"
	"time"
)

// asOf is a fixed reference date used across all stale-audit tests.
var staleTestAsOf = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// govTuples is a convenience builder for governance MetadataTuple slices.
func govTuples(kv ...string) []MetadataTuple {
	if len(kv)%2 != 0 {
		panic("govTuples: odd number of arguments")
	}
	out := make([]MetadataTuple, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		out = append(out, MetadataTuple{
			Key:       kv[i],
			Value:     kv[i+1],
			ValueType: "string",
			Source:    "frontmatter",
		})
	}
	return out
}

// ---- findStaleReview ----

// TestStaleReview_Positive: approved doc with review_due in the past → one finding.
func TestStaleReview_Positive(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "policy/approved.md", "policy/approved.md")

	if err := st.UpsertGovernanceMetadata("policy/approved.md", govTuples(
		"status", "approved",
		"owner", "alice",
		"review_due", "2020-01-01",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf}
	findings, err := st.findStaleReview(opts)
	if err != nil {
		t.Fatalf("findStaleReview: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Code != CodePolicyStaleReview {
		t.Errorf("Code = %q, want %q", f.Code, CodePolicyStaleReview)
	}
	if f.NodeID != "policy/approved.md" {
		t.Errorf("NodeID = %q, want policy/approved.md", f.NodeID)
	}
	if f.Severity != "warning" {
		t.Errorf("Severity = %q, want warning", f.Severity)
	}
	if f.Message == "" {
		t.Error("Message must not be empty")
	}
	if f.Evidence == "" {
		t.Error("Evidence must not be empty")
	}
}

// TestStaleReview_ArchivedExcluded: archived doc with stale review_due → no finding.
func TestStaleReview_ArchivedExcluded(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "policy/archived.md", "policy/archived.md")

	if err := st.UpsertGovernanceMetadata("policy/archived.md", govTuples(
		"status", "archived",
		"owner", "bob",
		"review_due", "2020-01-01",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf}
	findings, err := st.findStaleReview(opts)
	if err != nil {
		t.Fatalf("findStaleReview: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for archived doc, got %d: %+v", len(findings), findings)
	}
}

// TestStaleReview_SupersededExcluded: superseded doc with stale review_due → no finding.
func TestStaleReview_SupersededExcluded(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "policy/superseded.md", "policy/superseded.md")

	if err := st.UpsertGovernanceMetadata("policy/superseded.md", govTuples(
		"status", "superseded",
		"owner", "carol",
		"review_due", "2020-01-01",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf}
	findings, err := st.findStaleReview(opts)
	if err != nil {
		t.Fatalf("findStaleReview: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for superseded doc, got %d: %+v", len(findings), findings)
	}
}

// TestStaleReview_NonBindingExcluded: non-binding doc with stale review_due → no finding.
func TestStaleReview_NonBindingExcluded(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "policy/nonbinding.md", "policy/nonbinding.md")

	if err := st.UpsertGovernanceMetadata("policy/nonbinding.md", govTuples(
		"status", "non-binding",
		"owner", "dave",
		"review_due", "2020-01-01",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf}
	findings, err := st.findStaleReview(opts)
	if err != nil {
		t.Fatalf("findStaleReview: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for non-binding doc, got %d: %+v", len(findings), findings)
	}
}

// TestStaleReview_FutureReviewDue: approved doc with review_due in the future → no finding.
func TestStaleReview_FutureReviewDue(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "policy/future.md", "policy/future.md")

	if err := st.UpsertGovernanceMetadata("policy/future.md", govTuples(
		"status", "approved",
		"owner", "eve",
		"review_due", "2030-01-01",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf}
	findings, err := st.findStaleReview(opts)
	if err != nil {
		t.Fatalf("findStaleReview: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for future review_due, got %d: %+v", len(findings), findings)
	}
}

// TestStaleReview_NoReviewDue: approved doc with no review_due set → no finding.
func TestStaleReview_NoReviewDue(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "policy/nodue.md", "policy/nodue.md")

	if err := st.UpsertGovernanceMetadata("policy/nodue.md", govTuples(
		"status", "approved",
		"owner", "frank",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf}
	findings, err := st.findStaleReview(opts)
	if err != nil {
		t.Fatalf("findStaleReview: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for doc with no review_due, got %d: %+v", len(findings), findings)
	}
}

// TestStaleReview_Limit: limit=1 on two matching docs returns exactly 1 finding.
func TestStaleReview_Limit(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "policy/a.md", "policy/a.md")
	insertTestNode(t, st, "policy/b.md", "policy/b.md")

	for _, id := range []string{"policy/a.md", "policy/b.md"} {
		if err := st.UpsertGovernanceMetadata(id, govTuples(
			"status", "approved",
			"owner", "owner",
			"review_due", "2020-06-01",
		)); err != nil {
			t.Fatalf("UpsertGovernanceMetadata(%s): %v", id, err)
		}
	}

	opts := DriftAuditOpts{Limit: 1, AsOf: staleTestAsOf}
	findings, err := st.findStaleReview(opts)
	if err != nil {
		t.Fatalf("findStaleReview: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding with Limit=1, got %d", len(findings))
	}
}

// ---- findSupersededReferenced ----

// TestSupersededReferenced_Positive: superseded doc with an incoming ref from
// an approved (active) doc → one finding.
func TestSupersededReferenced_Positive(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "policy/old.md", "policy/old.md")
	insertTestNode(t, st, "policy/current.md", "policy/current.md")

	// old.md is superseded.
	if err := st.UpsertGovernanceMetadata("policy/old.md", govTuples(
		"status", "superseded",
		"superseded_by", "policy/new.md",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata old.md: %v", err)
	}
	// current.md is active (approved).
	if err := st.UpsertGovernanceMetadata("policy/current.md", govTuples(
		"status", "approved",
		"owner", "alice",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata current.md: %v", err)
	}

	// current.md references old.md.
	if err := st.InsertEdges([]Edge{
		{Source: "policy/current.md", Target: "policy/old.md", Kind: "references"},
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf}
	findings, err := st.findSupersededReferenced(opts)
	if err != nil {
		t.Fatalf("findSupersededReferenced: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Code != CodePolicySupersedeReferenced {
		t.Errorf("Code = %q, want %q", f.Code, CodePolicySupersedeReferenced)
	}
	if f.NodeID != "policy/old.md" {
		t.Errorf("NodeID = %q, want policy/old.md", f.NodeID)
	}
	if f.RelatedNodeID != "policy/current.md" {
		t.Errorf("RelatedNodeID = %q, want policy/current.md", f.RelatedNodeID)
	}
	if f.Severity != "warning" {
		t.Errorf("Severity = %q, want warning", f.Severity)
	}
	if f.Evidence == "" {
		t.Error("Evidence must not be empty")
	}
}

// TestSupersededReferenced_RefFromArchivedExcluded: superseded doc with an
// incoming ref only from an archived source → no finding (archived = inactive).
func TestSupersededReferenced_RefFromArchivedExcluded(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "policy/old2.md", "policy/old2.md")
	insertTestNode(t, st, "policy/also-old.md", "policy/also-old.md")

	if err := st.UpsertGovernanceMetadata("policy/old2.md", govTuples(
		"status", "superseded",
		"superseded_by", "policy/new2.md",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata old2.md: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("policy/also-old.md", govTuples(
		"status", "archived",
		"owner", "bob",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata also-old.md: %v", err)
	}

	// The only incoming reference is from an archived doc.
	if err := st.InsertEdges([]Edge{
		{Source: "policy/also-old.md", Target: "policy/old2.md", Kind: "references"},
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf}
	findings, err := st.findSupersededReferenced(opts)
	if err != nil {
		t.Fatalf("findSupersededReferenced: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings (ref from archived), got %d: %+v", len(findings), findings)
	}
}

// TestSupersededReferenced_RefFromSupersededExcluded: superseded doc with an
// incoming ref only from another superseded source → no finding.
func TestSupersededReferenced_RefFromSupersededExcluded(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "policy/old3.md", "policy/old3.md")
	insertTestNode(t, st, "policy/old3-referer.md", "policy/old3-referer.md")

	if err := st.UpsertGovernanceMetadata("policy/old3.md", govTuples(
		"status", "superseded",
		"superseded_by", "policy/new3.md",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata old3.md: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("policy/old3-referer.md", govTuples(
		"status", "superseded",
		"superseded_by", "policy/other.md",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata old3-referer.md: %v", err)
	}

	if err := st.InsertEdges([]Edge{
		{Source: "policy/old3-referer.md", Target: "policy/old3.md", Kind: "wikilinks_to"},
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf}
	findings, err := st.findSupersededReferenced(opts)
	if err != nil {
		t.Fatalf("findSupersededReferenced: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings (ref from superseded), got %d: %+v", len(findings), findings)
	}
}

// TestSupersededReferenced_NoGovernanceOnSource: superseded doc referenced by
// a doc with no governance record at all → finding emitted (no record = active).
func TestSupersededReferenced_NoGovernanceOnSource(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "policy/old4.md", "policy/old4.md")
	insertTestNode(t, st, "policy/untracked.md", "policy/untracked.md")

	if err := st.UpsertGovernanceMetadata("policy/old4.md", govTuples(
		"status", "superseded",
		"superseded_by", "policy/new4.md",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata old4.md: %v", err)
	}
	// No governance metadata for untracked.md — it has no record at all.

	if err := st.InsertEdges([]Edge{
		{Source: "policy/untracked.md", Target: "policy/old4.md", Kind: "related_to"},
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf}
	findings, err := st.findSupersededReferenced(opts)
	if err != nil {
		t.Fatalf("findSupersededReferenced: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding (source has no governance record), got %d: %+v", len(findings), findings)
	}
}

// TestSupersededReferenced_EmbedsKindExcluded: superseded doc with an incoming
// 'embeds' edge (not in allowed kinds) from an active doc → no finding.
func TestSupersededReferenced_EmbedsKindExcluded(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "policy/old5.md", "policy/old5.md")
	insertTestNode(t, st, "policy/embedder.md", "policy/embedder.md")

	if err := st.UpsertGovernanceMetadata("policy/old5.md", govTuples(
		"status", "superseded",
		"superseded_by", "policy/new5.md",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata old5.md: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("policy/embedder.md", govTuples(
		"status", "approved",
		"owner", "carol",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata embedder.md: %v", err)
	}

	// Only an 'embeds' edge — not in the allowed kinds list.
	if err := st.InsertEdges([]Edge{
		{Source: "policy/embedder.md", Target: "policy/old5.md", Kind: "embeds"},
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf}
	findings, err := st.findSupersededReferenced(opts)
	if err != nil {
		t.Fatalf("findSupersededReferenced: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings (embeds edge excluded), got %d: %+v", len(findings), findings)
	}
}

// TestSupersededReferenced_MultiEdgeDedup: two edges (references + wikilinks_to)
// between the same source and superseded doc → only one finding (dedup by pair).
func TestSupersededReferenced_MultiEdgeDedup(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "policy/old6.md", "policy/old6.md")
	insertTestNode(t, st, "policy/active6.md", "policy/active6.md")

	if err := st.UpsertGovernanceMetadata("policy/old6.md", govTuples(
		"status", "superseded",
		"superseded_by", "policy/new6.md",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata old6.md: %v", err)
	}
	if err := st.UpsertGovernanceMetadata("policy/active6.md", govTuples(
		"status", "approved",
		"owner", "dave",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata active6.md: %v", err)
	}

	// Two different edge kinds from the same source.
	if err := st.InsertEdges([]Edge{
		{Source: "policy/active6.md", Target: "policy/old6.md", Kind: "references"},
		{Source: "policy/active6.md", Target: "policy/old6.md", Kind: "wikilinks_to"},
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	opts := DriftAuditOpts{Limit: 100, AsOf: staleTestAsOf}
	findings, err := st.findSupersededReferenced(opts)
	if err != nil {
		t.Fatalf("findSupersededReferenced: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding (dedup), got %d: %+v", len(findings), findings)
	}
}

// TestGetDriftFindings_Integration: smoke test for GetDriftFindings routing through
// findStaleReview and findSupersededReferenced via the public API.
func TestGetDriftFindings_Integration(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "policy/stale.md", "policy/stale.md")
	insertTestNode(t, st, "policy/superseded-doc.md", "policy/superseded-doc.md")
	insertTestNode(t, st, "policy/active.md", "policy/active.md")

	// stale.md: approved with past review_due → stale_review finding
	if err := st.UpsertGovernanceMetadata("policy/stale.md", govTuples(
		"status", "approved",
		"owner", "alice",
		"review_due", "2020-03-01",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata stale.md: %v", err)
	}
	// superseded-doc.md: superseded_by set
	if err := st.UpsertGovernanceMetadata("policy/superseded-doc.md", govTuples(
		"status", "superseded",
		"superseded_by", "policy/new.md",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata superseded-doc.md: %v", err)
	}
	// active.md references superseded-doc.md
	if err := st.UpsertGovernanceMetadata("policy/active.md", govTuples(
		"status", "approved",
		"owner", "bob",
	)); err != nil {
		t.Fatalf("UpsertGovernanceMetadata active.md: %v", err)
	}
	if err := st.InsertEdges([]Edge{
		{Source: "policy/active.md", Target: "policy/superseded-doc.md", Kind: "references"},
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	opts := DriftAuditOpts{
		Limit: 100,
		AsOf:  staleTestAsOf,
	}
	findings, err := st.GetDriftFindings(opts)
	if err != nil {
		t.Fatalf("GetDriftFindings: %v", err)
	}

	var staleCount, supersededCount int
	for _, f := range findings {
		switch f.Code {
		case CodePolicyStaleReview:
			staleCount++
		case CodePolicySupersedeReferenced:
			supersededCount++
		}
	}
	if staleCount < 1 {
		t.Errorf("expected at least 1 stale_review finding, got %d", staleCount)
	}
	if supersededCount < 1 {
		t.Errorf("expected at least 1 superseded_referenced finding, got %d", supersededCount)
	}
}

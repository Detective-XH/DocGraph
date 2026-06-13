package store

import (
	"strings"
	"testing"
	"time"
)

// TestMetadataQualityExemptsBinaryDocs verifies that PDF/DOCX document nodes —
// which structurally cannot carry frontmatter — are NOT flagged for the governance
// fields they can never hold, while a frontmatter-less Markdown doc still is.
func TestMetadataQualityExemptsBinaryDocs(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "fixture.pdf", "fixture.pdf")
	insertTestNode(t, st, "report.docx", "report.docx")
	insertTestNode(t, st, "notes.md", "notes.md")

	asOf := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	structural := []string{"missing_status", "missing_owner", "missing_review_due"}

	for _, bin := range []string{"fixture.pdf", "report.docx"} {
		q, err := st.GetMetadataQuality(bin, asOf)
		if err != nil {
			t.Fatalf("GetMetadataQuality(%s): %v", bin, err)
		}
		for _, code := range structural {
			if qualityHasIssue(q, code) {
				t.Errorf("binary %s should be exempt from %q, got %+v", bin, code, q.Issues)
			}
		}
	}

	md, err := st.GetMetadataQuality("notes.md", asOf)
	if err != nil {
		t.Fatalf("GetMetadataQuality(md): %v", err)
	}
	for _, code := range structural {
		if !qualityHasIssue(md, code) {
			t.Errorf("frontmatter-less Markdown should still report %q, got %+v", code, md.Issues)
		}
	}
}

// TestMetadataQualityIssuesCarryRemediation pins that every quality issue ships an
// actionable fix (the AX gap: a finding with no path to resolution).
func TestMetadataQualityIssuesCarryRemediation(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "notes.md", "notes.md")
	q, err := st.GetMetadataQuality("notes.md", time.Time{})
	if err != nil {
		t.Fatalf("GetMetadataQuality: %v", err)
	}
	if len(q.Issues) == 0 {
		t.Fatal("expected issues for a frontmatter-less doc")
	}
	for _, iss := range q.Issues {
		if strings.TrimSpace(iss.Remediation) == "" {
			t.Errorf("issue %q has no remediation", iss.Code)
		}
	}
}

// TestDriftFindingsCarryRemediation pins that drift findings ship an actionable fix.
func TestDriftFindingsCarryRemediation(t *testing.T) {
	st := newTestStore(t)
	insertTestNode(t, st, "policy.md", "policy.md")
	if err := st.UpsertGovernanceMetadata("policy.md", []MetadataTuple{
		{Key: "status", Value: "approved", ValueType: "string", Source: "frontmatter"},
		{Key: "review_due", Value: "2020-01-01", ValueType: "date", Source: "frontmatter"},
	}); err != nil {
		t.Fatalf("UpsertGovernanceMetadata: %v", err)
	}
	findings, err := st.GetDriftFindings(DriftAuditOpts{AsOf: time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("GetDriftFindings: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected at least one drift finding")
	}
	for _, f := range findings {
		if strings.TrimSpace(f.Remediation) == "" {
			t.Errorf("finding %q has no remediation", f.Code)
		}
	}
}

package tools

import (
	"errors"
	"strings"
	"testing"

	"github.com/Detective-XH/docgraph/internal/store"
)

// mockDriftAuditor returns canned results, proving the drift consumers depend
// only on the DriftAuditor interface — drift rendering is now unit-testable
// with no real *store.Store, no SQLite, no on-disk index. That decoupling is
// the payoff of extracting the interface at the consumer.
type mockDriftAuditor struct {
	findings []store.DriftFinding
	err      error
	calls    int
}

func (m *mockDriftAuditor) GetDriftFindings(store.DriftAuditOpts) ([]store.DriftFinding, error) {
	m.calls++
	return m.findings, m.err
}

var _ DriftAuditor = (*mockDriftAuditor)(nil)

func TestDriftSummaryFor(t *testing.T) {
	t.Run("summarizes findings", func(t *testing.T) {
		m := &mockDriftAuditor{findings: []store.DriftFinding{
			{Code: "policy.stale_review", Severity: "warning"},
			{Code: "policy.stale_review", Severity: "warning"},
			{Code: "research.unverified_evidence", Severity: "error"},
		}}
		summary, ok := driftSummaryFor(m)
		if !ok {
			t.Fatal("expected ok for non-empty findings")
		}
		if summary.TotalFindings != 3 {
			t.Fatalf("TotalFindings = %d, want 3", summary.TotalFindings)
		}
		if summary.BySeverity["warning"] != 2 || summary.BySeverity["error"] != 1 {
			t.Fatalf("BySeverity = %v, want warning:2 error:1", summary.BySeverity)
		}
		if summary.ByCode["policy.stale_review"] != 2 {
			t.Fatalf("ByCode[policy.stale_review] = %d, want 2", summary.ByCode["policy.stale_review"])
		}
	})

	t.Run("no findings -> not ok", func(t *testing.T) {
		if _, ok := driftSummaryFor(&mockDriftAuditor{}); ok {
			t.Fatal("expected ok=false for empty findings")
		}
	})

	t.Run("error -> not ok", func(t *testing.T) {
		if _, ok := driftSummaryFor(&mockDriftAuditor{err: errors.New("boom")}); ok {
			t.Fatal("expected ok=false on auditor error")
		}
	})
}

func TestAppendDriftAuditStats_WithMock(t *testing.T) {
	t.Run("renders summary from mock", func(t *testing.T) {
		m := &mockDriftAuditor{findings: []store.DriftFinding{
			{Code: "policy.stale_review", Severity: "warning"},
			{Code: "research.unverified_evidence", Severity: "error"},
		}}
		var sb strings.Builder
		appendDriftAuditStats(&sb, m)
		if m.calls != 1 {
			t.Fatalf("expected exactly 1 GetDriftFindings call, got %d", m.calls)
		}
		out := sb.String()
		for _, want := range []string{"### Drift Audit", "Total findings: 2", "Errors: 1", "Warnings: 1", "policy.stale_review: 1"} {
			if !strings.Contains(out, want) {
				t.Fatalf("output missing %q\n--- output ---\n%s", want, out)
			}
		}
	})

	t.Run("clean project -> no section", func(t *testing.T) {
		var sb strings.Builder
		appendDriftAuditStats(&sb, &mockDriftAuditor{})
		if sb.Len() != 0 {
			t.Fatalf("expected empty output for no findings, got %q", sb.String())
		}
	})

	t.Run("auditor error -> no section", func(t *testing.T) {
		var sb strings.Builder
		appendDriftAuditStats(&sb, &mockDriftAuditor{err: errors.New("db down")})
		if sb.Len() != 0 {
			t.Fatalf("expected empty output on error, got %q", sb.String())
		}
	})

	t.Run("nil auditor -> no panic, no output", func(t *testing.T) {
		var sb strings.Builder
		appendDriftAuditStats(&sb, nil)
		if sb.Len() != 0 {
			t.Fatalf("expected empty output for nil auditor, got %q", sb.String())
		}
	})
}

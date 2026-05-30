package tools

import "github.com/Detective-XH/docgraph/internal/store"

// DriftAuditor is the drift-audit surface the tools layer consumes from a store.
//
// GetDriftFindings is the single exported drift entry point — the ~20
// find*/conflicting* helpers are package-private to internal/store and hide
// behind it — so drift consumers depend on this narrow interface instead of the
// whole *store.Store. That keeps the dependency honest (drift rendering needs
// nothing else from a store) and lets the rendering be unit-tested against a
// mock. *store.Store satisfies it; in workspace mode each project's *Store does.
type DriftAuditor interface {
	GetDriftFindings(opts store.DriftAuditOpts) ([]store.DriftFinding, error)
}

var _ DriftAuditor = (*store.Store)(nil)

// driftSummaryFor fetches and summarizes one auditor's drift findings. ok is
// false when the auditor errors or has no findings, so callers can omit the
// section entirely (a clean project adds no noise to status output).
func driftSummaryFor(auditor DriftAuditor) (store.DriftAuditStats, bool) {
	findings, err := auditor.GetDriftFindings(store.DriftAuditOpts{})
	if err != nil || len(findings) == 0 {
		return store.DriftAuditStats{}, false
	}
	return store.SummarizeDriftFindings(findings), true
}

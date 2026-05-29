package store

import (
	"math"
	"testing"
)

// TestClampDriftLimit locks the drift-audit findings-limit normalization that
// every sibling finder's LIMIT ? clause funnels through (see GetDriftFindings).
// A non-positive value defaults to 100; anything above maxDriftLimit is capped.
// This is the structural bound that keeps a caller-supplied (potentially
// untrusted, future MCP-wired) Limit from reaching SQLite unbounded — same
// rationale as getIntArgClamped / similarity.maxTargetsPerDoc.
func TestClampDriftLimit(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero defaults to 100", 0, 100},
		{"negative defaults to 100", -5, 100},
		{"in-range passes through", 250, 250},
		{"exactly max passes through", maxDriftLimit, maxDriftLimit},
		{"above max is capped", maxDriftLimit + 1, maxDriftLimit},
		{"MaxInt is capped", math.MaxInt, maxDriftLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampDriftLimit(tc.in); got != tc.want {
				t.Errorf("clampDriftLimit(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestGetDriftFindingsClampsExtremeLimit confirms the clamp is wired into the
// public entry point: a pathological Limit must flow through clampDriftLimit and
// not error out on an empty store (proving the bounded value reaches every
// sub-finder's LIMIT ? without surprise).
func TestGetDriftFindingsClampsExtremeLimit(t *testing.T) {
	st := tempStore(t)
	if _, err := st.GetDriftFindings(DriftAuditOpts{Limit: math.MaxInt}); err != nil {
		t.Fatalf("GetDriftFindings with extreme Limit: %v", err)
	}
}

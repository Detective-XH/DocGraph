package store

import (
	"math"
	"testing"
)

// TestFtsRankBoostTracksBm25: the boost must track bm25 magnitude, not saturate
// to a flat value. Before de-saturation every real hit (any rank < 0) returned
// a flat +8, so full-text relevance never affected final order.
func TestFtsRankBoostTracksBm25(t *testing.T) {
	// Two distinct, realistic bm25 ranks must yield two distinct boosts.
	weak := ftsRankBoost(-2.0)
	strong := ftsRankBoost(-5.0)
	if weak == strong {
		t.Fatalf("boost saturated: rank -2 and -5 both gave %v (bm25 discarded)", weak)
	}

	// More-relevant (more-negative bm25) must score higher — monotonic.
	if strong <= weak {
		t.Fatalf("boost not monotonic in relevance: rank -5 gave %v, rank -2 gave %v", strong, weak)
	}

	// Below the cap, the boost equals the bm25 magnitude (scale 1:1).
	if got := ftsRankBoost(-2.0); math.Abs(got-2.0) > 1e-9 {
		t.Fatalf("sub-cap boost should equal -rank: got %v, want 2.0", got)
	}
}

// TestFtsRankBoostCap: runaway bm25 magnitudes clamp at the cap so FTS can never
// dominate the Go-side field/graph signals.
func TestFtsRankBoostCap(t *testing.T) {
	if got := ftsRankBoost(-1000.0); math.Abs(got-ftsBoostCap) > 1e-9 {
		t.Fatalf("large bm25 magnitude should clamp at cap %v, got %v", ftsBoostCap, got)
	}
	// A hit exactly at the cap magnitude lands on the cap.
	if got := ftsRankBoost(-ftsBoostCap); math.Abs(got-ftsBoostCap) > 1e-9 {
		t.Fatalf("rank -%v should give cap %v, got %v", ftsBoostCap, ftsBoostCap, got)
	}
}

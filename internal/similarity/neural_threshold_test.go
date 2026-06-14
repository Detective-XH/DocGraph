package similarity

import (
	"math"
	"testing"
)

// TestResolveNeuralThreshold locks the threshold-resolution semantics that a
// gocyclo refactor (extracting resolveNeuralThreshold out of
// ComputeNeuralSimilarityForDoc) must preserve byte-for-byte. The NaN case is
// the subtle one: a stored "NaN" parses successfully and is NOT <= 0, so it
// propagates unchanged — every later `score < NaN` comparison is false and no
// neural edges are created. The pre-refactor code behaved this way; this test
// guards against a future "v > 0" tightening silently turning NaN into 0.25.
func TestResolveNeuralThreshold(t *testing.T) {
	t.Run("incoming positive is returned unchanged without consulting meta", func(t *testing.T) {
		st := setupSimilarityStore(t)
		// Even with a conflicting stored value, a positive incoming wins.
		if err := st.UpsertProjectMeta("similarity_threshold", "0.9"); err != nil {
			t.Fatal(err)
		}
		if got := resolveNeuralThreshold(st, 0.5); got != 0.5 {
			t.Fatalf("incoming 0.5: got %v, want 0.5", got)
		}
	})

	t.Run("absent stored meta falls back to 0.25", func(t *testing.T) {
		st := setupSimilarityStore(t)
		if got := resolveNeuralThreshold(st, 0); got != 0.25 {
			t.Fatalf("absent meta: got %v, want 0.25", got)
		}
	})

	cases := []struct {
		name   string
		stored string
		want   float64
		isNaN  bool
	}{
		{"valid positive stored", "0.5", 0.5, false},
		{"zero stored falls back", "0", 0.25, false},
		{"negative stored falls back", "-1", 0.25, false},
		{"unparseable stored falls back", "abc", 0.25, false},
		{"NaN stored propagates (no fallback)", "NaN", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := setupSimilarityStore(t)
			if err := st.UpsertProjectMeta("similarity_threshold", tc.stored); err != nil {
				t.Fatal(err)
			}
			got := resolveNeuralThreshold(st, 0)
			if tc.isNaN {
				if !math.IsNaN(got) {
					t.Fatalf("stored %q: got %v, want NaN", tc.stored, got)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("stored %q: got %v, want %v", tc.stored, got, tc.want)
			}
		})
	}
}

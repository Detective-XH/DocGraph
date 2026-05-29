package tools

import "testing"

func TestGetIntArgClamped(t *testing.T) {
	cases := []struct {
		name        string
		val         any
		def, lo, hi int
		want        int
	}{
		{"missing uses default", nil, 10, 1, 200, 10},
		{"negative clamps to lo", float64(-1), 10, 1, 200, 1},
		{"zero clamps to lo when lo=1", float64(0), 10, 1, 200, 1},
		{"zero preserved when lo=0", float64(0), 50, 0, maxListLimit, 0},
		{"in range passes through", float64(42), 10, 1, 200, 42},
		{"above hi clamps to hi", float64(1e9), 10, 1, 200, 200},
		{"wrong type uses default", "not-a-number", 7, 1, 200, 7},
		{"int kind in range", 5, 10, 1, 200, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{}
			if tc.val != nil {
				args["k"] = tc.val
			}
			got := getIntArgClamped(args, "k", tc.def, tc.lo, tc.hi)
			if got != tc.want {
				t.Fatalf("getIntArgClamped(%v, def=%d, lo=%d, hi=%d) = %d; want %d",
					tc.val, tc.def, tc.lo, tc.hi, got, tc.want)
			}
		})
	}
}

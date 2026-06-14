package tools

import "testing"

// These tests cover the MCP argument helpers that lacked direct unit coverage
// (getIntArgClamped is exercised separately in tools_util_clamp_test.go). The
// security-relevant contract is type-confusion safety: a wrong-typed or absent
// argument must fall back to the default rather than reaching a sink, and string
// arguments must be length-bounded before use.

func TestGetStringArg(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		def  string
		want string
	}{
		{"present", map[string]any{"k": "v"}, "d", "v"},
		{"present empty", map[string]any{"k": ""}, "d", ""},
		{"missing key", map[string]any{}, "d", "d"},
		{"nil value", map[string]any{"k": nil}, "d", "d"},
		{"wrong type int", map[string]any{"k": 5}, "d", "d"},
		{"wrong type bool", map[string]any{"k": true}, "d", "d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := getStringArg(tc.args, "k", tc.def); got != tc.want {
				t.Fatalf("getStringArg(%v, def=%q) = %q; want %q", tc.args, tc.def, got, tc.want)
			}
		})
	}
}

func TestGetIntArg(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		def  int
		want int
	}{
		{"float64 (JSON number)", map[string]any{"k": float64(42)}, 10, 42},
		{"float64 truncates toward zero", map[string]any{"k": float64(3.9)}, 10, 3},
		{"int kind", map[string]any{"k": 7}, 10, 7},
		{"missing key", map[string]any{}, 10, 10},
		{"nil value", map[string]any{"k": nil}, 10, 10},
		{"wrong type string", map[string]any{"k": "12"}, 10, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := getIntArg(tc.args, "k", tc.def); got != tc.want {
				t.Fatalf("getIntArg(%v, def=%d) = %d; want %d", tc.args, tc.def, got, tc.want)
			}
		})
	}
}

func TestGetBoolArg(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		def  bool
		want bool
	}{
		{"true", map[string]any{"k": true}, false, true},
		{"false", map[string]any{"k": false}, true, false},
		{"missing key", map[string]any{}, true, true},
		{"nil value", map[string]any{"k": nil}, true, true},
		// def=true so a buggy impl returning the zero value (false) for a
		// non-bool would fail — proving the default is actually returned.
		{"wrong type string", map[string]any{"k": "true"}, true, true},
		{"wrong type number", map[string]any{"k": float64(1)}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := getBoolArg(tc.args, "k", tc.def); got != tc.want {
				t.Fatalf("getBoolArg(%v, def=%t) = %t; want %t", tc.args, tc.def, got, tc.want)
			}
		})
	}
}

func TestSanitizeArg(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"under max unchanged", "hi", 5, "hi"},
		{"equal max unchanged", "hello", 5, "hello"},
		{"over max truncated", "hello world", 5, "hello"},
		{"zero max empties", "x", 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeArg(tc.in, tc.max); got != tc.want {
				t.Fatalf("sanitizeArg(%q, %d) = %q; want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"under n unchanged", "abc", 5, "abc"},
		{"equal n unchanged", "abcde", 5, "abcde"},
		{"over n appends ellipsis", "abcdefgh", 5, "ab..."},
		// Rune-aware: a byte-wise cut of this 18-byte string would split a
		// multibyte rune; truncateRunes must cut on rune boundaries.
		{"multibyte cut on rune boundary", "日本語テスト", 5, "日本..."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncateRunes(tc.in, tc.n); got != tc.want {
				t.Fatalf("truncateRunes(%q, %d) = %q; want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}

package parser

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// buildAliasBomb returns a billion-laughs-style YAML payload kept under the
// 8 KB frontmatter cap. Each level references the previous one `fanout` times,
// so total expansion is fanout^levels if the parser expands aliases eagerly
// with no limit.
func buildAliasBomb(levels, fanout int) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	// a0 is a small leaf list.
	sb.WriteString("a0: &a0 [")
	for i := 0; i < fanout; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteByte('x')
	}
	sb.WriteString("]\n")
	for l := 1; l <= levels; l++ {
		fmt.Fprintf(&sb, "a%d: &a%d [", l, l)
		for i := 0; i < fanout; i++ {
			if i > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, "*a%d", l-1)
		}
		sb.WriteString("]\n")
	}
	sb.WriteString("---\n")
	return sb.String()
}

// buildDeepNest returns deeply nested flow sequences ([[[...]]]) under 8 KB to
// probe parser recursion depth (stack-overflow DoS — fatal/unrecoverable in Go).
func buildDeepNest(depth int) string {
	return "---\nx: " + strings.Repeat("[", depth) + strings.Repeat("]", depth) + "\n---\n"
}

func runWithTimeout(t *testing.T, name string, payload string, d time.Duration) {
	t.Helper()
	t.Logf("%s: payload %d bytes", name, len(payload))
	done := make(chan struct{})
	var err error
	go func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("%s: PANIC during ExtractFrontmatter: %v", name, r)
			}
			close(done)
		}()
		_, err = ExtractFrontmatter([]byte(payload))
	}()
	select {
	case <-done:
		t.Logf("%s: returned (err=%v) — bounded, no DoS", name, err)
	case <-time.After(d):
		t.Errorf("%s: DID NOT RETURN within %v — possible expansion/recursion DoS", name, d)
	}
}

func TestYAMLAliasBombBounded(t *testing.T) {
	// 6 levels × fanout 10 = 10^6 theoretical expansion, comfortably < 8 KB.
	payload := buildAliasBomb(6, 10)
	if len(payload) > 8192 {
		t.Fatalf("seed payload %d bytes exceeds 8KB cap — adjust", len(payload))
	}
	runWithTimeout(t, "alias-bomb", payload, 10*time.Second)
}

func TestYAMLDeepNestBounded(t *testing.T) {
	// ~4000 nested levels fits in 8 KB (2 bytes/level).
	payload := buildDeepNest(4000)
	runWithTimeout(t, "deep-nest", payload, 10*time.Second)
}

// TestYAMLAliasBombFullPath probes the REAL pipeline: ParseFile calls
// FrontmatterToJSON(fm) -> json.Marshal, which expands an alias DAG into a
// tree. ExtractFrontmatter alone may share references (instant), but
// json.Marshal re-arms the billion-laughs. Measure output growth per added
// level — multiplicative growth from a sub-8KB input is the amplification.
func TestYAMLAliasBombFullPath(t *testing.T) {
	for _, levels := range []int{2, 3, 4, 5, 6} {
		payload := buildAliasBomb(levels, 10)
		if len(payload) > 8192 {
			t.Logf("levels=%d payload %d bytes exceeds 8KB — skipped", levels, len(payload))
			continue
		}
		fm, err := ExtractFrontmatter([]byte(payload))
		if err != nil {
			t.Logf("levels=%d: ExtractFrontmatter err=%v (rejected — safe)", levels, err)
			continue
		}
		js := FrontmatterToJSON(fm)
		t.Logf("levels=%d: input=%dB  fm-keys=%d  JSON-output=%dB",
			levels, len(payload), len(fm), len(js))
		// Regression guard: yaml.v3's alias budget + the 8KB frontmatter cap
		// must keep marshaled output bounded. A multi-MB output here means the
		// billion-laughs protection regressed (e.g. a yaml dependency bump).
		if len(js) > 64*1024 {
			t.Errorf("levels=%d: JSON output %dB exceeds 64KB from a %dB input — alias-bomb protection regressed",
				levels, len(js), len(payload))
		}
	}
}

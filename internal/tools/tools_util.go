package tools

import (
	"fmt"
	"unicode/utf8"

	"github.com/Detective-XH/docgraph/internal/store"
)

// ---------------------------------------------------------------------------
// MCP input length limits
// ---------------------------------------------------------------------------

const maxArgLength = 10000

// maxListLimit is a generous upper bound for "max results" arguments whose
// sink has no tighter natural cap. It is far above any realistic request, so
// it never changes legitimate behavior; its only job is to reject pathological
// values (e.g. an overflowed int from a huge JSON number).
const maxListLimit = 100000

func sanitizeArg(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}

// truncateRunes truncates s to at most n runes, appending "..." if truncated.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-3]) + "..."
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func getStringArg(args map[string]any, key string, defaultVal string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return defaultVal
	}
	s, ok := v.(string)
	if !ok {
		return defaultVal
	}
	return s
}

func getIntArg(args map[string]any, key string, defaultVal int) int {
	v, ok := args[key]
	if !ok || v == nil {
		return defaultVal
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return defaultVal
	}
}

// getIntArgClamped parses an int arg like getIntArg, then clamps the result to
// [lo, hi]. Clamping at retrieval guarantees every consumer receives a bounded
// value regardless of whether its own sink re-checks, closing the class of
// "a new int arg reaches a slice/SQL sink without a downstream guard" bugs.
// Pass lo=0 where 0 carries a dedicated "unlimited" meaning at the sink.
func getIntArgClamped(args map[string]any, key string, defaultVal, lo, hi int) int {
	n := getIntArg(args, key, defaultVal)
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

func getBoolArg(args map[string]any, key string, defaultVal bool) bool {
	v, ok := args[key]
	if !ok || v == nil {
		return defaultVal
	}
	b, ok := v.(bool)
	if !ok {
		return defaultVal
	}
	return b
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(bytes)/1024/1024)
	case bytes >= 1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func formatNodePath(n store.Node) string {
	if n.ProjectName == "" {
		return n.FilePath
	}
	return "[" + n.ProjectName + "] " + n.FilePath
}

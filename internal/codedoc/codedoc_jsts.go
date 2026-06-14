// JS/TS/Svelte/Vue code documentation extractor.
// Uses regex + line scanning only — no external dependencies.
package codedoc

import (
	"bytes"
	"regexp"
	"strings"
)

// ---- compiled regexps -------------------------------------------------------

// jsdocClose matches the closing */ of a JSDoc block.
var jsdocCloseLine = regexp.MustCompile(`\*/\s*$`)

// symbolDecl matches a function/class/const/let/var/interface/type declaration
// optionally preceded by "export" or "export default".
// Group 1: keyword, Group 2: symbol name.
var symbolDeclRe = regexp.MustCompile(
	`^\s*(?:export\s+(?:default\s+)?)?` +
		`(function\*?|class|interface|type|const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)`,
)

// arrowFuncRe matches "const/let/var NAME = (...) =>" style arrow functions.
// Group 1: symbol name.
var arrowFuncRe = regexp.MustCompile(
	`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*(?:async\s+)?\(`,
)

// testCallRe matches describe/it/test call expressions, with optional
// .only/.skip/.todo/.concurrent/.failing suffix.
// Group 1: name string (single-quoted, double-quoted, or backtick).
var testCallRe = regexp.MustCompile(
	`^\s*(?:describe|it|test)(?:\.(?:only|skip|todo|concurrent|failing))?\s*\(\s*(['"\x60])`,
)

// testEachRe matches test.each(...)('name') or it.each(...)('name') on a single line —
// the chained-call form where .each() is called inline with the test name.
// Group 1: name string quote character.
var testEachRe = regexp.MustCompile(
	`^\s*(?:describe|it|test)\.each\b[^(]*\([^)]*(?:\)[^(]*\([^)]*)*\)\s*\(\s*(['"\x60])`,
)

// testEachContinuationRe matches the `)('name', ...)` closing continuation line
// for multi-line test.each calls, e.g.:
//
//	test.each([
//	  [1, 2, 3],
//	])('adds numbers', (a, b, c) => { ... });
//
// Group 1: name string quote character.
var testEachContinuationRe = regexp.MustCompile(
	`^\s*\]\s*\)\s*\(\s*(['"\x60])`,
)

// scriptBlockRe extracts the <script ...> ... </script> block from Svelte/Vue.
// Handles <script>, <script setup>, <script lang="ts">, etc.
var scriptBlockRe = regexp.MustCompile(`(?is)<script[^>]*>([\s\S]*?)</script>`)

// ---- helpers ----------------------------------------------------------------

// isTestFile returns true if the path indicates a test file.
func isTestFile(relPath string) bool {
	base := relPath
	// __tests__/ directory component
	if strings.Contains(relPath, "/__tests__/") || strings.HasPrefix(relPath, "__tests__/") {
		return true
	}
	// .test.js .test.ts .spec.js .spec.ts .test.jsx .test.tsx .spec.jsx .spec.tsx
	for _, suffix := range []string{
		".test.js", ".test.ts", ".test.jsx", ".test.tsx",
		".spec.js", ".spec.ts", ".spec.jsx", ".spec.tsx",
	} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

// truncate60 truncates s to 60 characters for HeadingPath.
func truncate60(s string) string {
	if len(s) <= 60 {
		return s
	}
	return s[:60]
}

// jsdocBlock accumulates lines within a /** ... */ block.
type jsdocBlock struct {
	lines     []string
	startLine int // 1-based, absolute
}

// jsScanner holds the mutable state shared across the per-line handler methods.
type jsScanner struct {
	lang     string
	fileType string
	entries  []CodeDocEntry

	firstJSDocSeen bool
	inJSDoc        bool
	cur            jsdocBlock
	pending        *jsdocBlock
}

func (s *jsScanner) emit(e CodeDocEntry) {
	e.Lang = s.lang
	e.FileType = s.fileType
	s.entries = append(s.entries, e)
}

// onJSDocClose finalises a completed JSDoc block (multi- or single-line).
func (s *jsScanner) onJSDocClose(absLine int) {
	if !s.firstJSDocSeen {
		s.firstJSDocSeen = true
		s.emit(CodeDocEntry{
			CommentKind: KindFileHeader,
			HeadingPath: "File Header",
			Text:        cleanJSDoc(s.cur.lines),
			StartLine:   s.cur.startLine,
			EndLine:     absLine,
		})
		s.cur = jsdocBlock{}
		return
	}
	s.pending = &jsdocBlock{lines: s.cur.lines, startLine: s.cur.startLine}
	s.cur = jsdocBlock{}
}

// onTestLine attempts to match line against re (a test regex with a
// quote-capture group). Emits a KindTestFunc entry and returns true on match.
func (s *jsScanner) onTestLine(re *regexp.Regexp, line string, absLine int) bool {
	loc := re.FindStringSubmatchIndex(line)
	if len(loc) < 4 {
		return false
	}
	quoteChar := line[loc[2]:loc[3]]
	name := extractStringUntilClose(line[loc[3]:], quoteChar)
	if name != "" {
		s.emit(CodeDocEntry{
			SymbolName:  name,
			CommentKind: KindTestFunc,
			HeadingPath: "Tests > " + truncate60(name),
			Text:        name,
			StartLine:   absLine,
			EndLine:     absLine,
		})
	}
	s.pending = nil
	return true
}

// onDeclLine processes a non-JSDoc, non-test line that may be a symbol
// declaration (consuming a pending JSDoc) or a substantive code line
// (advancing the firstJSDocSeen heuristic). Returns true when the line was
// consumed as a declaration.
func (s *jsScanner) onDeclLine(line, trimmed string, absLine int) bool {
	if s.pending != nil {
		return s.onPendingLine(line, trimmed, absLine)
	}
	// No pending JSDoc — advance firstJSDocSeen past any substantive code line.
	if !s.firstJSDocSeen && jsSubstantiveLine(trimmed) {
		s.firstJSDocSeen = true
	}
	return false
}

// onPendingLine handles a line when there is a pending JSDoc block.
func (s *jsScanner) onPendingLine(line, trimmed string, absLine int) bool {
	if sym := jsDeclSymbol(line); sym != "" {
		s.emit(CodeDocEntry{
			SymbolName:  sym,
			CommentKind: KindDocComment,
			HeadingPath: "DocComment > " + sym,
			Text:        cleanJSDoc(s.pending.lines),
			StartLine:   s.pending.startLine,
			EndLine:     absLine,
		})
		s.pending = nil
		return true
	}
	// Non-empty, non-declaration line — discard pending if it's code.
	if trimmed != "" && !strings.HasPrefix(trimmed, "//") {
		s.pending = nil
	}
	return false
}

// jsSubstantiveLine reports whether trimmed is a non-blank, non-comment line
// that marks the end of the file-header window.
func jsSubstantiveLine(trimmed string) bool {
	return trimmed != "" &&
		!strings.HasPrefix(trimmed, "//") &&
		!strings.HasPrefix(trimmed, "/*") &&
		!strings.HasPrefix(trimmed, "*")
}

// extractFromJSBlock scans a JS/TS source buffer and returns CodeDocEntry values.
// lineOffset is the 0-based number of lines before this buffer in the original file
// (used for Svelte/Vue where we pass only the <script> content).
// lang and fileType are set on every returned entry.
func extractFromJSBlock(src []byte, lineOffset int, relPath, lang, fileType string) []CodeDocEntry {
	lines := bytes.Split(src, []byte("\n"))
	sc := &jsScanner{lang: lang, fileType: fileType}

	for i, rawLine := range lines {
		absLine := lineOffset + i + 1 // 1-based absolute line number
		line := string(rawLine)

		// -- Inside a JSDoc block --
		if sc.inJSDoc {
			sc.cur.lines = append(sc.cur.lines, line)
			if jsdocCloseLine.MatchString(line) {
				sc.inJSDoc = false
				sc.onJSDocClose(absLine)
			}
			continue
		}

		// -- Check for JSDoc open --
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "/**") {
			// "/**" followed by more content, or just "/**" alone on the line.
			sc.inJSDoc = true
			sc.cur = jsdocBlock{startLine: absLine}
			sc.cur.lines = append(sc.cur.lines, line)
			// If the open and close are on the same line: "/** text */"
			if strings.Contains(trimmed[3:], "*/") {
				sc.inJSDoc = false
				sc.onJSDocClose(absLine)
			}
			continue
		}

		// -- Test calls --
		// Try the chained .each(table)('name') pattern first (more specific).
		if sc.onTestLine(testEachRe, line, absLine) {
			continue
		}
		// Multi-line test.each continuation: ])('name', ...) on its own line.
		if sc.onTestLine(testEachContinuationRe, line, absLine) {
			continue
		}
		if sc.onTestLine(testCallRe, line, absLine) {
			continue
		}

		// -- Symbol declarations (may consume pending JSDoc) --
		if sc.onDeclLine(line, trimmed, absLine) {
			continue
		}
	}

	return sc.entries
}

// jsDeclSymbol returns the symbol name matched by symbolDeclRe or arrowFuncRe,
// or "" if neither matches.
func jsDeclSymbol(line string) string {
	if m := symbolDeclRe.FindStringSubmatch(line); m != nil {
		return m[2]
	}
	if m := arrowFuncRe.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

// cleanJSDoc strips JSDoc formatting from a block of lines and returns plain text.
func cleanJSDoc(lines []string) string {
	var out []string
	for i, l := range lines {
		s := strings.TrimSpace(l)
		// Remove opening /** or /**
		if i == 0 {
			s = strings.TrimPrefix(s, "/**")
			s = strings.TrimSpace(s)
			// Single-line: also remove trailing */
			s = strings.TrimSuffix(s, "*/")
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
			continue
		}
		// Remove closing */
		if strings.HasSuffix(s, "*/") {
			s = strings.TrimSuffix(s, "*/")
			s = strings.TrimSpace(s)
			// Remove leading * if present
			s = strings.TrimPrefix(s, "*")
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
			continue
		}
		// Interior line: remove leading " * "
		s = strings.TrimPrefix(s, "*")
		s = strings.TrimSpace(s)
		out = append(out, s)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// extractStringUntilClose reads characters from s until it hits the closing
// quote character (which may be ', ", or `). Returns the string content.
func extractStringUntilClose(s, quoteChar string) string {
	q := quoteChar[0]
	var sb strings.Builder
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			sb.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == q {
			return sb.String()
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

// scriptBlockContent extracts the content of the first <script ...> block
// and returns the content bytes plus the 0-based line offset of the first
// content line within the original file.
func scriptBlockContent(src []byte) (content []byte, lineOffset int, found bool) {
	loc := scriptBlockRe.FindSubmatchIndex(src)
	if loc == nil {
		return nil, 0, false
	}
	// loc[2]:loc[3] is the capture group (content inside <script>...</script>)
	inner := src[loc[2]:loc[3]]
	// Compute line offset: count newlines in src before loc[2]
	prefix := src[:loc[2]]
	lineOffset = bytes.Count(prefix, []byte("\n"))
	return inner, lineOffset, true
}

// ---- extractors -------------------------------------------------------------

// extractJSTS is the extractor for .js, .jsx, .ts, .tsx files.
func extractJSTS(relPath string, src []byte) ([]CodeDocEntry, error) {
	lang := "javascript"
	ext := strings.ToLower(relPath[strings.LastIndex(relPath, "."):])
	if ext == ".ts" || ext == ".tsx" {
		lang = "typescript"
	}
	ft := FileTypeSource
	if isTestFile(relPath) {
		ft = FileTypeTest
	}
	return extractFromJSBlock(src, 0, relPath, lang, ft), nil
}

// extractSvelte is the extractor for .svelte files.
func extractSvelte(relPath string, src []byte) ([]CodeDocEntry, error) {
	content, lineOffset, found := scriptBlockContent(src)
	if !found {
		// No <script> block — return empty (component with no logic).
		return nil, nil
	}
	ft := FileTypeSource
	if isTestFile(relPath) {
		ft = FileTypeTest
	}
	return extractFromJSBlock(content, lineOffset, relPath, "svelte", ft), nil
}

// extractVue is the extractor for .vue files.
func extractVue(relPath string, src []byte) ([]CodeDocEntry, error) {
	content, lineOffset, found := scriptBlockContent(src)
	if !found {
		return nil, nil
	}
	ft := FileTypeSource
	if isTestFile(relPath) {
		ft = FileTypeTest
	}
	return extractFromJSBlock(content, lineOffset, relPath, "vue", ft), nil
}

// ---- registration -----------------------------------------------------------

func init() {
	RegisterExtractor("javascript", extractJSTS, ".js", ".jsx")
	RegisterExtractor("typescript", extractJSTS, ".ts", ".tsx")
	RegisterExtractor("svelte", extractSvelte, ".svelte")
	RegisterExtractor("vue", extractVue, ".vue")
}

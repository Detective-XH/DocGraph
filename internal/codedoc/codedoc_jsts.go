// JS/TS/Svelte/Vue code documentation extractor.
// Uses regex + line scanning only — no external dependencies.
package codedoc

import (
	"bytes"
	"regexp"
	"strings"
)

// ---- compiled regexps -------------------------------------------------------

// jsdocOpen matches the opening /** of a JSDoc block (must be double asterisk).
var jsdocOpenRe = regexp.MustCompile(`^\s*/\*\*\s*$|^\s*/\*\*\s+`)

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

// extractFromJSBlock scans a JS/TS source buffer and returns CodeDocEntry values.
// lineOffset is the 0-based number of lines before this buffer in the original file
// (used for Svelte/Vue where we pass only the <script> content).
// lang and fileType are set on every returned entry.
func extractFromJSBlock(src []byte, lineOffset int, relPath, lang, fileType string) []CodeDocEntry {
	lines := bytes.Split(src, []byte("\n"))
	var entries []CodeDocEntry

	// firstJSDocSeen tracks whether we have emitted a KindFileHeader yet.
	firstJSDocSeen := false

	// jsdocBuf accumulates lines within a /** ... */ block.
	type jsdocBlock struct {
		lines     []string
		startLine int // 1-based, absolute
	}

	var pending *jsdocBlock // JSDoc block waiting for the next declaration
	inJSDoc := false
	var cur jsdocBlock

	emit := func(e CodeDocEntry) {
		e.Lang = lang
		e.FileType = fileType
		entries = append(entries, e)
	}

	flushPending := func() {
		// Discard a pending JSDoc that had no following declaration.
		pending = nil
	}

	for i, rawLine := range lines {
		absLine := lineOffset + i + 1 // 1-based absolute line number
		line := string(rawLine)

		// -- Inside a JSDoc block --
		if inJSDoc {
			cur.lines = append(cur.lines, line)
			if jsdocCloseLine.MatchString(line) {
				inJSDoc = false
				// Determine if this is a file header (first JSDoc, near top of block).
				if !firstJSDocSeen {
					firstJSDocSeen = true
					emit(CodeDocEntry{
						CommentKind: KindFileHeader,
						HeadingPath: "File Header",
						Text:        cleanJSDoc(cur.lines),
						StartLine:   cur.startLine,
						EndLine:     absLine,
					})
					cur = jsdocBlock{}
					continue
				}
				// Otherwise store as pending for the next declaration.
				// Text is computed from pending.lines when the declaration is matched.
				pending = &jsdocBlock{
					lines:     cur.lines,
					startLine: cur.startLine,
				}
				cur = jsdocBlock{}
			}
			continue
		}

		// -- Check for JSDoc open --
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "/**") {
			// Ensure it really is JSDoc (double asterisk), not a block comment "/*".
			// "/**" followed by more content, or just "/**" alone on the line.
			inJSDoc = true
			cur = jsdocBlock{startLine: absLine}
			cur.lines = append(cur.lines, line)
			// If the open and close are on the same line: "/** text */"
			if strings.Contains(trimmed[3:], "*/") {
				inJSDoc = false
				text := cleanJSDoc(cur.lines)
				if !firstJSDocSeen {
					firstJSDocSeen = true
					emit(CodeDocEntry{
						CommentKind: KindFileHeader,
						HeadingPath: "File Header",
						Text:        text,
						StartLine:   cur.startLine,
						EndLine:     absLine,
					})
					cur = jsdocBlock{}
					continue
				}
				pending = &jsdocBlock{lines: cur.lines, startLine: cur.startLine}
				cur = jsdocBlock{}
			}
			continue
		}

		// -- Test calls --
		// Try the chained .each(table)('name') pattern first (more specific).
		if loc := testEachRe.FindStringSubmatchIndex(line); loc != nil && len(loc) >= 4 {
			quoteChar := line[loc[2]:loc[3]]
			rest := line[loc[3]:]
			name := extractStringUntilClose(rest, quoteChar)
			if name != "" {
				emit(CodeDocEntry{
					SymbolName:  name,
					CommentKind: KindTestFunc,
					HeadingPath: "Tests > " + truncate60(name),
					Text:        name,
					StartLine:   absLine,
					EndLine:     absLine,
				})
			}
			flushPending()
			continue
		}
		// Multi-line test.each continuation: ])('name', ...) on its own line.
		if loc := testEachContinuationRe.FindStringSubmatchIndex(line); loc != nil && len(loc) >= 4 {
			quoteChar := line[loc[2]:loc[3]]
			rest := line[loc[3]:]
			name := extractStringUntilClose(rest, quoteChar)
			if name != "" {
				emit(CodeDocEntry{
					SymbolName:  name,
					CommentKind: KindTestFunc,
					HeadingPath: "Tests > " + truncate60(name),
					Text:        name,
					StartLine:   absLine,
					EndLine:     absLine,
				})
			}
			flushPending()
			continue
		}
		if m := testCallRe.FindStringIndex(line); m != nil {
			// Find the opening quote
			loc := testCallRe.FindStringSubmatchIndex(line)
			if loc != nil && len(loc) >= 4 {
				quoteChar := line[loc[2]:loc[3]] // the captured quote character
				rest := line[loc[3]:]            // everything after the opening quote
				name := extractStringUntilClose(rest, quoteChar)
				if name != "" {
					sym := name
					emit(CodeDocEntry{
						SymbolName:  sym,
						CommentKind: KindTestFunc,
						HeadingPath: "Tests > " + truncate60(sym),
						Text:        name,
						StartLine:   absLine,
						EndLine:     absLine,
					})
				}
			}
			flushPending()
			continue
		}

		// -- Symbol declarations (may consume pending JSDoc) --
		if pending != nil {
			sym := ""
			// Try symbolDecl
			if m := symbolDeclRe.FindStringSubmatch(line); m != nil {
				sym = m[2]
			} else if m := arrowFuncRe.FindStringSubmatch(line); m != nil {
				sym = m[1]
			}

			if sym != "" {
				text := cleanJSDoc(pending.lines)
				emit(CodeDocEntry{
					SymbolName:  sym,
					CommentKind: KindDocComment,
					HeadingPath: "DocComment > " + sym,
					Text:        text,
					StartLine:   pending.startLine,
					EndLine:     absLine,
				})
				pending = nil
				continue
			}

			// Non-empty, non-declaration line — discard pending if it's code.
			if trimmed != "" && !strings.HasPrefix(trimmed, "//") {
				flushPending()
			}
		} else {
			// No pending JSDoc — still check for declarations to advance firstJSDocSeen
			// heuristic: any non-blank, non-comment line means we're past the file top.
			if !firstJSDocSeen && trimmed != "" && !strings.HasPrefix(trimmed, "//") &&
				!strings.HasPrefix(trimmed, "/*") && !strings.HasPrefix(trimmed, "*") {
				// Any substantive code line: file header window has passed.
				firstJSDocSeen = true
			}
		}
	}

	return entries
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

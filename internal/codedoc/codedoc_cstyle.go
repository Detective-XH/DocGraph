// codedoc_cstyle.go — C-style comment extractors for Rust, C, C++, Java,
// Swift, C#, PHP, Kotlin, and Dart.
//
// All nine languages share // line-comment and /* */ block-comment syntax.
// Extraction is line-scanning / regex only — no external dependencies.
// Each language is registered via init() using makeExtractor.
package codedoc

import (
	"regexp"
	"strings"
)

// ──────────────────────────────────────────────────────────────────────────────
// Registration
// ──────────────────────────────────────────────────────────────────────────────

func init() {
	RegisterExtractor("rust", makeExtractor("rust"), ".rs")
	RegisterExtractor("c", makeExtractor("c"), ".c", ".cc", ".h")
	RegisterExtractor("cpp", makeExtractor("cpp"), ".cpp", ".cxx", ".hpp", ".hh")
	RegisterExtractor("java", makeExtractor("java"), ".java")
	RegisterExtractor("swift", makeExtractor("swift"), ".swift")
	RegisterExtractor("csharp", makeExtractor("csharp"), ".cs")
	RegisterExtractor("php", makeExtractor("php"), ".php")
	RegisterExtractor("kotlin", makeExtractor("kotlin"), ".kt", ".kts")
	RegisterExtractor("dart", makeExtractor("dart"), ".dart")
}

func makeExtractor(lang string) extractFunc {
	return func(relPath string, src []byte) ([]CodeDocEntry, error) {
		return extractCStyle(lang, relPath, src)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Per-language configuration
// ──────────────────────────────────────────────────────────────────────────────

// cStyleConfig drives the language-specific parts of extractCStyle.
type cStyleConfig struct {
	// testAttrs are annotation/attribute strings that signal a test.
	// Checked as exact trimmed-line matches after stripping outer [ ] or @.
	testAttrs []string

	// testFuncPrefixes: the function name must start with one of these
	// (case-insensitive) for the method to be considered a test when no
	// attribute is present. Empty means attributes alone decide.
	testFuncPrefixes []string

	// testFileSuffixes: path suffixes (lower-case) that mark the file as
	// FileTypeTest.
	testFileSuffixes []string

	// testDirSegments: if the relPath contains any of these path segments the
	// file is FileTypeTest. E.g. "src/test/" for Java.
	testDirSegments []string

	// classNameTestSuffixes: if the file's class name ends with one of these the
	// file is FileTypeTest. Detected on the class declaration line.
	classNameTestSuffixes []string

	// useTripleSlash: language uses /// for doc comments (Rust).
	useTripleSlash bool

	// useDoubleStarBlock: /** … */ is the idiomatic doc block (Java/Kotlin/Dart).
	useDoubleStarBlock bool

	// dartTestCall: language uses test('name', fn) style (Dart).
	dartTestCall bool

	// phpTestTag: language uses /** @test */ annotation (PHP).
	phpTestTag bool
}

var langConfigs = map[string]cStyleConfig{
	"rust": {
		useTripleSlash:   true,
		testFileSuffixes: []string{"_test.rs"},
	},
	"c": {
		testFileSuffixes: []string{},
	},
	"cpp": {
		testFileSuffixes: []string{},
	},
	"java": {
		useDoubleStarBlock:    true,
		testAttrs:             []string{"@Test", "@ParameterizedTest", "@RepeatedTest"},
		testDirSegments:       []string{"src/test/"},
		classNameTestSuffixes: []string{"Test"},
	},
	"swift": {
		testFuncPrefixes: []string{"test"},
		testFileSuffixes: []string{"tests.swift"},
	},
	"csharp": {
		testAttrs:        []string{"[Test]", "[Fact]", "[Theory]", "[TestMethod]"},
		testFileSuffixes: []string{"tests.cs"},
	},
	"php": {
		phpTestTag:       true,
		testFuncPrefixes: []string{"test"},
		testFileSuffixes: []string{"test.php"},
	},
	"kotlin": {
		useDoubleStarBlock:    true,
		testAttrs:             []string{"@Test"},
		classNameTestSuffixes: []string{"Test", "Spec"},
	},
	"dart": {
		useDoubleStarBlock: true,
		dartTestCall:       true,
		testFileSuffixes:   []string{"_test.dart"},
		testDirSegments:    []string{"test/"},
	},
}

// ──────────────────────────────────────────────────────────────────────────────
// Declaration regexps (compiled once)
// ──────────────────────────────────────────────────────────────────────────────

// Generic: fn NAME( (Rust)
var rustFuncRe = regexp.MustCompile(`(?:pub\s+(?:unsafe\s+)?)?fn\s+([A-Za-z_][A-Za-z0-9_]*)`)

// Generic: func NAME( (Swift/Kotlin)
var funcRe = regexp.MustCompile(`\bfunc\s+([A-Za-z_][A-Za-z0-9_]*)`)
var kotlinFuncRe = regexp.MustCompile(`\bfun\s+([A-Za-z_][A-Za-z0-9_]*)`)

// Java/C#/C/C++/PHP: method or function with return type + name + (
var javaMethodRe = regexp.MustCompile(
	`(?:public|private|protected|static|final|override|async|virtual|abstract|\s)+` +
		`\s+(?:\w+(?:<[^>]*>)?)\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`,
)

// class NAME (any language — for classNameTestSuffixes detection)
var classRe = regexp.MustCompile(`\bclass\s+([A-Za-z_][A-Za-z0-9_]*)`)

// C/C++: "type name(" — simplified
var cFuncRe = regexp.MustCompile(
	`^(?:static\s+|inline\s+|extern\s+)*(?:unsigned\s+)?(?:\w+(?:\s*\*)?)\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`,
)

// PHP: function NAME(
var phpFuncRe = regexp.MustCompile(`\bfunction\s+([A-Za-z_][A-Za-z0-9_]*)`)

// Dart: test('NAME', ...) and testWidgets('NAME', ...)
var dartTestCallRe = regexp.MustCompile(`^\s*test(?:Widgets)?\s*\(\s*['"]([^'"]+)['"]`)

// Rust: #[test] attribute line
var rustTestAttrRe = regexp.MustCompile(`^\s*#\[test\]`)

// Rust: #[cfg(test)] module start
var rustCfgTestRe = regexp.MustCompile(`^\s*#\[cfg\(test\)\]`)

// ──────────────────────────────────────────────────────────────────────────────
// Main extractor
// ──────────────────────────────────────────────────────────────────────────────

// extractCStyle implements the shared C-style documentation extractor.
func extractCStyle(lang, relPath string, src []byte) ([]CodeDocEntry, error) {
	cfg, ok := langConfigs[lang]
	if !ok {
		return nil, nil
	}

	lines := splitLines(src)
	fileType := detectFileType(lang, relPath, lines, cfg)

	var entries []CodeDocEntry

	// --- Phase 1: file header ---
	// The file header is the first comment block (// run or /* */ block) that
	// appears before any substantive code.
	hdr, hdrEnd := extractFileHeader(lang, lines, cfg)
	if hdr.text != "" {
		entries = append(entries, CodeDocEntry{
			CommentKind: KindFileHeader,
			HeadingPath: "File Header",
			Text:        hdr.text,
			StartLine:   hdr.start,
			EndLine:     hdr.end,
			FileType:    fileType,
			Lang:        lang,
		})
	}

	// --- Phase 2: scan body for doc comments and test markers ---
	// We walk from hdrEnd onward, maintaining a "pending comment" that is
	// flushed when the next code line decides its kind.

	inBlockComment := false
	var commentBuf []string
	var commentStart int
	var pendingIsTest bool  // whether pending attrs indicate a test
	inCfgTestBlock := false // Rust: inside #[cfg(test)] mod

	for i := hdrEnd; i < len(lines); i++ {
		if len(entries) >= maxEntries {
			break
		}
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)
		lineNum := i + 1

		// ── Rust cfg(test) block detector ──────────────────────────────────────
		if lang == "rust" && rustCfgTestRe.MatchString(trimmed) {
			inCfgTestBlock = true
		}

		// ── Block comment /* … */ ───────────────────────────────────────────────
		if inBlockComment {
			commentBuf = append(commentBuf, trimmed)
			if strings.Contains(trimmed, "*/") {
				inBlockComment = false
				// End of block comment recorded; continue to check next lines.
			}
			continue
		}

		if isBlockCommentOpen(trimmed) && !inBlockComment {
			// Check if it opens and closes on the same line.
			after := trimmed[2:]
			if strings.Contains(after, "*/") {
				// Single-line block comment.
				inner := extractBlockCommentSingleLine(trimmed)
				if len(commentBuf) == 0 {
					commentStart = lineNum
				}
				commentBuf = append(commentBuf, inner)
				// Does not stay in block mode.
			} else {
				inBlockComment = true
				inner := strings.TrimPrefix(trimmed, "/*")
				inner = strings.TrimPrefix(inner, "*")
				inner = strings.TrimSpace(inner)
				if len(commentBuf) == 0 {
					commentStart = lineNum
				}
				if inner != "" {
					commentBuf = append(commentBuf, inner)
				}
			}
			continue
		}

		// ── Triple-slash doc comment (Rust) ────────────────────────────────────
		if lang == "rust" && cfg.useTripleSlash && strings.HasPrefix(trimmed, "///") {
			text := strings.TrimSpace(strings.TrimPrefix(trimmed, "///"))
			if len(commentBuf) == 0 {
				commentStart = lineNum
			}
			commentBuf = append(commentBuf, text)
			continue
		}

		// ── Single-line // comment ─────────────────────────────────────────────
		if strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "///") {
			// For Rust, // (not ///) does NOT accumulate into doc comment.
			// For all other languages, a run of // lines forms a potential doc block.
			if lang != "rust" {
				text := strings.TrimSpace(strings.TrimPrefix(trimmed, "//"))
				if len(commentBuf) == 0 {
					commentStart = lineNum
				}
				commentBuf = append(commentBuf, text)
			}
			continue
		}

		// ── Attribute / annotation lines ───────────────────────────────────────
		if isAttrLine(lang, trimmed) {
			pendingIsTest = pendingIsTest || isTestAttr(lang, trimmed, cfg)
			// Rust: #[test] — also set via rustTestAttrRe for safety.
			if lang == "rust" && rustTestAttrRe.MatchString(trimmed) {
				pendingIsTest = true
			}
			continue
		}

		// ── Dart test call: test('name', fn) ──────────────────────────────────
		if cfg.dartTestCall {
			if m := dartTestCallRe.FindStringSubmatch(trimmed); m != nil {
				name := m[1]
				entries = append(entries, CodeDocEntry{
					SymbolName:  name,
					CommentKind: KindTestFunc,
					HeadingPath: "Tests > " + name,
					Text:        trimmed,
					StartLine:   lineNum,
					EndLine:     lineNum,
					FileType:    fileType,
					Lang:        lang,
				})
				commentBuf = nil
				commentStart = 0
				pendingIsTest = false
				continue
			}
		}

		// ── PHP @test in /** @test */ ──────────────────────────────────────────
		phpHasTestTag := false
		if cfg.phpTestTag && len(commentBuf) > 0 {
			for _, cl := range commentBuf {
				if strings.Contains(cl, "@test") {
					phpHasTestTag = true
					break
				}
			}
		}

		// ── Empty or blank line: flush pending only if no comment was active ───
		if trimmed == "" {
			// A blank line between comment and declaration resets the comment buffer.
			if !inBlockComment {
				commentBuf = nil
				commentStart = 0
				pendingIsTest = false
			}
			continue
		}

		// ── Declaration line ───────────────────────────────────────────────────
		sym := extractSymbol(lang, trimmed)
		if sym == "" {
			// Not a declaration: discard pending comment.
			commentBuf = nil
			commentStart = 0
			pendingIsTest = false
			continue
		}

		commentText := strings.Join(commentBuf, "\n")
		commentText = strings.TrimSpace(commentText)

		// Decide kind — compute isTest before the "skip if no content" guard
		// so that prefix-based test detection (Swift, PHP) works without a comment.
		kind := KindDocComment
		headingPrefix := "DocComment"

		isTest := pendingIsTest || phpHasTestTag

		// Swift: func testXxx() (no annotation needed)
		if lang == "swift" {
			for _, pfx := range cfg.testFuncPrefixes {
				if strings.HasPrefix(strings.ToLower(sym), strings.ToLower(pfx)) && sym != pfx {
					isTest = true
					break
				}
			}
		}

		// PHP: function name starting with "test"
		if lang == "php" {
			for _, pfx := range cfg.testFuncPrefixes {
				if strings.HasPrefix(strings.ToLower(sym), strings.ToLower(pfx)) && sym != pfx {
					isTest = true
					break
				}
			}
		}

		// Rust: inside cfg(test) or has #[test] attr
		if lang == "rust" && inCfgTestBlock {
			isTest = true
		}

		if isTest {
			kind = KindTestFunc
			headingPrefix = "Tests"
		}

		// Rust: look for "# Examples" section in comment → KindExampleFunc
		if lang == "rust" && kind == KindDocComment && strings.Contains(commentText, "# Examples") {
			kind = KindExampleFunc
			headingPrefix = "Examples"
		}

		entryFileType := fileType
		if isTest {
			entryFileType = FileTypeTest
		}

		if commentText != "" || isTest {
			sl := commentStart
			if sl == 0 {
				sl = lineNum
			}
			entries = append(entries, CodeDocEntry{
				SymbolName:  sym,
				CommentKind: kind,
				HeadingPath: headingPrefix + " > " + sym,
				Text:        commentText,
				StartLine:   sl,
				EndLine:     lineNum,
				FileType:    entryFileType,
				Lang:        lang,
			})
		}

		commentBuf = nil
		commentStart = 0
		pendingIsTest = false
	}

	return entries, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// File-type detection
// ──────────────────────────────────────────────────────────────────────────────

func detectFileType(lang, relPath string, lines []string, cfg cStyleConfig) string {
	lower := strings.ToLower(relPath)

	for _, suf := range cfg.testFileSuffixes {
		if strings.HasSuffix(lower, suf) {
			return FileTypeTest
		}
	}

	for _, seg := range cfg.testDirSegments {
		if strings.Contains(lower, seg) {
			return FileTypeTest
		}
	}

	// C/C++: if "test" appears anywhere in the base filename.
	if lang == "c" || lang == "cpp" {
		base := lower
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[idx+1:]
		}
		if strings.Contains(base, "test") {
			return FileTypeTest
		}
	}

	// Dart: if in test/ directory segment.
	if lang == "dart" {
		if strings.Contains(lower, "/test/") || strings.HasPrefix(lower, "test/") {
			return FileTypeTest
		}
	}

	if len(cfg.classNameTestSuffixes) > 0 {
		for _, line := range lines {
			if m := classRe.FindStringSubmatch(line); m != nil {
				className := m[1]
				for _, suf := range cfg.classNameTestSuffixes {
					if strings.HasSuffix(className, suf) {
						return FileTypeTest
					}
				}
			}
		}
	}

	// C# [TestFixture]
	if lang == "csharp" {
		for _, line := range lines {
			if strings.Contains(strings.TrimSpace(line), "[TestFixture]") {
				return FileTypeTest
			}
		}
	}

	return FileTypeSource
}

// ──────────────────────────────────────────────────────────────────────────────
// File-header extraction
// ──────────────────────────────────────────────────────────────────────────────

type commentSpan struct {
	text  string
	start int
	end   int
}

// extractFileHeader returns the first comment block at the top of the file
// (before any substantive code) and the line index (0-based) where it ends.
func extractFileHeader(lang string, lines []string, cfg cStyleConfig) (commentSpan, int) {
	// Skip leading blank lines.
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i >= len(lines) {
		return commentSpan{}, i
	}

	trimmed := strings.TrimSpace(lines[i])

	// PHP: skip <?php line.
	if lang == "php" && (strings.HasPrefix(trimmed, "<?php") || strings.HasPrefix(trimmed, "<?")) {
		i++
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
		if i >= len(lines) {
			return commentSpan{}, i
		}
		trimmed = strings.TrimSpace(lines[i])
	}

	startLine := i + 1 // 1-based

	// Case 1: block comment /* ... */
	if isBlockCommentOpen(trimmed) {
		var buf []string
		commentEnd := i
		inner := strings.TrimPrefix(trimmed, "/*")
		inner = strings.TrimPrefix(inner, "*")
		inner = strings.TrimSpace(inner)
		// Check same-line close.
		if idx := strings.Index(trimmed, "*/"); idx >= 2 {
			// single-line block comment
			inner = extractBlockCommentSingleLine(trimmed)
			if inner != "" {
				buf = append(buf, inner)
			}
			commentEnd = i
			return commentSpan{
				text:  strings.Join(buf, "\n"),
				start: startLine,
				end:   commentEnd + 1,
			}, commentEnd + 1
		}
		if inner != "" {
			buf = append(buf, inner)
		}
		i++
		for i < len(lines) {
			raw := strings.TrimSpace(lines[i])
			commentEnd = i
			// Strip leading * decoration.
			stripped := strings.TrimPrefix(raw, "*")
			stripped = strings.TrimSpace(stripped)
			if strings.HasSuffix(raw, "*/") {
				// Remove trailing */
				stripped = strings.TrimSuffix(stripped, "*/")
				stripped = strings.TrimSpace(stripped)
				if stripped != "" {
					buf = append(buf, stripped)
				}
				i++
				break
			}
			buf = append(buf, stripped)
			i++
		}
		text := strings.TrimSpace(strings.Join(buf, "\n"))
		return commentSpan{text: text, start: startLine, end: commentEnd + 1}, i
	}

	// Case 2: run of // lines.
	// For Rust: only plain // (not ///) counts as a file header.
	// /// is always a doc comment for the next item (not a file-level comment).
	if strings.HasPrefix(trimmed, "//") {
		// Rust: a leading /// block is NOT a file header — skip.
		if lang == "rust" && strings.HasPrefix(trimmed, "///") {
			return commentSpan{}, i
		}
		var buf []string
		commentEnd := i
		for i < len(lines) {
			t := strings.TrimSpace(lines[i])
			if !strings.HasPrefix(t, "//") {
				break
			}
			// Rust: stop at /// — it belongs to the following declaration.
			if lang == "rust" && strings.HasPrefix(t, "///") {
				break
			}
			buf = append(buf, strings.TrimSpace(strings.TrimPrefix(t, "//")))
			commentEnd = i
			i++
		}
		return commentSpan{
			text:  strings.Join(buf, "\n"),
			start: startLine,
			end:   commentEnd + 1,
		}, i
	}

	return commentSpan{}, i
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// isBlockCommentOpen returns true if the line starts a /* or /** block comment.
func isBlockCommentOpen(trimmed string) bool {
	return strings.HasPrefix(trimmed, "/*")
}

// extractBlockCommentSingleLine extracts the text from a single-line block
// comment like "/* text */" or "/** text */".
func extractBlockCommentSingleLine(trimmed string) string {
	s := trimmed
	if idx := strings.Index(s, "/*"); idx >= 0 {
		s = s[idx+2:]
	}
	if idx := strings.Index(s, "*/"); idx >= 0 {
		s = s[:idx]
	}
	// Strip leading asterisks (for /**).
	s = strings.TrimLeft(s, "*")
	return strings.TrimSpace(s)
}

// isAttrLine returns true if the line is an attribute/annotation, not code.
func isAttrLine(lang, trimmed string) bool {
	switch lang {
	case "rust":
		return strings.HasPrefix(trimmed, "#[")
	case "java", "kotlin":
		return strings.HasPrefix(trimmed, "@")
	case "csharp":
		return strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")
	}
	return false
}

// isTestAttr returns true if the attribute/annotation line indicates a test.
func isTestAttr(lang, trimmed string, cfg cStyleConfig) bool {
	for _, attr := range cfg.testAttrs {
		if trimmed == attr || strings.HasPrefix(trimmed, attr+"(") {
			return true
		}
	}
	return false
}

// extractSymbol returns the primary symbol name from a declaration line.
// Returns "" if the line does not look like a top-level declaration.
func extractSymbol(lang, trimmed string) string {
	switch lang {
	case "rust":
		if m := rustFuncRe.FindStringSubmatch(trimmed); m != nil {
			return m[1]
		}
		if m := classRe.FindStringSubmatch(trimmed); m != nil {
			return m[1]
		}
	case "swift":
		if m := funcRe.FindStringSubmatch(trimmed); m != nil {
			return m[1]
		}
		if m := classRe.FindStringSubmatch(trimmed); m != nil {
			return m[1]
		}
	case "kotlin":
		if m := kotlinFuncRe.FindStringSubmatch(trimmed); m != nil {
			return m[1]
		}
		if m := classRe.FindStringSubmatch(trimmed); m != nil {
			return m[1]
		}
	case "java":
		// Prefer javaMethodRe for typed methods; fallback to func patterns.
		if m := javaMethodRe.FindStringSubmatch(trimmed); m != nil {
			return m[1]
		}
		if m := classRe.FindStringSubmatch(trimmed); m != nil {
			return m[1]
		}
	case "csharp":
		if m := javaMethodRe.FindStringSubmatch(trimmed); m != nil {
			return m[1]
		}
		if m := classRe.FindStringSubmatch(trimmed); m != nil {
			return m[1]
		}
	case "php":
		if m := phpFuncRe.FindStringSubmatch(trimmed); m != nil {
			return m[1]
		}
		if m := classRe.FindStringSubmatch(trimmed); m != nil {
			return m[1]
		}
	case "dart":
		if m := funcRe.FindStringSubmatch(trimmed); m != nil {
			return m[1]
		}
		if m := classRe.FindStringSubmatch(trimmed); m != nil {
			return m[1]
		}
	case "c", "cpp":
		if m := cFuncRe.FindStringSubmatch(trimmed); m != nil {
			return m[1]
		}
		if m := classRe.FindStringSubmatch(trimmed); m != nil {
			return m[1]
		}
	}
	return ""
}

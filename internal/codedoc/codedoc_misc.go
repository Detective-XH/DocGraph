// codedoc_misc.go — language extractors for Lua/Luau, Pascal, SQL, and Liquid.
//
// Each extractor is shallow and regex-based (stdlib only, no external deps).
// Registered via init() — imported transitively when codedoc package is used.
package codedoc

import (
	"bytes"
	"regexp"
	"strings"
)

func init() {
	RegisterExtractor("lua", extractLua, ".lua")
	RegisterExtractor("luau", extractLuau, ".luau")
	RegisterExtractor("pascal", extractPascal, ".pas", ".pp")
	RegisterExtractor("sql", extractSQL, ".sql")
	RegisterExtractor("liquid", extractLiquid, ".liquid")
}

// ──────────────────────────────────────────────────────────────────────────────
// Lua / Luau
// ──────────────────────────────────────────────────────────────────────────────

var (
	// luaFuncRe matches bare and local function declarations.
	luaFuncRe = regexp.MustCompile(`(?i)^(?:local\s+)?function\s+([A-Za-z_][A-Za-z0-9_.]*)`)

	// luaTestDescribeRe matches Busted/Roblox describe('…') blocks.
	luaTestDescribeRe = regexp.MustCompile(`^describe\s*\(\s*['"]([^'"]+)['"]`)
	// luaTestItRe matches it('…') calls.
	luaTestItRe = regexp.MustCompile(`^it\s*\(\s*['"]([^'"]+)['"]`)
)

func extractLuaLang(lang, relPath string, src []byte) ([]CodeDocEntry, error) {
	fileType := FileTypeSource
	base := strings.ToLower(relPath)
	if strings.HasSuffix(base, "_spec.lua") || strings.HasSuffix(base, "_test.lua") ||
		strings.HasSuffix(base, "_spec.luau") || strings.HasSuffix(base, "_test.luau") {
		fileType = FileTypeTest
	}

	lines := splitLines(src)
	var entries []CodeDocEntry

	// File header: leading long-bracket block --[[ ... ]] or leading -- lines.
	if hdr, sl, el := luaFileHeader(lines); hdr != "" {
		entries = append(entries, CodeDocEntry{
			CommentKind: KindFileHeader,
			HeadingPath: "File Header",
			Text:        hdr,
			StartLine:   sl,
			EndLine:     el,
			FileType:    fileType,
			Lang:        lang,
		})
	}

	// Line-by-line: collect -- comment blocks and match to function declarations,
	// or detect test framework calls.
	var commentBuf []string
	var commentStart int

	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		lineNum := i + 1

		if strings.HasPrefix(line, "--") && !strings.HasPrefix(line, "--[[") {
			// Single-line comment.
			text := strings.TrimSpace(strings.TrimPrefix(line, "--"))
			if len(commentBuf) == 0 {
				commentStart = lineNum
			}
			commentBuf = append(commentBuf, text)
			continue
		}

		// Non-comment line: check if it is a function decl or test call.
		if len(commentBuf) > 0 {
			commentText := strings.Join(commentBuf, "\n")

			if m := luaFuncRe.FindStringSubmatch(line); m != nil {
				entries = append(entries, CodeDocEntry{
					SymbolName:  m[1],
					CommentKind: KindDocComment,
					HeadingPath: "DocComment > " + m[1],
					Text:        commentText,
					StartLine:   commentStart,
					EndLine:     lineNum,
					FileType:    fileType,
					Lang:        lang,
				})
			}
			commentBuf = nil
			commentStart = 0
		}

		// Test framework: describe / it.
		if m := luaTestDescribeRe.FindStringSubmatch(line); m != nil {
			entries = append(entries, CodeDocEntry{
				SymbolName:  m[1],
				CommentKind: KindTestFunc,
				HeadingPath: "Tests > " + m[1],
				Text:        line,
				StartLine:   lineNum,
				EndLine:     lineNum,
				FileType:    fileType,
				Lang:        lang,
			})
			continue
		}
		if m := luaTestItRe.FindStringSubmatch(line); m != nil {
			entries = append(entries, CodeDocEntry{
				SymbolName:  m[1],
				CommentKind: KindTestFunc,
				HeadingPath: "Tests > " + m[1],
				Text:        line,
				StartLine:   lineNum,
				EndLine:     lineNum,
				FileType:    fileType,
				Lang:        lang,
			})
			continue
		}
	}

	return entries, nil
}

func extractLua(relPath string, src []byte) ([]CodeDocEntry, error) {
	return extractLuaLang("lua", relPath, src)
}

func extractLuau(relPath string, src []byte) ([]CodeDocEntry, error) {
	return extractLuaLang("luau", relPath, src)
}

// luaFileHeader returns the text, start line, and end line of the leading
// long-bracket block (--[[ ... ]]) or the initial run of -- comment lines.
// Returns "", 0, 0 if none found.
func luaFileHeader(lines []string) (string, int, int) {
	if len(lines) == 0 {
		return "", 0, 0
	}

	// Skip leading blank lines.
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	if start >= len(lines) {
		return "", 0, 0
	}

	first := strings.TrimSpace(lines[start])

	// Long-bracket block: --[[ ... ]]
	if strings.HasPrefix(first, "--[[") {
		joined := strings.Join(lines[start:], "\n")
		end := strings.Index(joined, "]]")
		if end >= 0 {
			block := joined[:end+2]
			lineCount := strings.Count(block, "\n")
			text := strings.TrimSpace(block[4:end]) // strip --[[ and ]]
			return text, start + 1, start + 1 + lineCount
		}
	}

	// Run of single-line -- comments.
	if !strings.HasPrefix(first, "--") {
		return "", 0, 0
	}
	var buf []string
	end := start
	for end < len(lines) {
		trimmed := strings.TrimSpace(lines[end])
		if !strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "--[[") {
			break
		}
		buf = append(buf, strings.TrimSpace(strings.TrimPrefix(trimmed, "--")))
		end++
	}
	if len(buf) == 0 {
		return "", 0, 0
	}
	return strings.Join(buf, "\n"), start + 1, end
}

// ──────────────────────────────────────────────────────────────────────────────
// Pascal
// ──────────────────────────────────────────────────────────────────────────────

var (
	// pascalBraceCommentRe matches { ... } comments (possibly multi-line).
	pascalBraceCommentRe = regexp.MustCompile(`(?s)\{([^}]*)\}`)
	// pascalStarCommentRe matches (* ... *) comments.
	pascalStarCommentRe = regexp.MustCompile(`(?s)\(\*(.+?)\*\)`)
	// pascalDeclRe matches procedure/function declarations.
	pascalDeclRe = regexp.MustCompile(`(?i)^(?:procedure|function)\s+([A-Za-z_][A-Za-z0-9_]*)`)
)

func extractPascal(relPath string, src []byte) ([]CodeDocEntry, error) {
	lines := splitLines(src)
	var entries []CodeDocEntry
	fileType := FileTypeSource

	// Collect comment blocks with their start/end line positions.
	type commentBlock struct {
		text  string
		start int // 1-based
		end   int // 1-based
	}
	var blocks []commentBlock

	i := 0
	for i < len(lines) {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)
		lineNum := i + 1

		if strings.HasPrefix(trimmed, "{") {
			// Brace comment — may span multiple lines.
			joined, span := collectUntilClose(lines, i, "{", "}")
			if m := pascalBraceCommentRe.FindStringSubmatch(joined); m != nil {
				blocks = append(blocks, commentBlock{
					text:  strings.TrimSpace(m[1]),
					start: lineNum,
					end:   lineNum + span - 1,
				})
			}
			i += span
			continue
		}

		if strings.HasPrefix(trimmed, "(*") {
			joined, span := collectUntilClose(lines, i, "(*", "*)")
			if m := pascalStarCommentRe.FindStringSubmatch(joined); m != nil {
				blocks = append(blocks, commentBlock{
					text:  strings.TrimSpace(m[1]),
					start: lineNum,
					end:   lineNum + span - 1,
				})
			}
			i += span
			continue
		}

		i++
	}

	if len(blocks) == 0 {
		return entries, nil
	}

	// First block is a file header if it starts at line 1 (after skipping blanks).
	first := blocks[0]
	// Find first non-blank line.
	firstContentLine := 1
	for k, l := range lines {
		if strings.TrimSpace(l) != "" {
			firstContentLine = k + 1
			break
		}
	}
	if first.start == firstContentLine {
		entries = append(entries, CodeDocEntry{
			CommentKind: KindFileHeader,
			HeadingPath: "File Header",
			Text:        first.text,
			StartLine:   first.start,
			EndLine:     first.end,
			FileType:    fileType,
			Lang:        "pascal",
		})
		blocks = blocks[1:]
	}

	// Remaining blocks: check if immediately followed by a procedure/function decl.
	for _, blk := range blocks {
		// Find the next non-blank line after blk.end.
		nextLine := ""
		nextLineNum := 0
		for k := blk.end; k < len(lines); k++ {
			if strings.TrimSpace(lines[k]) != "" {
				nextLine = strings.TrimSpace(lines[k])
				nextLineNum = k + 1
				break
			}
		}
		if nextLine == "" {
			continue
		}
		if m := pascalDeclRe.FindStringSubmatch(nextLine); m != nil {
			entries = append(entries, CodeDocEntry{
				SymbolName:  m[1],
				CommentKind: KindDocComment,
				HeadingPath: "DocComment > " + m[1],
				Text:        blk.text,
				StartLine:   blk.start,
				EndLine:     nextLineNum,
				FileType:    fileType,
				Lang:        "pascal",
			})
		}
	}

	return entries, nil
}

// collectUntilClose collects lines starting at idx until the close delimiter is found.
// Returns the joined string and the number of lines consumed (≥1).
func collectUntilClose(lines []string, idx int, open, close string) (string, int) {
	var buf bytes.Buffer
	span := 0
	for i := idx; i < len(lines); i++ {
		span++
		buf.WriteString(lines[i])
		buf.WriteByte('\n')
		if i == idx {
			// First line — only search after the open delimiter.
			after := strings.Index(lines[i], open)
			if after >= 0 {
				rest := lines[i][after+len(open):]
				if strings.Contains(rest, close) {
					break
				}
			}
		} else {
			if strings.Contains(lines[i], close) {
				break
			}
		}
	}
	return buf.String(), span
}

// ──────────────────────────────────────────────────────────────────────────────
// SQL
// ──────────────────────────────────────────────────────────────────────────────

var (
	// sqlCreateRe matches CREATE PROCEDURE/FUNCTION/VIEW/TABLE object names.
	sqlCreateRe = regexp.MustCompile(`(?i)^CREATE\s+(?:OR\s+REPLACE\s+)?(?:PROCEDURE|FUNCTION|VIEW|TABLE)\s+([A-Za-z_][A-Za-z0-9_.]*)`)
)

func extractSQL(relPath string, src []byte) ([]CodeDocEntry, error) {
	lines := splitLines(src)
	fileType := FileTypeSource
	var entries []CodeDocEntry

	// File header: leading -- block.
	if hdr, sl, el := sqlFileHeader(lines); hdr != "" {
		entries = append(entries, CodeDocEntry{
			CommentKind: KindFileHeader,
			HeadingPath: "File Header",
			Text:        hdr,
			StartLine:   sl,
			EndLine:     el,
			FileType:    fileType,
			Lang:        "sql",
		})
	}

	// Scan for -- comment blocks preceding CREATE statements.
	var commentBuf []string
	var commentStart int

	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		lineNum := i + 1

		if strings.HasPrefix(line, "--") {
			text := strings.TrimSpace(strings.TrimPrefix(line, "--"))
			if len(commentBuf) == 0 {
				commentStart = lineNum
			}
			commentBuf = append(commentBuf, text)
			continue
		}

		if strings.TrimSpace(line) == "" {
			// Blank line resets comment buffer unless we are accumulating.
			// Only reset if buffer hasn't been started, to allow multi-para headers.
			if len(commentBuf) > 0 {
				// Keep buffer — may span paragraphs before a CREATE.
				commentBuf = append(commentBuf, "")
			}
			continue
		}

		// Non-comment, non-blank line.
		if len(commentBuf) > 0 {
			commentText := strings.Join(commentBuf, "\n")
			commentText = strings.TrimSpace(commentText)

			if m := sqlCreateRe.FindStringSubmatch(line); m != nil {
				entries = append(entries, CodeDocEntry{
					SymbolName:  m[1],
					CommentKind: KindDocComment,
					HeadingPath: "DocComment > " + m[1],
					Text:        commentText,
					StartLine:   commentStart,
					EndLine:     lineNum,
					FileType:    fileType,
					Lang:        "sql",
				})
			}
			commentBuf = nil
			commentStart = 0
			continue
		}
	}

	return entries, nil
}

// sqlFileHeader returns leading -- comment block text and line numbers.
func sqlFileHeader(lines []string) (string, int, int) {
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	if start >= len(lines) {
		return "", 0, 0
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[start]), "--") {
		return "", 0, 0
	}
	var buf []string
	end := start
	for end < len(lines) {
		trimmed := strings.TrimSpace(lines[end])
		if !strings.HasPrefix(trimmed, "--") {
			break
		}
		buf = append(buf, strings.TrimSpace(strings.TrimPrefix(trimmed, "--")))
		end++
	}
	if len(buf) == 0 {
		return "", 0, 0
	}
	return strings.Join(buf, "\n"), start + 1, end
}

// ──────────────────────────────────────────────────────────────────────────────
// Liquid
// ──────────────────────────────────────────────────────────────────────────────

var (
	// liquidCommentRe matches {% comment %}...{% endcomment %} blocks.
	liquidCommentRe = regexp.MustCompile(`(?s)\{%-?\s*comment\s*-?%\}(.*?)\{%-?\s*endcomment\s*-?%\}`)
)

func extractLiquid(relPath string, src []byte) ([]CodeDocEntry, error) {
	fileType := FileTypeSource
	var entries []CodeDocEntry

	matches := liquidCommentRe.FindAllIndex(src, -1)
	textMatches := liquidCommentRe.FindAllSubmatch(src, -1)

	for idx, loc := range matches {
		text := strings.TrimSpace(string(textMatches[idx][1]))
		if text == "" {
			continue
		}
		startLine := bytes.Count(src[:loc[0]], []byte("\n")) + 1
		endLine := bytes.Count(src[:loc[1]], []byte("\n")) + 1

		kind := KindDocComment
		headingPath := "DocComment"

		// First comment block near the top of file → treat as file header.
		if idx == 0 && startLine <= 10 {
			kind = KindFileHeader
			headingPath = "File Header"
		}

		entries = append(entries, CodeDocEntry{
			CommentKind: kind,
			HeadingPath: headingPath,
			Text:        text,
			StartLine:   startLine,
			EndLine:     endLine,
			FileType:    fileType,
			Lang:        "liquid",
		})
	}

	return entries, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Shared helpers
// ──────────────────────────────────────────────────────────────────────────────

// splitLines splits src into lines, preserving empty lines.
func splitLines(src []byte) []string {
	raw := strings.Split(string(src), "\n")
	// Trim trailing empty string from a final newline.
	if len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	return raw
}

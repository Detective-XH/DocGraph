package codedoc

import (
	"path/filepath"
	"strings"
)

func init() {
	RegisterExtractor("python", extractPython, ".py")
	RegisterExtractor("ruby", extractRuby, ".rb")
}

// extractPython extracts documentation entries from Python source files.
//
// Extracted kinds:
//   - KindFileHeader: module-level docstring (first triple-quoted string at
//     top of file, skipping shebangs, encoding declarations, and blank lines).
//   - KindDocComment: docstring immediately following a def/class statement
//     (tolerates decorators and multi-line signatures).
//   - KindTestFunc: any def whose name starts with "test_", or any class whose
//     name starts with "Test" (no docstring required for test entries).
func extractPython(relPath string, src []byte) ([]CodeDocEntry, error) {
	fileType := pyFileType(relPath)
	lang := "python"

	var entries []CodeDocEntry
	lines := splitLines(src)

	headerEntry, headerEnd := pyModuleDocstring(lines, fileType, lang)
	if headerEntry != nil {
		entries = append(entries, *headerEntry)
	}
	entries = append(entries, pySymbols(lines, headerEnd, fileType, lang)...)
	return entries, nil
}

func pyFileType(relPath string) string {
	base := strings.ToLower(filepath.Base(relPath))
	if strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") {
		return FileTypeTest
	}
	return FileTypeSource
}

func pyModuleDocstring(lines []string, fileType, lang string) (*CodeDocEntry, int) {
	i := 0
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			i++
			continue
		}
		break
	}
	if i >= len(lines) {
		return nil, 0
	}

	trimmed := strings.TrimSpace(lines[i])
	quote := pyTripleQuotePrefix(trimmed)
	if quote == "" {
		return nil, 0
	}

	startLine := i + 1
	text, endLine := pyReadTripleQuoted(lines, i, quote)
	if text == "" {
		return nil, 0
	}

	return &CodeDocEntry{
		SymbolName:  "",
		CommentKind: KindFileHeader,
		HeadingPath: "File Header",
		Text:        text,
		StartLine:   startLine,
		EndLine:     endLine,
		FileType:    fileType,
		Lang:        lang,
	}, endLine
}

func pySymbols(lines []string, startAfter int, fileType, lang string) []CodeDocEntry {
	var entries []CodeDocEntry
	n := len(lines)
	consumed := make([]bool, n)

	i := startAfter
	if i < 0 {
		i = 0
	}
	for i < n {
		if consumed[i] {
			i++
			continue
		}

		trimmed := strings.TrimSpace(lines[i])

		if strings.HasPrefix(trimmed, "@") {
			i++
			continue
		}

		kind, name, found := pyDefOrClass(trimmed)
		if !found {
			i++
			continue
		}

		defLine := i + 1
		sigEndIdx := pySignatureEnd(lines, i)
		dsStart, dsText, dsEnd := pyDocstringAfter(lines, sigEndIdx, consumed)

		switch kind {
		case "def":
			isTest := strings.HasPrefix(name, "test_")
			if isTest {
				endLine := defLine
				if dsText != "" {
					endLine = dsEnd
				}
				entries = append(entries, CodeDocEntry{
					SymbolName:  name,
					CommentKind: KindTestFunc,
					HeadingPath: "Tests > " + name,
					Text:        dsText,
					StartLine:   defLine,
					EndLine:     endLine,
					FileType:    fileType,
					Lang:        lang,
				})
				if dsEnd > sigEndIdx {
					pyMarkConsumed(consumed, dsStart, dsEnd-1)
				}
			} else if dsText != "" {
				entries = append(entries, CodeDocEntry{
					SymbolName:  name,
					CommentKind: KindDocComment,
					HeadingPath: "DocComment > " + name,
					Text:        dsText,
					StartLine:   defLine,
					EndLine:     dsEnd,
					FileType:    fileType,
					Lang:        lang,
				})
				pyMarkConsumed(consumed, dsStart, dsEnd-1)
			}

		case "class":
			isTest := strings.HasPrefix(name, "Test")
			if isTest {
				endLine := defLine
				if dsText != "" {
					endLine = dsEnd
				}
				entries = append(entries, CodeDocEntry{
					SymbolName:  name,
					CommentKind: KindTestFunc,
					HeadingPath: "Tests > " + name,
					Text:        dsText,
					StartLine:   defLine,
					EndLine:     endLine,
					FileType:    fileType,
					Lang:        lang,
				})
				if dsEnd > sigEndIdx {
					pyMarkConsumed(consumed, dsStart, dsEnd-1)
				}
			} else if dsText != "" {
				entries = append(entries, CodeDocEntry{
					SymbolName:  name,
					CommentKind: KindDocComment,
					HeadingPath: "DocComment > " + name,
					Text:        dsText,
					StartLine:   defLine,
					EndLine:     dsEnd,
					FileType:    fileType,
					Lang:        lang,
				})
				pyMarkConsumed(consumed, dsStart, dsEnd-1)
			}
		}

		i = sigEndIdx + 1
	}
	return entries
}

func pyDefOrClass(line string) (kind, name string, found bool) {
	for _, kw := range []string{"def ", "class "} {
		if !strings.HasPrefix(line, kw) {
			continue
		}
		rest := line[len(kw):]
		end := strings.IndexAny(rest, "(: \t")
		if end < 0 {
			end = len(rest)
		}
		n := rest[:end]
		if n == "" {
			continue
		}
		return strings.TrimSuffix(kw, " "), n, true
	}
	return "", "", false
}

func pySignatureEnd(lines []string, i int) int {
	line := strings.TrimSpace(lines[i])
	if strings.HasSuffix(line, ":") || strings.Contains(line, "):") {
		return i
	}
	for j := i + 1; j < len(lines) && j < i+50; j++ {
		t := strings.TrimSpace(lines[j])
		if strings.HasSuffix(t, ":") {
			return j
		}
	}
	return i
}

func pyDocstringAfter(lines []string, sigEndIdx int, consumed []bool) (startIdx int, text string, endLine int) {
	j := sigEndIdx + 1
	for j < len(lines) {
		if consumed[j] {
			break
		}
		trimmed := strings.TrimSpace(lines[j])
		if trimmed == "" {
			j++
			continue
		}
		quote := pyTripleQuotePrefix(trimmed)
		if quote == "" {
			return 0, "", 0
		}
		t, end := pyReadTripleQuoted(lines, j, quote)
		if t == "" {
			return 0, "", 0
		}
		return j, t, end
	}
	return 0, "", 0
}

func pyTripleQuotePrefix(line string) string {
	for _, q := range []string{`"""`, `'''`} {
		if strings.HasPrefix(line, q) {
			return q
		}
	}
	return ""
}

func pyReadTripleQuoted(lines []string, startIdx int, quote string) (string, int) {
	first := strings.TrimSpace(lines[startIdx])
	content := first[len(quote):]

	if idx := strings.Index(content, quote); idx >= 0 {
		text := strings.TrimSpace(content[:idx])
		return text, startIdx + 1
	}

	var sb strings.Builder
	sb.WriteString(content)

	for j := startIdx + 1; j < len(lines); j++ {
		raw := lines[j]
		idx := strings.Index(raw, quote)
		if idx >= 0 {
			beforeClose := raw[:idx]
			if sb.Len() > 0 && strings.TrimSpace(beforeClose) != "" {
				sb.WriteByte('\n')
				sb.WriteString(strings.TrimSpace(beforeClose))
			} else if strings.TrimSpace(beforeClose) != "" {
				sb.WriteString(strings.TrimSpace(beforeClose))
			}
			return strings.TrimSpace(sb.String()), j + 1
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(strings.TrimRight(raw, " \t"))
	}
	return strings.TrimSpace(sb.String()), len(lines)
}

func pyMarkConsumed(consumed []bool, startIdx, endIdx int) {
	for k := startIdx; k <= endIdx && k < len(consumed); k++ {
		consumed[k] = true
	}
}

// extractRuby extracts documentation entries from Ruby source files.
func extractRuby(relPath string, src []byte) ([]CodeDocEntry, error) {
	fileType := rbFileType(relPath)
	lang := "ruby"

	var entries []CodeDocEntry
	lines := splitLines(src)

	headerEntry, headerEnd := rbFileHeader(lines, fileType, lang)
	if headerEntry != nil {
		entries = append(entries, *headerEntry)
	}
	entries = append(entries, rbSymbols(lines, headerEnd, fileType, lang)...)
	return entries, nil
}

func rbFileType(relPath string) string {
	base := strings.ToLower(filepath.Base(relPath))
	if strings.Contains(base, "_spec.rb") || strings.Contains(base, "_test.rb") {
		return FileTypeTest
	}
	return FileTypeSource
}

func rbFileHeader(lines []string, fileType, lang string) (*CodeDocEntry, int) {
	i := 0
	if i < len(lines) && strings.HasPrefix(lines[i], "#!") {
		i++
	}
	if i >= len(lines) {
		return nil, 0
	}

	startIdx := i
	var commentLines []string
	for i < len(lines) {
		t := lines[i]
		trimmed := strings.TrimSpace(t)
		if strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "#!") {
			text := strings.TrimPrefix(trimmed, "#")
			text = strings.TrimPrefix(text, " ")
			commentLines = append(commentLines, text)
			i++
		} else {
			break
		}
	}

	if len(commentLines) == 0 {
		return nil, 0
	}

	text := strings.TrimSpace(strings.Join(commentLines, "\n"))
	if text == "" {
		return nil, 0
	}

	nextCode := i
	for nextCode < len(lines) && strings.TrimSpace(lines[nextCode]) == "" {
		nextCode++
	}
	if nextCode < len(lines) {
		t := strings.TrimSpace(lines[nextCode])
		if kind, _, _ := rbStatement(t); kind != "" {
			return nil, 0
		}
	}

	return &CodeDocEntry{
		SymbolName:  "",
		CommentKind: KindFileHeader,
		HeadingPath: "File Header",
		Text:        text,
		StartLine:   startIdx + 1,
		EndLine:     i,
		FileType:    fileType,
		Lang:        lang,
	}, i
}

func rbSymbols(lines []string, startAfter int, fileType, lang string) []CodeDocEntry {
	var entries []CodeDocEntry
	n := len(lines)
	i := startAfter
	if i < 0 {
		i = 0
	}

	var pendingComment []string
	var pendingStart int

	for i < n {
		trimmed := strings.TrimSpace(lines[i])

		if strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "#!") {
			if len(pendingComment) == 0 {
				pendingStart = i
			}
			text := strings.TrimPrefix(trimmed, "#")
			text = strings.TrimPrefix(text, " ")
			pendingComment = append(pendingComment, text)
			i++
			continue
		}

		if trimmed == "" {
			pendingComment = nil
			i++
			continue
		}

		kind, name, isTest := rbStatement(trimmed)
		if kind == "" {
			pendingComment = nil
			i++
			continue
		}

		defLine := i + 1
		docText := ""
		commentStartLine := defLine
		if len(pendingComment) > 0 {
			docText = strings.TrimSpace(strings.Join(pendingComment, "\n"))
			commentStartLine = pendingStart + 1
		}

		switch {
		case isTest:
			entries = append(entries, CodeDocEntry{
				SymbolName:  name,
				CommentKind: KindTestFunc,
				HeadingPath: "Tests > " + name,
				Text:        docText,
				StartLine:   commentStartLine,
				EndLine:     defLine,
				FileType:    fileType,
				Lang:        lang,
			})
		case kind == "def" && docText != "":
			entries = append(entries, CodeDocEntry{
				SymbolName:  name,
				CommentKind: KindDocComment,
				HeadingPath: "DocComment > " + name,
				Text:        docText,
				StartLine:   commentStartLine,
				EndLine:     defLine,
				FileType:    fileType,
				Lang:        lang,
			})
		case kind == "describe" || kind == "context":
			entries = append(entries, CodeDocEntry{
				SymbolName:  name,
				CommentKind: KindTestFunc,
				HeadingPath: "Tests > " + name,
				Text:        docText,
				StartLine:   commentStartLine,
				EndLine:     defLine,
				FileType:    fileType,
				Lang:        lang,
			})
		}

		pendingComment = nil
		i++
	}
	return entries
}

func rbStatement(line string) (kind, name string, isTest bool) {
	if strings.HasPrefix(line, "def ") {
		rest := strings.TrimPrefix(line, "def ")
		end := strings.IndexAny(rest, "( \t")
		if end < 0 {
			end = len(rest)
		}
		methodName := rest[:end]
		if methodName == "" {
			return "", "", false
		}
		return "def", methodName, strings.HasPrefix(methodName, "test_")
	}

	if strings.HasPrefix(line, "it ") || strings.HasPrefix(line, "it('") || strings.HasPrefix(line, `it("`) {
		label := rbExtractStringArg(line)
		if label == "" {
			label = "anonymous"
		}
		return "it", label, true
	}

	if strings.HasPrefix(line, "describe ") || strings.HasPrefix(line, "describe('") || strings.HasPrefix(line, `describe("`) {
		label := rbExtractStringArg(line)
		if label == "" {
			label = "anonymous"
		}
		return "describe", label, true
	}

	if strings.HasPrefix(line, "context ") || strings.HasPrefix(line, "context('") || strings.HasPrefix(line, `context("`) {
		label := rbExtractStringArg(line)
		if label == "" {
			label = "anonymous"
		}
		return "context", label, true
	}

	return "", "", false
}

func rbExtractStringArg(line string) string {
	for _, q := range []byte{'"', '\''} {
		idx := strings.IndexByte(line, q)
		if idx < 0 {
			continue
		}
		rest := line[idx+1:]
		end := strings.IndexByte(rest, q)
		if end < 0 {
			continue
		}
		return rest[:end]
	}
	return ""
}

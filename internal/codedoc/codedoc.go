// Package codedoc indexes code files for their documentation surface:
// file headers, exported doc comments, test names, and example names.
// Symbol semantics (types, call graphs, imports) are out of scope.
//
// Extraction is shallow and regex-based for most languages; Go uses go/parser.
// No new Go module dependencies are added.
//
// Entry point: Extract(absPath, relPath, src, hash) → *parser.ParseResult
// Registration: language files call RegisterExtractor from init().
package codedoc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Detective-XH/docgraph/internal/parser"
	"github.com/Detective-XH/docgraph/internal/store"
)

// Comment kind constants are stable strings consumed by docs-code drift checks.
const (
	KindFileHeader  = "file_header"
	KindTestFunc    = "test_func"
	KindExampleFunc = "example_func"
	KindDocComment  = "doc_comment"

	FileTypeSource = "source"
	FileTypeTest   = "test"

	maxTextBytes = 10_240 // H-19 10 KB cap (matches section_chunks cap)
	maxEntries   = 500    // max comment blocks per file
)

// CodeDocEntry is an extracted code documentation block returned by language extractors.
type CodeDocEntry struct {
	SymbolName  string // exported symbol name; "" for file_header.
	CommentKind string // KindFileHeader | KindTestFunc | KindExampleFunc | KindDocComment
	HeadingPath string // "File Header", "Tests > TestFoo", "Examples > ExampleFoo", "DocComment > Bar"
	Text        string // extracted comment/doc text
	StartLine   int
	EndLine     int
	FileType    string // FileTypeSource | FileTypeTest
	Lang        string // "go", "python", "javascript", "typescript", "rust"
}

// extractFunc is the per-language extraction function signature.
type extractFunc func(relPath string, src []byte) ([]CodeDocEntry, error)

type langExtractor struct {
	lang string
	fn   extractFunc
}

// extractors is populated by language-specific init() functions.
var extractors = map[string]langExtractor{}

// RegisterExtractor registers a language extractor for one or more file extensions.
// Called from init() in per-language files (codedoc_go.go, codedoc_python.go, etc.).
func RegisterExtractor(lang string, fn extractFunc, exts ...string) {
	for _, ext := range exts {
		extractors[ext] = langExtractor{lang: lang, fn: fn}
	}
}

// Extract dispatches to the language-specific extractor and returns a ParseResult
// shaped identically to the document extraction pipeline output.
// Returns (nil, error) for unsupported extensions or parse failures.
func Extract(absPath, relPath string, src []byte, hash string) (*parser.ParseResult, error) {
	ext := strings.ToLower(filepath.Ext(relPath))
	le, ok := extractors[ext]
	if !ok {
		return nil, fmt.Errorf("codedoc: unsupported extension %q", ext)
	}
	entries, err := le.fn(relPath, src)
	if err != nil {
		return nil, fmt.Errorf("codedoc %s: %w", relPath, err)
	}
	return buildResult(relPath, src, hash, le.lang, entries), nil
}

// SupportedExts returns all extensions currently registered by language extractors.
// Used by docformat and scanner to decide which code files to include.
func SupportedExts() []string {
	exts := make([]string, 0, len(extractors))
	for ext := range extractors {
		exts = append(exts, ext)
	}
	return exts
}

// IsCodeExt reports whether ext (lower-cased, dot-prefixed) has a registered extractor.
func IsCodeExt(ext string) bool {
	_, ok := extractors[ext]
	return ok
}

// buildResult converts extracted entries into a *parser.ParseResult.
func buildResult(relPath string, src []byte, hash, lang string, entries []CodeDocEntry) *parser.ParseResult {
	now := time.Now().Unix()

	lineCount := strings.Count(string(src), "\n") + 1
	docNode := store.Node{
		ID:            relPath,
		Kind:          "code_file",
		Name:          filepath.Base(relPath),
		QualifiedName: relPath,
		FilePath:      relPath,
		StartLine:     1,
		EndLine:       lineCount,
		Metadata:      marshalMeta(map[string]string{"source_language": lang}),
		UpdatedAt:     now,
	}

	var headings []store.Node
	var chunks []store.SectionChunk

	for i, e := range entries {
		if i >= maxEntries {
			break
		}
		text := e.Text
		if len(text) > maxTextBytes {
			text = text[:maxTextBytes]
		}

		nodeID := fmt.Sprintf("%s#%s-%d", relPath, e.CommentKind, e.StartLine)
		headings = append(headings, store.Node{
			ID:            nodeID,
			Kind:          "heading",
			Name:          entryName(e),
			QualifiedName: nodeID,
			FilePath:      relPath,
			StartLine:     e.StartLine,
			EndLine:       e.EndLine,
			Level:         2,
			Metadata:      entryMeta(e),
			BodyExcerpt:   truncate(text, 500),
			UpdatedAt:     now,
		})

		chunks = append(chunks, store.SectionChunk{
			NodeID:      nodeID,
			FilePath:    relPath,
			StartLine:   e.StartLine,
			EndLine:     e.EndLine,
			ContentHash: hash,
			SectionHash: sha256Short(text),
			HeadingPath: e.HeadingPath,
			Text:        text,
		})
	}

	return &parser.ParseResult{
		DocNode:       docNode,
		Headings:      headings,
		SectionChunks: chunks,
		FileInfo: store.FileInfo{
			Path:        relPath,
			ContentHash: hash,
			Size:        int64(len(src)),
		},
	}
}

func entryName(e CodeDocEntry) string {
	if e.SymbolName != "" {
		return e.SymbolName
	}
	return "File Header"
}

func entryMeta(e CodeDocEntry) string {
	return marshalMeta(map[string]string{
		"source_language":  e.Lang,
		"comment_kind":     e.CommentKind,
		"file_type":        e.FileType,
		"symbol_name":      e.SymbolName,
		"codegraph_anchor": "", // reserved for future CodeGraph interop
	})
}

func marshalMeta(m map[string]string) string {
	b, _ := json.Marshal(m)
	return string(b)
}

func sha256Short(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:16]
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// Package extractor dispatches non-Markdown files to format-specific
// extractors, each returning a *parser.ParseResult identical in shape to the
// Markdown pipeline output.
package extractor

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Detective-XH/docgraph/internal/parser"
)

// Extract dispatches to the correct format extractor by file extension.
// absPath is the absolute on-disk path; relPath is the path relative to the
// project root (used as the canonical node ID prefix).  src is the raw file
// bytes; hash is the pre-computed SHA-256 hex string.
func Extract(absPath, relPath string, src []byte, hash string) (res *parser.ParseResult, err error) {
	// Untrusted documents are parsed by third-party libraries (notably
	// Detective-XH/pdf, which panics on malformed input — proven by
	// FuzzExtractPDF). Recover so a hostile file degrades to a skipped file
	// with an error, never a crash of the long-lived `serve` watcher process.
	defer func() {
		if r := recover(); r != nil {
			res = nil
			err = fmt.Errorf("extractor: recovered from panic parsing %q: %v", relPath, r)
		}
	}()
	ext := strings.ToLower(filepath.Ext(relPath))
	switch ext {
	case ".docx":
		return extractDOCX(absPath, relPath, src, hash)
	case ".html", ".htm":
		return extractHTML(absPath, relPath, src, hash)
	case ".pdf":
		return extractPDF(absPath, relPath, src, hash)
	default:
		return nil, fmt.Errorf("extractor: unsupported extension %q", ext)
	}
}

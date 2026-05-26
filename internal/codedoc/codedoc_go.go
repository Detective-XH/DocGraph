package codedoc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

func init() {
	RegisterExtractor("go", extractGo, ".go")
}

// extractGo extracts the documentation surface from a Go source file using go/parser.
// It returns CodeDocEntry values for the file header, exported doc comments,
// test functions, and example functions. Syntax errors are tolerated — an empty
// slice is returned rather than an error.
func extractGo(relPath string, src []byte) ([]CodeDocEntry, error) {
	isTestFile := strings.HasSuffix(relPath, "_test.go")
	fileType := FileTypeSource
	if isTestFile {
		fileType = FileTypeTest
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relPath, src, parser.ParseComments)
	if err != nil {
		// Be lenient — syntax errors are not fatal.
		return []CodeDocEntry{}, nil
	}

	var entries []CodeDocEntry

	// --- File header: package-level doc comment ---
	if file.Doc != nil {
		text := commentGroupText(file.Doc)
		startLine := fset.Position(file.Doc.Pos()).Line
		endLine := fset.Position(file.Doc.End()).Line
		entries = append(entries, CodeDocEntry{
			SymbolName:  "",
			CommentKind: KindFileHeader,
			HeadingPath: "File Header",
			Text:        text,
			StartLine:   startLine,
			EndLine:     endLine,
			FileType:    fileType,
			Lang:        "go",
		})
	}

	// --- Walk declarations ---
	for _, decl := range file.Decls {
		if len(entries) >= maxEntries {
			break
		}
		switch d := decl.(type) {
		case *ast.FuncDecl:
			entries = appendFuncEntry(entries, fset, d, fileType)
		case *ast.GenDecl:
			entries = appendGenDeclEntries(entries, fset, d, fileType)
		}
	}

	return entries, nil
}

// appendFuncEntry handles a function declaration.
func appendFuncEntry(entries []CodeDocEntry, fset *token.FileSet, d *ast.FuncDecl, fileType string) []CodeDocEntry {
	if len(entries) >= maxEntries {
		return entries
	}
	name := d.Name.Name

	// Detect Test/Example functions first (they override the exported-doc rule).
	if strings.HasPrefix(name, "Test") && name != "Test" {
		text := ""
		if d.Doc != nil {
			text = commentGroupText(d.Doc)
		}
		startLine, endLine := declLines(fset, d.Pos(), d.End(), d.Doc)
		return append(entries, CodeDocEntry{
			SymbolName:  name,
			CommentKind: KindTestFunc,
			HeadingPath: "Tests > " + name,
			Text:        text,
			StartLine:   startLine,
			EndLine:     endLine,
			FileType:    FileTypeTest,
			Lang:        "go",
		})
	}

	if strings.HasPrefix(name, "Example") && name != "Example" {
		text := ""
		if d.Doc != nil {
			text = commentGroupText(d.Doc)
		}
		startLine, endLine := declLines(fset, d.Pos(), d.End(), d.Doc)
		return append(entries, CodeDocEntry{
			SymbolName:  name,
			CommentKind: KindExampleFunc,
			HeadingPath: "Examples > " + name,
			Text:        text,
			StartLine:   startLine,
			EndLine:     endLine,
			FileType:    fileType,
			Lang:        "go",
		})
	}

	// Exported doc comment: exported name + has a doc comment.
	if ast.IsExported(name) && d.Doc != nil {
		text := commentGroupText(d.Doc)
		startLine, endLine := declLines(fset, d.Pos(), d.End(), d.Doc)
		return append(entries, CodeDocEntry{
			SymbolName:  name,
			CommentKind: KindDocComment,
			HeadingPath: "DocComment > " + name,
			Text:        text,
			StartLine:   startLine,
			EndLine:     endLine,
			FileType:    fileType,
			Lang:        "go",
		})
	}

	return entries
}

// appendGenDeclEntries handles type/const/var declarations.
func appendGenDeclEntries(entries []CodeDocEntry, fset *token.FileSet, d *ast.GenDecl, fileType string) []CodeDocEntry {
	for _, spec := range d.Specs {
		if len(entries) >= maxEntries {
			break
		}
		switch s := spec.(type) {
		case *ast.TypeSpec:
			// Prefer spec-level Doc; fall back to GenDecl Doc.
			doc := s.Doc
			if doc == nil {
				doc = d.Doc
			}
			name := s.Name.Name
			if ast.IsExported(name) && doc != nil {
				text := commentGroupText(doc)
				// Use the spec's own position to avoid nodeID collisions in grouped decls.
				startLine, endLine := declLines(fset, s.Pos(), s.End(), doc)
				entries = append(entries, CodeDocEntry{
					SymbolName:  name,
					CommentKind: KindDocComment,
					HeadingPath: "DocComment > " + name,
					Text:        text,
					StartLine:   startLine,
					EndLine:     endLine,
					FileType:    fileType,
					Lang:        "go",
				})
			}
		case *ast.ValueSpec:
			// For const/var blocks: each spec may have its own doc.
			doc := s.Doc
			if doc == nil && len(d.Specs) == 1 {
				// Single-spec decl: fall back to GenDecl Doc.
				doc = d.Doc
			}
			if doc == nil {
				continue
			}
			for _, ident := range s.Names {
				if len(entries) >= maxEntries {
					break
				}
				name := ident.Name
				if ast.IsExported(name) {
					text := commentGroupText(doc)
					// Use spec position to avoid nodeID collisions in grouped var/const decls.
					startLine, endLine := declLines(fset, s.Pos(), s.End(), doc)
					entries = append(entries, CodeDocEntry{
						SymbolName:  name,
						CommentKind: KindDocComment,
						HeadingPath: "DocComment > " + name,
						Text:        text,
						StartLine:   startLine,
						EndLine:     endLine,
						FileType:    fileType,
						Lang:        "go",
					})
				}
			}
		}
	}
	return entries
}

// commentGroupText returns the plain text of a comment group (strips // and /* */).
func commentGroupText(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	// Use CommentGroup.Text() which normalises // and /* */ comments.
	return strings.TrimSpace(cg.Text())
}

// declLines computes start/end lines for an entry.
// If a doc comment is present, the start line includes the comment.
func declLines(fset *token.FileSet, declPos, declEnd token.Pos, doc *ast.CommentGroup) (startLine, endLine int) {
	startPos := declPos
	if doc != nil && doc.Pos() < declPos {
		startPos = doc.Pos()
	}
	return fset.Position(startPos).Line, fset.Position(declEnd).Line
}

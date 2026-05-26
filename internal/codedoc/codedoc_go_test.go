package codedoc

import (
	"testing"
)

// findEntries returns all entries with the given CommentKind from a slice.
func findEntries(entries []CodeDocEntry, kind string) []CodeDocEntry {
	var out []CodeDocEntry
	for _, e := range entries {
		if e.CommentKind == kind {
			out = append(out, e)
		}
	}
	return out
}

// findBySymbol finds the first entry with the given SymbolName.
func findBySymbol(entries []CodeDocEntry, sym string) (CodeDocEntry, bool) {
	for _, e := range entries {
		if e.SymbolName == sym {
			return e, true
		}
	}
	return CodeDocEntry{}, false
}

// TestExtractGo_FileHeader verifies that a package-level doc comment becomes
// a KindFileHeader entry with an empty SymbolName.
func TestExtractGo_FileHeader(t *testing.T) {
	src := []byte(`// Package foo is a great package.
// It does many things.
package foo
`)
	entries, err := extractGo("foo.go", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	headers := findEntries(entries, KindFileHeader)
	if len(headers) != 1 {
		t.Fatalf("want 1 file_header entry, got %d", len(headers))
	}
	h := headers[0]
	if h.SymbolName != "" {
		t.Errorf("file_header SymbolName should be empty, got %q", h.SymbolName)
	}
	if h.HeadingPath != "File Header" {
		t.Errorf("HeadingPath = %q, want %q", h.HeadingPath, "File Header")
	}
	if h.Lang != "go" {
		t.Errorf("Lang = %q, want %q", h.Lang, "go")
	}
	if h.Text == "" {
		t.Error("expected non-empty Text for file header")
	}
	if h.StartLine < 1 {
		t.Errorf("StartLine = %d, want >= 1", h.StartLine)
	}
}

// TestExtractGo_NoFileHeader verifies that a file without a package doc comment
// produces no KindFileHeader entry.
func TestExtractGo_NoFileHeader(t *testing.T) {
	src := []byte(`package foo

func Foo() {}
`)
	entries, err := extractGo("foo.go", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	headers := findEntries(entries, KindFileHeader)
	if len(headers) != 0 {
		t.Errorf("want 0 file_header entries, got %d", len(headers))
	}
}

// TestExtractGo_TestFunc verifies that a TestXxx function yields a KindTestFunc
// entry with FileType=test and the correct HeadingPath.
func TestExtractGo_TestFunc(t *testing.T) {
	src := []byte(`package foo_test

import "testing"

// TestFoo tests the Foo function.
func TestFoo(t *testing.T) {}
`)
	entries, err := extractGo("foo_test.go", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tests := findEntries(entries, KindTestFunc)
	if len(tests) != 1 {
		t.Fatalf("want 1 test_func entry, got %d", len(tests))
	}
	e := tests[0]
	if e.SymbolName != "TestFoo" {
		t.Errorf("SymbolName = %q, want %q", e.SymbolName, "TestFoo")
	}
	if e.HeadingPath != "Tests > TestFoo" {
		t.Errorf("HeadingPath = %q, want %q", e.HeadingPath, "Tests > TestFoo")
	}
	if e.FileType != FileTypeTest {
		t.Errorf("FileType = %q, want %q", e.FileType, FileTypeTest)
	}
	if e.Lang != "go" {
		t.Errorf("Lang = %q, want %q", e.Lang, "go")
	}
}

// TestExtractGo_TestFuncNoDoc verifies that a test function without a doc comment
// still produces a KindTestFunc entry (Text may be empty).
func TestExtractGo_TestFuncNoDoc(t *testing.T) {
	src := []byte(`package foo_test

import "testing"

func TestBar(t *testing.T) {}
`)
	entries, err := extractGo("foo_test.go", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tests := findEntries(entries, KindTestFunc)
	if len(tests) != 1 {
		t.Fatalf("want 1 test_func entry, got %d", len(tests))
	}
	if tests[0].SymbolName != "TestBar" {
		t.Errorf("SymbolName = %q, want %q", tests[0].SymbolName, "TestBar")
	}
}

// TestExtractGo_ExampleFunc verifies that an ExampleXxx function yields a
// KindExampleFunc entry.
func TestExtractGo_ExampleFunc(t *testing.T) {
	src := []byte(`package foo_test

// ExampleFoo shows how to call Foo.
func ExampleFoo() {}
`)
	entries, err := extractGo("foo_test.go", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	examples := findEntries(entries, KindExampleFunc)
	if len(examples) != 1 {
		t.Fatalf("want 1 example_func entry, got %d", len(examples))
	}
	e := examples[0]
	if e.SymbolName != "ExampleFoo" {
		t.Errorf("SymbolName = %q, want %q", e.SymbolName, "ExampleFoo")
	}
	if e.HeadingPath != "Examples > ExampleFoo" {
		t.Errorf("HeadingPath = %q, want %q", e.HeadingPath, "Examples > ExampleFoo")
	}
}

// TestExtractGo_ExampleFunc_SourceFile verifies that an Example function in a
// non-test file still gets KindExampleFunc (FileType reflects the source file).
func TestExtractGo_ExampleFunc_SourceFile(t *testing.T) {
	src := []byte(`package foo

// ExampleBar shows Bar usage.
func ExampleBar() {}
`)
	entries, err := extractGo("foo.go", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	examples := findEntries(entries, KindExampleFunc)
	if len(examples) != 1 {
		t.Fatalf("want 1 example_func entry, got %d", len(examples))
	}
	if examples[0].FileType != FileTypeSource {
		t.Errorf("FileType = %q, want %q", examples[0].FileType, FileTypeSource)
	}
}

// TestExtractGo_DocComment verifies that an exported function with a doc comment
// yields a KindDocComment entry.
func TestExtractGo_DocComment(t *testing.T) {
	src := []byte(`package foo

// Bar does something useful.
func Bar() {}
`)
	entries, err := extractGo("foo.go", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	docs := findEntries(entries, KindDocComment)
	if len(docs) != 1 {
		t.Fatalf("want 1 doc_comment entry, got %d", len(docs))
	}
	e := docs[0]
	if e.SymbolName != "Bar" {
		t.Errorf("SymbolName = %q, want %q", e.SymbolName, "Bar")
	}
	if e.HeadingPath != "DocComment > Bar" {
		t.Errorf("HeadingPath = %q, want %q", e.HeadingPath, "DocComment > Bar")
	}
	if e.FileType != FileTypeSource {
		t.Errorf("FileType = %q, want %q", e.FileType, FileTypeSource)
	}
}

// TestExtractGo_DocComment_Type verifies that exported type declarations with
// doc comments produce KindDocComment entries.
func TestExtractGo_DocComment_Type(t *testing.T) {
	src := []byte(`package foo

// Widget is the main widget type.
type Widget struct {
	Name string
}
`)
	entries, err := extractGo("foo.go", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e, ok := findBySymbol(entries, "Widget")
	if !ok {
		t.Fatal("expected KindDocComment entry for Widget, not found")
	}
	if e.CommentKind != KindDocComment {
		t.Errorf("CommentKind = %q, want %q", e.CommentKind, KindDocComment)
	}
}

// TestExtractGo_UnexportedFunc verifies that unexported functions are NOT extracted,
// even if they have a doc comment.
func TestExtractGo_UnexportedFunc(t *testing.T) {
	src := []byte(`package foo

// foo does private things.
func foo() {}
`)
	entries, err := extractGo("foo.go", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	docs := findEntries(entries, KindDocComment)
	if len(docs) != 0 {
		t.Errorf("want 0 doc_comment entries for unexported func, got %d", len(docs))
	}
}

// TestExtractGo_ExportedNoDoc verifies that an exported function WITHOUT a doc
// comment is NOT extracted as a KindDocComment entry.
func TestExtractGo_ExportedNoDoc(t *testing.T) {
	src := []byte(`package foo

func Baz() {}
`)
	entries, err := extractGo("foo.go", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	docs := findEntries(entries, KindDocComment)
	if len(docs) != 0 {
		t.Errorf("want 0 doc_comment entries for exported func without doc, got %d", len(docs))
	}
}

// TestExtractGo_SyntaxError verifies that a file with a syntax error returns
// an empty slice and no error (lenient mode).
func TestExtractGo_SyntaxError(t *testing.T) {
	src := []byte(`package foo

this is not valid Go {{{
`)
	entries, err := extractGo("bad.go", src)
	if err != nil {
		t.Fatalf("expected no error for syntax error, got: %v", err)
	}
	if entries == nil {
		t.Fatal("expected non-nil (empty) slice, got nil")
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for broken file, got %d", len(entries))
	}
}

// TestExtractGo_FileType_TestFile verifies that _test.go files get FileType=test.
func TestExtractGo_FileType_TestFile(t *testing.T) {
	src := []byte(`package foo_test

import "testing"

func TestAlpha(t *testing.T) {}
`)
	entries, err := extractGo("alpha_test.go", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}
	for _, e := range entries {
		if e.FileType != FileTypeTest {
			t.Errorf("entry %q has FileType=%q, want %q", e.SymbolName, e.FileType, FileTypeTest)
		}
	}
}

// TestExtractGo_FileType_SourceFile verifies that non-_test.go files get FileType=source.
func TestExtractGo_FileType_SourceFile(t *testing.T) {
	src := []byte(`package foo

// Gamma does gamma things.
func Gamma() {}
`)
	entries, err := extractGo("gamma.go", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range entries {
		if e.FileType != FileTypeSource {
			t.Errorf("entry %q has FileType=%q, want %q", e.SymbolName, e.FileType, FileTypeSource)
		}
	}
}

// TestExtractGo_Lang verifies that all entries have Lang="go".
func TestExtractGo_Lang(t *testing.T) {
	src := []byte(`// Package bar tests lang field.
package bar

// Exported is an exported func.
func Exported() {}
`)
	entries, err := extractGo("bar.go", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range entries {
		if e.Lang != "go" {
			t.Errorf("entry %q has Lang=%q, want %q", e.SymbolName, e.Lang, "go")
		}
	}
}

// TestExtractGo_Lines verifies that StartLine and EndLine are set and plausible.
func TestExtractGo_Lines(t *testing.T) {
	src := []byte(`// Package lines tests line numbers.
package lines

// Func1 is a function.
func Func1() {}
`)
	entries, err := extractGo("lines.go", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range entries {
		if e.StartLine < 1 {
			t.Errorf("entry %q StartLine=%d, want >= 1", e.SymbolName, e.StartLine)
		}
		if e.EndLine < e.StartLine {
			t.Errorf("entry %q EndLine=%d < StartLine=%d", e.SymbolName, e.EndLine, e.StartLine)
		}
	}
}

// TestExtractGo_MultipleDecls verifies extraction from a file with mixed exports
// and unexported identifiers.
func TestExtractGo_MultipleDecls(t *testing.T) {
	src := []byte(`// Package multi has multiple declarations.
package multi

// Public is exported.
func Public() {}

// private is not exported.
func private() {}

// Also is exported.
func Also() {}
`)
	entries, err := extractGo("multi.go", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	docs := findEntries(entries, KindDocComment)
	if len(docs) != 2 {
		t.Fatalf("want 2 doc_comment entries, got %d", len(docs))
	}
	syms := map[string]bool{}
	for _, e := range docs {
		syms[e.SymbolName] = true
	}
	if !syms["Public"] {
		t.Error("expected Public in doc_comment entries")
	}
	if !syms["Also"] {
		t.Error("expected Also in doc_comment entries")
	}
	if syms["private"] {
		t.Error("private should not appear in doc_comment entries")
	}
}

// TestExtractGo_HeadingPaths verifies the HeadingPath format for all kinds.
func TestExtractGo_HeadingPaths(t *testing.T) {
	src := []byte(`// Package paths tests heading paths.
package paths

import "testing"

// Exported is exported.
func Exported() {}

func TestSomething(t *testing.T) {}

func ExampleSomething() {}
`)
	entries, err := extractGo("paths_test.go", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Collect by kind.
	byKind := map[string][]CodeDocEntry{}
	for _, e := range entries {
		byKind[e.CommentKind] = append(byKind[e.CommentKind], e)
	}

	if hdr := byKind[KindFileHeader]; len(hdr) != 1 || hdr[0].HeadingPath != "File Header" {
		t.Errorf("file_header HeadingPath = %v", byKind[KindFileHeader])
	}
	if tests := byKind[KindTestFunc]; len(tests) != 1 || tests[0].HeadingPath != "Tests > TestSomething" {
		t.Errorf("test_func HeadingPath = %v", byKind[KindTestFunc])
	}
	if exs := byKind[KindExampleFunc]; len(exs) != 1 || exs[0].HeadingPath != "Examples > ExampleSomething" {
		t.Errorf("example_func HeadingPath = %v", byKind[KindExampleFunc])
	}
	if docs := byKind[KindDocComment]; len(docs) != 1 || docs[0].HeadingPath != "DocComment > Exported" {
		t.Errorf("doc_comment HeadingPath = %v", byKind[KindDocComment])
	}
}

// TestExtractGo_EmptyFile verifies that an empty (but valid) Go file returns
// an empty slice without error.
func TestExtractGo_EmptyFile(t *testing.T) {
	src := []byte(`package empty
`)
	entries, err := extractGo("empty.go", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("want 0 entries for empty file, got %d", len(entries))
	}
}

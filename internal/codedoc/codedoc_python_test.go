package codedoc

import (
	"strings"
	"testing"
)

// ──────────────────────────────────────────────────────────────────────────────
// Python tests
// ──────────────────────────────────────────────────────────────────────────────

func TestPython_ModuleDocstring_FileHeader(t *testing.T) {
	src := `"""Module for computing things."""

def compute(x):
    return x * 2
`
	entries, err := extractPython("mymodule.py", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := pyRubyFindEntry(entries, KindFileHeader, "")
	if e == nil {
		t.Fatal("expected KindFileHeader entry, got none")
	}
	if e.HeadingPath != "File Header" {
		t.Errorf("HeadingPath = %q, want %q", e.HeadingPath, "File Header")
	}
	if e.Lang != "python" {
		t.Errorf("Lang = %q, want %q", e.Lang, "python")
	}
	if e.FileType != FileTypeSource {
		t.Errorf("FileType = %q, want %q", e.FileType, FileTypeSource)
	}
	if !strings.Contains(e.Text, "computing things") {
		t.Errorf("Text %q does not contain expected content", e.Text)
	}
}

func TestPython_ModuleDocstring_SingleQuotes(t *testing.T) {
	src := `'''Single-quote module docstring.'''

x = 1
`
	entries, err := extractPython("mymodule.py", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := pyRubyFindEntry(entries, KindFileHeader, "")
	if e == nil {
		t.Fatal("expected KindFileHeader for single-quote triple docstring, got none")
	}
	if !strings.Contains(e.Text, "Single-quote module docstring") {
		t.Errorf("Text %q does not contain expected content", e.Text)
	}
}

func TestPython_ModuleDocstring_SkipsShebangAndEncoding(t *testing.T) {
	src := `#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Module with shebang and encoding declaration."""

def foo():
    pass
`
	entries, err := extractPython("mymodule.py", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := pyRubyFindEntry(entries, KindFileHeader, "")
	if e == nil {
		t.Fatal("expected KindFileHeader after shebang/encoding lines, got none")
	}
	if !strings.Contains(e.Text, "shebang and encoding") {
		t.Errorf("Text %q does not contain expected content", e.Text)
	}
}

func TestPython_TestFunc_WithDocstring(t *testing.T) {
	src := `def test_foo():
    """Test that foo works."""
    assert True
`
	entries, err := extractPython("test_mymodule.py", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := pyRubyFindEntry(entries, KindTestFunc, "test_foo")
	if e == nil {
		t.Fatal("expected KindTestFunc entry for test_foo, got none")
	}
	if e.HeadingPath != "Tests > test_foo" {
		t.Errorf("HeadingPath = %q, want %q", e.HeadingPath, "Tests > test_foo")
	}
	if e.FileType != FileTypeTest {
		t.Errorf("FileType = %q, want %q", e.FileType, FileTypeTest)
	}
	if !strings.Contains(e.Text, "Test that foo works") {
		t.Errorf("Text %q does not contain expected content", e.Text)
	}
}

func TestPython_TestFunc_NoDocstring(t *testing.T) {
	src := `def test_bare():
    assert True
`
	entries, err := extractPython("test_mymodule.py", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := pyRubyFindEntry(entries, KindTestFunc, "test_bare")
	if e == nil {
		t.Fatal("expected KindTestFunc for test_bare even without docstring")
	}
	if e.Text != "" {
		t.Errorf("Text = %q, want empty for test func without docstring", e.Text)
	}
}

func TestPython_Class_WithDocstring(t *testing.T) {
	src := `class Foo:
    """Foo does things."""
    pass
`
	entries, err := extractPython("mymodule.py", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := pyRubyFindEntry(entries, KindDocComment, "Foo")
	if e == nil {
		t.Fatal("expected KindDocComment entry for class Foo, got none")
	}
	if e.HeadingPath != "DocComment > Foo" {
		t.Errorf("HeadingPath = %q, want %q", e.HeadingPath, "DocComment > Foo")
	}
	if !strings.Contains(e.Text, "Foo does things") {
		t.Errorf("Text %q does not contain expected content", e.Text)
	}
}

func TestPython_TestClass(t *testing.T) {
	src := `class TestFoo:
    """Tests for Foo."""
    pass
`
	entries, err := extractPython("test_mymodule.py", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := pyRubyFindEntry(entries, KindTestFunc, "TestFoo")
	if e == nil {
		t.Fatal("expected KindTestFunc for class TestFoo, got none")
	}
	if e.HeadingPath != "Tests > TestFoo" {
		t.Errorf("HeadingPath = %q, want %q", e.HeadingPath, "Tests > TestFoo")
	}
}

func TestPython_FuncWithoutDocstring_NotExtracted(t *testing.T) {
	src := `def helper():
    return 42
`
	entries, err := extractPython("mymodule.py", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range entries {
		if e.CommentKind == KindDocComment && e.SymbolName == "helper" {
			t.Error("did not expect KindDocComment for function without docstring")
		}
	}
}

func TestPython_FuncWithDocstring(t *testing.T) {
	src := `def compute(x):
    """Compute the result for x."""
    return x * 2
`
	entries, err := extractPython("mymodule.py", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := pyRubyFindEntry(entries, KindDocComment, "compute")
	if e == nil {
		t.Fatal("expected KindDocComment for compute, got none")
	}
	if !strings.Contains(e.Text, "Compute the result") {
		t.Errorf("Text %q does not contain expected content", e.Text)
	}
}

func TestPython_DecoratorBeforeDef(t *testing.T) {
	src := `@staticmethod
def decorated():
    """A decorated function."""
    pass
`
	entries, err := extractPython("mymodule.py", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := pyRubyFindEntry(entries, KindDocComment, "decorated")
	if e == nil {
		t.Fatal("expected KindDocComment for decorated function, got none")
	}
	if !strings.Contains(e.Text, "decorated function") {
		t.Errorf("Text %q does not contain expected content", e.Text)
	}
}

func TestPython_FileType_TestFile(t *testing.T) {
	src := `def test_something():
    pass
`
	entries, err := extractPython("test_something.py", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}
	for _, e := range entries {
		if e.FileType != FileTypeTest {
			t.Errorf("entry %q: FileType = %q, want %q", e.SymbolName, e.FileType, FileTypeTest)
		}
	}
}

func TestPython_FileType_SuffixTestFile(t *testing.T) {
	src := `def something():
    """Does something."""
    pass
`
	entries, err := extractPython("mymodule_test.py", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}
	for _, e := range entries {
		if e.FileType != FileTypeTest {
			t.Errorf("entry %q: FileType = %q, want %q", e.SymbolName, e.FileType, FileTypeTest)
		}
	}
}

func TestPython_MultipleSymbols(t *testing.T) {
	src := `"""Module docstring."""

class Foo:
    """Foo class docstring."""

    def method(self):
        """Method docstring."""
        pass

def test_foo():
    assert True
`
	entries, err := extractPython("test_mymodule.py", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	header := pyRubyFindEntry(entries, KindFileHeader, "")
	if header == nil {
		t.Error("expected KindFileHeader for module docstring")
	}

	fooDoc := pyRubyFindEntry(entries, KindDocComment, "Foo")
	if fooDoc == nil {
		t.Error("expected KindDocComment for class Foo")
	}

	methodDoc := pyRubyFindEntry(entries, KindDocComment, "method")
	if methodDoc == nil {
		t.Error("expected KindDocComment for method")
	}

	testFunc := pyRubyFindEntry(entries, KindTestFunc, "test_foo")
	if testFunc == nil {
		t.Error("expected KindTestFunc for test_foo")
	}
}

func TestPython_EmptyFile(t *testing.T) {
	entries, err := extractPython("mymodule.py", []byte(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no entries for empty file, got %d", len(entries))
	}
}

func TestPython_CommentOnlyFile(t *testing.T) {
	src := `# comment
# only
`
	entries, err := extractPython("mymodule.py", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no entries for comment-only file (no docstrings), got %d", len(entries))
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Ruby tests
// ──────────────────────────────────────────────────────────────────────────────

func TestRuby_FileHeader_LeadingHashComments(t *testing.T) {
	src := `# comment
# lines

class Foo
  def bar
  end
end
`
	entries, err := extractRuby("mymodule.rb", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := pyRubyFindEntry(entries, KindFileHeader, "")
	if e == nil {
		t.Fatal("expected KindFileHeader from leading hash comments, got none")
	}
	if e.HeadingPath != "File Header" {
		t.Errorf("HeadingPath = %q, want %q", e.HeadingPath, "File Header")
	}
	if e.Lang != "ruby" {
		t.Errorf("Lang = %q, want %q", e.Lang, "ruby")
	}
	if e.FileType != FileTypeSource {
		t.Errorf("FileType = %q, want %q", e.FileType, FileTypeSource)
	}
}

func TestRuby_FileHeader_WithShebang(t *testing.T) {
	src := `#!/usr/bin/env ruby
# desc
`
	entries, err := extractRuby("mymodule.rb", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := pyRubyFindEntry(entries, KindFileHeader, "")
	if e == nil {
		t.Fatal("expected KindFileHeader after shebang, got none")
	}
	if !strings.Contains(e.Text, "desc") {
		t.Errorf("Text %q does not contain expected content", e.Text)
	}
}

func TestRuby_TestMethod_DefTestSomething(t *testing.T) {
	src := `def test_something
  assert true
end
`
	entries, err := extractRuby("test_mymodule.rb", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := pyRubyFindEntry(entries, KindTestFunc, "test_something")
	if e == nil {
		t.Fatal("expected KindTestFunc for test_something, got none")
	}
	if e.HeadingPath != "Tests > test_something" {
		t.Errorf("HeadingPath = %q, want %q", e.HeadingPath, "Tests > test_something")
	}
}

func TestRuby_ItBlock(t *testing.T) {
	src := `describe 'MyClass' do
  it 'does something useful' do
    expect(true).to be true
  end
end
`
	entries, err := extractRuby("mymodule_spec.rb", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	descEntry := pyRubyFindEntry(entries, KindTestFunc, "MyClass")
	if descEntry == nil {
		t.Error("expected KindTestFunc for describe 'MyClass'")
	}

	itEntry := pyRubyFindEntry(entries, KindTestFunc, "does something useful")
	if itEntry == nil {
		t.Error("expected KindTestFunc for it 'does something useful'")
	}
}

func TestRuby_MethodWithComment_DocComment(t *testing.T) {
	src := `# Computes the result.
def compute(x)
  x * 2
end
`
	entries, err := extractRuby("mymodule.rb", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := pyRubyFindEntry(entries, KindDocComment, "compute")
	if e == nil {
		t.Fatal("expected KindDocComment for compute, got none")
	}
	if !strings.Contains(e.Text, "Computes the result") {
		t.Errorf("Text %q does not contain expected content", e.Text)
	}
}

func TestRuby_MethodWithoutComment_NotExtracted(t *testing.T) {
	src := `def helper
  42
end
`
	entries, err := extractRuby("mymodule.rb", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range entries {
		if e.CommentKind == KindDocComment && e.SymbolName == "helper" {
			t.Error("did not expect KindDocComment for method without preceding comment")
		}
	}
}

func TestRuby_FileType_SpecFile(t *testing.T) {
	src := `it 'passes' do
  expect(true).to be true
end
`
	entries, err := extractRuby("foo_spec.rb", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}
	for _, e := range entries {
		if e.FileType != FileTypeTest {
			t.Errorf("entry %q: FileType = %q, want %q", e.SymbolName, e.FileType, FileTypeTest)
		}
	}
}

func TestRuby_FileType_TestFile(t *testing.T) {
	src := `def test_something
  assert true
end
`
	entries, err := extractRuby("foo_test.rb", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}
	for _, e := range entries {
		if e.FileType != FileTypeTest {
			t.Errorf("entry %q: FileType = %q, want %q", e.SymbolName, e.FileType, FileTypeTest)
		}
	}
}

func TestRuby_EmptyFile(t *testing.T) {
	entries, err := extractRuby("mymodule.rb", []byte(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no entries for empty file, got %d", len(entries))
	}
}

func TestRuby_MultipleMethodsWithComments(t *testing.T) {
	src := `#!/usr/bin/env ruby
# My Ruby module description.

# Computes a value.
def compute(x)
  x * 2
end

# Validates input.
def validate(x)
  x > 0
end
`
	entries, err := extractRuby("mymodule.rb", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	header := pyRubyFindEntry(entries, KindFileHeader, "")
	if header == nil {
		t.Error("expected KindFileHeader from leading comments after shebang")
	}

	compute := pyRubyFindEntry(entries, KindDocComment, "compute")
	if compute == nil {
		t.Error("expected KindDocComment for compute")
	}

	validate := pyRubyFindEntry(entries, KindDocComment, "validate")
	if validate == nil {
		t.Error("expected KindDocComment for validate")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Helper
// ──────────────────────────────────────────────────────────────────────────────

func pyRubyFindEntry(entries []CodeDocEntry, kind, symbolName string) *CodeDocEntry {
	for i := range entries {
		if entries[i].CommentKind == kind && entries[i].SymbolName == symbolName {
			return &entries[i]
		}
	}
	return nil
}

package codedoc

import (
	"testing"
)

// ──────────────────────────────────────────────────────────────────────────────
// Lua
// ──────────────────────────────────────────────────────────────────────────────

func TestLuaLongBracketHeader(t *testing.T) {
	src := `--[[
This is the file header.
It spans multiple lines.
]]

function foo()
end
`
	entries, err := extractLua("my_module.lua", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hdr := findKind(entries, KindFileHeader)
	if hdr == nil {
		t.Fatal("expected a KindFileHeader entry")
	}
	if hdr.HeadingPath != "File Header" {
		t.Errorf("HeadingPath = %q, want %q", hdr.HeadingPath, "File Header")
	}
	if hdr.Lang != "lua" {
		t.Errorf("Lang = %q, want %q", hdr.Lang, "lua")
	}
	if hdr.SymbolName != "" {
		t.Errorf("SymbolName = %q, want empty for file header", hdr.SymbolName)
	}
}

func TestLuaSingleLineHeader(t *testing.T) {
	src := `-- My Lua module
-- Does useful things.

function bar()
end
`
	entries, err := extractLua("my_module.lua", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hdr := findKind(entries, KindFileHeader)
	if hdr == nil {
		t.Fatal("expected a KindFileHeader entry")
	}
	if hdr.HeadingPath != "File Header" {
		t.Errorf("HeadingPath = %q, want %q", hdr.HeadingPath, "File Header")
	}
}

func TestLuaDocCommentBeforeFunction(t *testing.T) {
	src := `-- Returns the sum of a and b.
-- Both arguments must be numbers.
function foo(a, b)
  return a + b
end
`
	entries, err := extractLua("math.lua", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The leading comment should be captured as the file header first (it is
	// at the top and preceded by nothing). However extractLuaLang processes
	// line-by-line for doc comments, and the leading -- block is also captured
	// as a file header. Since both paths fire, look for a doc_comment OR
	// check that the first -- block before function foo is wired correctly.
	//
	// The file header extractor runs first and consumes the block; the
	// line-by-line pass then sees an empty commentBuf when it hits "function foo".
	// So if the only comment is at the very top, it becomes a file header.
	// Use a file with a separate function-preceding comment to test doc_comment.
	src2 := `-- File header comment.

-- Returns the sum.
function foo(a, b)
  return a + b
end
`
	entries2, err := extractLua("math.lua", []byte(src2))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := findKindSymbol(entries2, KindDocComment, "foo")
	if doc == nil {
		t.Fatal("expected KindDocComment for symbol 'foo'")
	}
	if doc.SymbolName != "foo" {
		t.Errorf("SymbolName = %q, want %q", doc.SymbolName, "foo")
	}
	if doc.HeadingPath != "DocComment > foo" {
		t.Errorf("HeadingPath = %q, want %q", doc.HeadingPath, "DocComment > foo")
	}
	if doc.Lang != "lua" {
		t.Errorf("Lang = %q, want %q", doc.Lang, "lua")
	}
	_ = entries
}

func TestLuaLocalFunctionDocComment(t *testing.T) {
	src := `-- Private helper.
local function helper(x)
  return x * 2
end
`
	entries, err := extractLua("util.lua", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The leading comment will be the file header; local function has no
	// preceding comment in this snippet, so use a two-block source.
	src2 := `-- Module header.

-- Doubles a value.
local function double(x)
  return x * 2
end
`
	entries2, err := extractLua("util.lua", []byte(src2))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := findKindSymbol(entries2, KindDocComment, "double")
	if doc == nil {
		t.Fatalf("expected KindDocComment for 'double'")
	}
	_ = entries
}

func TestLuaItTestFunc(t *testing.T) {
	src := `describe('MyModule', function()
  it('should work', function()
    assert.is_true(true)
  end)
end)
`
	entries, err := extractLua("my_spec.lua", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should find at least the it('should work', …) as KindTestFunc.
	test := findKindSymbol(entries, KindTestFunc, "should work")
	if test == nil {
		t.Fatal("expected KindTestFunc for 'should work'")
	}
	if test.HeadingPath != "Tests > should work" {
		t.Errorf("HeadingPath = %q, want %q", test.HeadingPath, "Tests > should work")
	}
}

func TestLuaDescribeTestFunc(t *testing.T) {
	src := `describe('Calculator', function()
  it('adds numbers', function()
    assert.equals(2, 1+1)
  end)
end)
`
	entries, err := extractLua("calc_spec.lua", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	desc := findKindSymbol(entries, KindTestFunc, "Calculator")
	if desc == nil {
		t.Fatal("expected KindTestFunc for 'Calculator'")
	}
}

func TestLuaFileTypeTest(t *testing.T) {
	src := `it('should pass', function() end)
`
	entries, err := extractLua("foo_spec.lua", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range entries {
		if e.FileType != FileTypeTest {
			t.Errorf("expected FileType=%q, got %q", FileTypeTest, e.FileType)
		}
	}
}

func TestLuauExtractor(t *testing.T) {
	// extractLuau and extractLua share the same logic; ensure registration works.
	src := `--[[
Luau module header.
]]

function greet(name: string): string
  return "Hello " .. name
end
`
	entries, err := extractLuau("module.luau", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hdr := findKind(entries, KindFileHeader)
	if hdr == nil {
		t.Fatal("expected KindFileHeader for luau file")
	}
	if hdr.Lang != "luau" {
		t.Errorf("Lang = %q, want %q", hdr.Lang, "luau")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Pascal
// ──────────────────────────────────────────────────────────────────────────────

func TestPascalBraceHeader(t *testing.T) {
	src := `{ Unit: MathUtils
  Description: Helper math routines. }

procedure Add(a, b: Integer);
begin
end;
`
	entries, err := extractPascal("mathutils.pas", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hdr := findKind(entries, KindFileHeader)
	if hdr == nil {
		t.Fatal("expected KindFileHeader from brace comment")
	}
	if hdr.HeadingPath != "File Header" {
		t.Errorf("HeadingPath = %q, want %q", hdr.HeadingPath, "File Header")
	}
	if hdr.Lang != "pascal" {
		t.Errorf("Lang = %q, want %q", hdr.Lang, "pascal")
	}
}

func TestPascalStarCommentBeforeProcedure(t *testing.T) {
	src := `{ File header. }

(* Prints the value of X to stdout. *)
procedure PrintX(X: Integer);
begin
  WriteLn(X);
end;
`
	entries, err := extractPascal("io.pas", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := findKindSymbol(entries, KindDocComment, "PrintX")
	if doc == nil {
		t.Fatal("expected KindDocComment for 'PrintX'")
	}
	if doc.SymbolName != "PrintX" {
		t.Errorf("SymbolName = %q, want %q", doc.SymbolName, "PrintX")
	}
	if doc.HeadingPath != "DocComment > PrintX" {
		t.Errorf("HeadingPath = %q, want %q", doc.HeadingPath, "DocComment > PrintX")
	}
}

func TestPascalBraceCommentBeforeFunction(t *testing.T) {
	src := `{ Module header. }

{ Computes the square of N. }
function Square(N: Integer): Integer;
begin
  Result := N * N;
end;
`
	entries, err := extractPascal("math.pas", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := findKindSymbol(entries, KindDocComment, "Square")
	if doc == nil {
		t.Fatal("expected KindDocComment for 'Square'")
	}
}

func TestPascalFileTypeSource(t *testing.T) {
	src := `{ Header. }
procedure Foo;
begin
end;
`
	entries, err := extractPascal("foo.pas", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range entries {
		if e.FileType != FileTypeSource {
			t.Errorf("expected FileType=%q, got %q", FileTypeSource, e.FileType)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// SQL
// ──────────────────────────────────────────────────────────────────────────────

func TestSQLFileHeader(t *testing.T) {
	src := `-- Database: warehouse
-- Author: team-data
-- Version: 1.0

CREATE TABLE products (id INT);
`
	entries, err := extractSQL("schema.sql", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hdr := findKind(entries, KindFileHeader)
	if hdr == nil {
		t.Fatal("expected KindFileHeader from leading -- block")
	}
	if hdr.HeadingPath != "File Header" {
		t.Errorf("HeadingPath = %q, want %q", hdr.HeadingPath, "File Header")
	}
	if hdr.Lang != "sql" {
		t.Errorf("Lang = %q, want %q", hdr.Lang, "sql")
	}
}

func TestSQLDocCommentBeforeCreateProcedure(t *testing.T) {
	src := `-- Schema setup.

-- Looks up a user by ID and returns their name.
CREATE PROCEDURE sp_foo
AS
BEGIN
  SELECT name FROM users WHERE id = @id;
END
`
	entries, err := extractSQL("procs.sql", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := findKindSymbol(entries, KindDocComment, "sp_foo")
	if doc == nil {
		t.Fatal("expected KindDocComment for 'sp_foo'")
	}
	if doc.SymbolName != "sp_foo" {
		t.Errorf("SymbolName = %q, want %q", doc.SymbolName, "sp_foo")
	}
	if doc.HeadingPath != "DocComment > sp_foo" {
		t.Errorf("HeadingPath = %q, want %q", doc.HeadingPath, "DocComment > sp_foo")
	}
}

func TestSQLDocCommentBeforeCreateView(t *testing.T) {
	src := `-- Returns active sessions.
CREATE VIEW v_active_sessions AS SELECT * FROM sessions WHERE active = 1;
`
	entries, err := extractSQL("views.sql", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := findKindSymbol(entries, KindDocComment, "v_active_sessions")
	if doc == nil {
		t.Fatal("expected KindDocComment for 'v_active_sessions'")
	}
}

func TestSQLDocCommentBeforeCreateFunction(t *testing.T) {
	src := `-- Computes tax amount.
CREATE FUNCTION fn_tax(@amount DECIMAL) RETURNS DECIMAL
AS BEGIN
  RETURN @amount * 0.1;
END
`
	entries, err := extractSQL("funcs.sql", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := findKindSymbol(entries, KindDocComment, "fn_tax")
	if doc == nil {
		t.Fatal("expected KindDocComment for 'fn_tax'")
	}
}

func TestSQLDocCommentBeforeCreateTable(t *testing.T) {
	src := `-- Stores product catalog entries.
CREATE TABLE products (id INT PRIMARY KEY, name TEXT);
`
	entries, err := extractSQL("tables.sql", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := findKindSymbol(entries, KindDocComment, "products")
	if doc == nil {
		t.Fatal("expected KindDocComment for 'products'")
	}
}

func TestSQLFileTypeSource(t *testing.T) {
	src := `-- Header.
CREATE TABLE t (id INT);
`
	entries, err := extractSQL("t.sql", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range entries {
		if e.FileType != FileTypeSource {
			t.Errorf("expected FileType=%q, got %q", FileTypeSource, e.FileType)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Liquid
// ──────────────────────────────────────────────────────────────────────────────

func TestLiquidFileHeader(t *testing.T) {
	src := `{% comment %}
This is the file header for the product template.
Renders the main product page.
{% endcomment %}

<h1>{{ product.title }}</h1>
`
	entries, err := extractLiquid("product.liquid", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hdr := findKind(entries, KindFileHeader)
	if hdr == nil {
		t.Fatal("expected KindFileHeader from leading {% comment %} block")
	}
	if hdr.HeadingPath != "File Header" {
		t.Errorf("HeadingPath = %q, want %q", hdr.HeadingPath, "File Header")
	}
	if hdr.Lang != "liquid" {
		t.Errorf("Lang = %q, want %q", hdr.Lang, "liquid")
	}
	if hdr.SymbolName != "" {
		t.Errorf("SymbolName = %q, want empty", hdr.SymbolName)
	}
}

func TestLiquidDocCommentBlock(t *testing.T) {
	src := `{% comment %}File header.{% endcomment %}

<p>{{ page.title }}</p>

{% comment %}
Section: footer links.
{% endcomment %}
<footer>...</footer>
`
	entries, err := extractLiquid("page.liquid", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 entries, got %d", len(entries))
	}
	// First block → file header.
	if entries[0].CommentKind != KindFileHeader {
		t.Errorf("first entry kind = %q, want %q", entries[0].CommentKind, KindFileHeader)
	}
	// Second block → doc comment.
	if entries[1].CommentKind != KindDocComment {
		t.Errorf("second entry kind = %q, want %q", entries[1].CommentKind, KindDocComment)
	}
	if entries[1].HeadingPath != "DocComment" {
		t.Errorf("second entry HeadingPath = %q, want %q", entries[1].HeadingPath, "DocComment")
	}
}

func TestLiquidDashVariants(t *testing.T) {
	// Liquid also allows {%- comment -%} with whitespace-stripping dashes.
	src := `{%- comment -%}
Dash variant header.
{%- endcomment -%}
`
	entries, err := extractLiquid("dashes.liquid", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hdr := findKind(entries, KindFileHeader)
	if hdr == nil {
		t.Fatal("expected KindFileHeader from {%- comment -%} block")
	}
}

func TestLiquidFileTypeSource(t *testing.T) {
	src := `{% comment %}Header.{% endcomment %}`
	entries, err := extractLiquid("t.liquid", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range entries {
		if e.FileType != FileTypeSource {
			t.Errorf("expected FileType=%q, got %q", FileTypeSource, e.FileType)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Registration
// ──────────────────────────────────────────────────────────────────────────────

func TestMiscExtractorsRegistered(t *testing.T) {
	expected := []string{".lua", ".luau", ".pas", ".pp", ".sql", ".liquid"}
	for _, ext := range expected {
		if _, ok := extractors[ext]; !ok {
			t.Errorf("extractor for %q not registered", ext)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

func findKind(entries []CodeDocEntry, kind string) *CodeDocEntry {
	for i := range entries {
		if entries[i].CommentKind == kind {
			return &entries[i]
		}
	}
	return nil
}

func findKindSymbol(entries []CodeDocEntry, kind, symbol string) *CodeDocEntry {
	for i := range entries {
		if entries[i].CommentKind == kind && entries[i].SymbolName == symbol {
			return &entries[i]
		}
	}
	return nil
}

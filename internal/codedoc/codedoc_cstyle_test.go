package codedoc

import (
	"strings"
	"testing"
)

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

func mustExtractCStyle(t *testing.T, lang, relPath string, src []byte) []CodeDocEntry {
	t.Helper()
	entries, err := extractCStyle(lang, relPath, src)
	if err != nil {
		t.Fatalf("extractCStyle(%q): unexpected error: %v", lang, err)
	}
	return entries
}

// ──────────────────────────────────────────────────────────────────────────────
// Rust
// ──────────────────────────────────────────────────────────────────────────────

func TestCStyle_Rust_TestFunc(t *testing.T) {
	src := []byte(`
fn add(a: i32, b: i32) -> i32 { a + b }

#[test]
fn test_add() {
    assert_eq!(add(1, 2), 3);
}
`)
	entries := mustExtractCStyle(t, "rust", "src/lib.rs", src)
	tests := findEntries(entries, KindTestFunc)
	if len(tests) == 0 {
		t.Fatal("expected at least one KindTestFunc entry for Rust #[test]")
	}
	found := false
	for _, e := range tests {
		if e.SymbolName == "test_add" {
			found = true
			if e.Lang != "rust" {
				t.Errorf("Lang = %q, want %q", e.Lang, "rust")
			}
			if !strings.HasPrefix(e.HeadingPath, "Tests > ") {
				t.Errorf("HeadingPath = %q, want prefix %q", e.HeadingPath, "Tests > ")
			}
			break
		}
	}
	if !found {
		t.Errorf("no KindTestFunc with SymbolName=test_add; got: %+v", tests)
	}
}

func TestCStyle_Rust_DocComment(t *testing.T) {
	// /// is always a doc comment for the NEXT item, never a file header.
	src := []byte(`/// Adds two numbers together.
///
/// Returns the sum of a and b.
pub fn add(a: i32, b: i32) -> i32 { a + b }
`)
	entries := mustExtractCStyle(t, "rust", "src/lib.rs", src)

	// Must not be captured as a file header.
	headers := findEntries(entries, KindFileHeader)
	if len(headers) != 0 {
		t.Errorf("/// before pub fn should NOT produce KindFileHeader; got %d", len(headers))
	}

	// Must be a doc comment for "add".
	docs := findEntries(entries, KindDocComment)
	if len(docs) == 0 {
		t.Fatal("expected KindDocComment from Rust /// block before pub fn")
	}
	e, ok := findBySymbol(docs, "add")
	if !ok {
		t.Errorf("KindDocComment entries: %+v", docs)
		return
	}
	if !strings.Contains(e.Text, "Adds two numbers") {
		t.Errorf("Text = %q, expected to contain 'Adds two numbers'", e.Text)
	}
	if e.Lang != "rust" {
		t.Errorf("Lang = %q, want rust", e.Lang)
	}
}

func TestCStyle_Rust_DocComment_BeforeExportedFn(t *testing.T) {
	src := []byte(`fn internal() {}

/// Multiplies two numbers.
pub fn multiply(a: i32, b: i32) -> i32 { a * b }
`)
	entries := mustExtractCStyle(t, "rust", "src/math.rs", src)
	docs := findEntries(entries, KindDocComment)
	if len(docs) == 0 {
		t.Fatal("expected KindDocComment for Rust /// before pub fn")
	}
	e, ok := findBySymbol(docs, "multiply")
	if !ok {
		t.Errorf("no KindDocComment with SymbolName=multiply; entries: %+v", docs)
		return
	}
	if !strings.Contains(e.Text, "Multiplies") {
		t.Errorf("Text = %q, expected to contain 'Multiplies'", e.Text)
	}
}

func TestCStyle_Rust_ExampleFunc(t *testing.T) {
	// A doc comment containing "# Examples" before a pub fn should produce KindExampleFunc.
	src := []byte("/// Adds two numbers.\n///\n/// # Examples\n///\n/// let result = add(1, 2);\n/// assert_eq!(result, 3);\npub fn add(a: i32, b: i32) -> i32 { a + b }\n")
	entries := mustExtractCStyle(t, "rust", "src/lib.rs", src)
	examples := findEntries(entries, KindExampleFunc)
	if len(examples) == 0 {
		t.Fatalf("expected KindExampleFunc from Rust /// doc containing '# Examples'; entries: %+v", entries)
	}
	e := examples[0]
	if e.Lang != "rust" {
		t.Errorf("Lang = %q, want rust", e.Lang)
	}
	if !strings.HasPrefix(e.HeadingPath, "Examples > ") {
		t.Errorf("HeadingPath = %q, want prefix 'Examples > '", e.HeadingPath)
	}
}

func TestCStyle_Rust_FileType_TestFile(t *testing.T) {
	src := []byte(`#[test]
fn test_foo() {}
`)
	entries := mustExtractCStyle(t, "rust", "src/foo_test.rs", src)
	tests := findEntries(entries, KindTestFunc)
	if len(tests) == 0 {
		t.Fatal("expected KindTestFunc in _test.rs file")
	}
	for _, e := range tests {
		if e.FileType != FileTypeTest {
			t.Errorf("FileType = %q, want %q for _test.rs", e.FileType, FileTypeTest)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Java
// ──────────────────────────────────────────────────────────────────────────────

func TestCStyle_Java_TestFunc(t *testing.T) {
	src := []byte(`import org.junit.Test;

public class CalculatorTest {
    @Test
    public void testAddition() {
        assertEquals(4, 2 + 2);
    }
}
`)
	entries := mustExtractCStyle(t, "java", "src/test/java/CalculatorTest.java", src)
	tests := findEntries(entries, KindTestFunc)
	if len(tests) == 0 {
		t.Fatal("expected KindTestFunc for Java @Test method")
	}
	found := false
	for _, e := range tests {
		if strings.Contains(e.SymbolName, "testAddition") || strings.Contains(e.HeadingPath, "testAddition") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no KindTestFunc with SymbolName containing 'testAddition'; got: %+v", tests)
	}
}

func TestCStyle_Java_TestFileType_SrcTest(t *testing.T) {
	src := []byte(`public class FooTest {
    @Test
    public void testFoo() {}
}
`)
	entries := mustExtractCStyle(t, "java", "src/test/java/FooTest.java", src)
	tests := findEntries(entries, KindTestFunc)
	if len(tests) == 0 {
		t.Fatal("expected at least one KindTestFunc for Java @Test in src/test/")
	}
	// File in src/test/ should be FileTypeTest.
	for _, e := range tests {
		if e.FileType != FileTypeTest {
			t.Errorf("FileType = %q for src/test/ file, want %q", e.FileType, FileTypeTest)
		}
	}
}

func TestCStyle_Java_ParameterizedTest(t *testing.T) {
	src := []byte(`
@ParameterizedTest
public void testParams(int x) {}
`)
	entries := mustExtractCStyle(t, "java", "src/test/FooTest.java", src)
	tests := findEntries(entries, KindTestFunc)
	if len(tests) == 0 {
		t.Fatal("expected KindTestFunc for Java @ParameterizedTest")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Swift
// ──────────────────────────────────────────────────────────────────────────────

func TestCStyle_Swift_TestFunc(t *testing.T) {
	src := []byte(`import XCTest

class MyTests: XCTestCase {
    func testBar() {
        XCTAssert(true)
    }
}
`)
	entries := mustExtractCStyle(t, "swift", "MyTests.swift", src)
	tests := findEntries(entries, KindTestFunc)
	if len(tests) == 0 {
		t.Fatal("expected KindTestFunc for Swift func testBar()")
	}
	e, ok := findBySymbol(tests, "testBar")
	if !ok {
		t.Errorf("no KindTestFunc with SymbolName=testBar; got: %+v", tests)
		return
	}
	if e.Lang != "swift" {
		t.Errorf("Lang = %q, want swift", e.Lang)
	}
}

func TestCStyle_Swift_TestFileType(t *testing.T) {
	src := []byte(`import XCTest

class FooTests: XCTestCase {
    func testSomething() {
        XCTAssert(true)
    }
}
`)
	entries := mustExtractCStyle(t, "swift", "FooTests.swift", src)
	tests := findEntries(entries, KindTestFunc)
	if len(tests) == 0 {
		t.Fatal("expected KindTestFunc in FooTests.swift")
	}
	for _, e := range tests {
		if e.FileType != FileTypeTest {
			t.Errorf("FileType = %q for Tests.swift, want %q", e.FileType, FileTypeTest)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// C
// ──────────────────────────────────────────────────────────────────────────────

func TestCStyle_C_BlockCommentFileHeader(t *testing.T) {
	src := []byte(`/* utils.c — utility functions for the project.
 * Copyright 2024 Example Corp.
 * SPDX-License-Identifier: MIT
 */

#include <stdio.h>

int add(int a, int b) { return a + b; }
`)
	entries := mustExtractCStyle(t, "c", "src/utils.c", src)
	headers := findEntries(entries, KindFileHeader)
	if len(headers) == 0 {
		t.Fatal("expected KindFileHeader from leading /* ... */ in C file")
	}
	h := headers[0]
	if h.Lang != "c" {
		t.Errorf("Lang = %q, want c", h.Lang)
	}
	if h.HeadingPath != "File Header" {
		t.Errorf("HeadingPath = %q, want 'File Header'", h.HeadingPath)
	}
	if h.SymbolName != "" {
		t.Errorf("SymbolName should be empty for file header, got %q", h.SymbolName)
	}
	if !strings.Contains(h.Text, "utils") {
		t.Errorf("Text %q should mention 'utils'", h.Text)
	}
}

func TestCStyle_C_SlashSlashFileHeader(t *testing.T) {
	src := []byte(`// myfile.c — does useful things.
// Author: Test Author

int foo(void) { return 0; }
`)
	entries := mustExtractCStyle(t, "c", "myfile.c", src)
	headers := findEntries(entries, KindFileHeader)
	if len(headers) == 0 {
		t.Fatal("expected KindFileHeader from leading // block in C file")
	}
	h := headers[0]
	if h.StartLine < 1 {
		t.Errorf("StartLine = %d, want >= 1", h.StartLine)
	}
	if h.EndLine < h.StartLine {
		t.Errorf("EndLine %d < StartLine %d", h.EndLine, h.StartLine)
	}
}

func TestCStyle_C_TestFileType_NameContainsTest(t *testing.T) {
	src := []byte(`/* test helper */
int test_helper() { return 0; }
`)
	entries := mustExtractCStyle(t, "c", "test_utils.c", src)
	// The file header should always be extracted from the leading /* ... */ block.
	headers := findEntries(entries, KindFileHeader)
	if len(headers) == 0 {
		t.Fatal("expected KindFileHeader for C file with leading /* */ block")
	}
	for _, e := range entries {
		if e.FileType != FileTypeTest {
			t.Errorf("FileType = %q for test_utils.c, want %q", e.FileType, FileTypeTest)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// C#
// ──────────────────────────────────────────────────────────────────────────────

func TestCStyle_CSharp_FactAttribute(t *testing.T) {
	src := []byte(`using Xunit;

public class CalculatorTests
{
    [Fact]
    public void TestAddition()
    {
        Assert.Equal(4, 2 + 2);
    }
}
`)
	entries := mustExtractCStyle(t, "csharp", "CalculatorTests.cs", src)
	tests := findEntries(entries, KindTestFunc)
	if len(tests) == 0 {
		t.Fatal("expected KindTestFunc for C# [Fact] attribute")
	}
	found := false
	for _, e := range tests {
		if strings.Contains(e.SymbolName, "TestAddition") || strings.Contains(e.HeadingPath, "TestAddition") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no test with SymbolName/HeadingPath=TestAddition; got: %+v", tests)
	}
}

func TestCStyle_CSharp_TheoryAttribute(t *testing.T) {
	src := []byte(`
[Theory]
public void TestMultiply(int x, int y) {}
`)
	entries := mustExtractCStyle(t, "csharp", "MathTests.cs", src)
	tests := findEntries(entries, KindTestFunc)
	if len(tests) == 0 {
		t.Fatal("expected KindTestFunc for C# [Theory]")
	}
}

func TestCStyle_CSharp_TestMethodAttribute(t *testing.T) {
	src := []byte(`
[TestMethod]
public void TestSubtract() {}
`)
	entries := mustExtractCStyle(t, "csharp", "CalcTests.cs", src)
	tests := findEntries(entries, KindTestFunc)
	if len(tests) == 0 {
		t.Fatal("expected KindTestFunc for C# [TestMethod]")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Dart
// ──────────────────────────────────────────────────────────────────────────────

func TestCStyle_Dart_TestCall(t *testing.T) {
	src := []byte(`import 'package:test/test.dart';

void main() {
  test('addition works', () {
    expect(1 + 1, equals(2));
  });
}
`)
	entries := mustExtractCStyle(t, "dart", "test/math_test.dart", src)
	tests := findEntries(entries, KindTestFunc)
	if len(tests) == 0 {
		t.Fatal("expected KindTestFunc from Dart test('...', ...)")
	}
	e := tests[0]
	if e.SymbolName != "addition works" {
		t.Errorf("SymbolName = %q, want %q", e.SymbolName, "addition works")
	}
	if e.Lang != "dart" {
		t.Errorf("Lang = %q, want dart", e.Lang)
	}
	if !strings.HasPrefix(e.HeadingPath, "Tests > ") {
		t.Errorf("HeadingPath = %q, want prefix 'Tests > '", e.HeadingPath)
	}
}

func TestCStyle_Dart_TestWidgetsCall(t *testing.T) {
	src := []byte(`
void main() {
  testWidgets('renders correctly', (WidgetTester tester) async {
    // ...
  });
}
`)
	entries := mustExtractCStyle(t, "dart", "test/widget_test.dart", src)
	tests := findEntries(entries, KindTestFunc)
	if len(tests) == 0 {
		t.Fatal("expected KindTestFunc from Dart testWidgets('...', ...)")
	}
	e := tests[0]
	if e.SymbolName != "renders correctly" {
		t.Errorf("SymbolName = %q, want %q", e.SymbolName, "renders correctly")
	}
}

func TestCStyle_Dart_FileType_TestDir(t *testing.T) {
	src := []byte(`void main() {
  test('foo', () {});
}
`)
	entries := mustExtractCStyle(t, "dart", "test/foo_test.dart", src)
	tests := findEntries(entries, KindTestFunc)
	if len(tests) == 0 {
		t.Fatal("expected KindTestFunc for Dart test() in test/ directory")
	}
	for _, e := range tests {
		if e.FileType != FileTypeTest {
			t.Errorf("FileType = %q for test/foo_test.dart, want %q", e.FileType, FileTypeTest)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Kotlin
// ──────────────────────────────────────────────────────────────────────────────

func TestCStyle_Kotlin_TestFunc(t *testing.T) {
	src := []byte(`import org.junit.Test

class CalculatorTest {
    @Test
    fun testAddition() {
        assertEquals(4, 2 + 2)
    }
}
`)
	entries := mustExtractCStyle(t, "kotlin", "src/test/CalculatorTest.kt", src)
	tests := findEntries(entries, KindTestFunc)
	if len(tests) == 0 {
		t.Fatal("expected KindTestFunc for Kotlin @Test fun")
	}
}

func TestCStyle_Kotlin_TestFileType_ClassSuffix(t *testing.T) {
	src := []byte(`class MySpec {
    @Test
    fun testFoo() {}
}
`)
	entries := mustExtractCStyle(t, "kotlin", "MySpec.kt", src)
	tests := findEntries(entries, KindTestFunc)
	if len(tests) == 0 {
		t.Fatal("expected KindTestFunc for Kotlin class ending with 'Spec'")
	}
	for _, e := range tests {
		if e.FileType != FileTypeTest {
			t.Errorf("FileType = %q for class ending with Spec, want %q", e.FileType, FileTypeTest)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// PHP
// ──────────────────────────────────────────────────────────────────────────────

func TestCStyle_PHP_TestMethod_Prefix(t *testing.T) {
	src := []byte(`<?php

class MathTest extends TestCase {
    public function testAddition() {
        $this->assertEquals(4, 2 + 2);
    }
}
`)
	entries := mustExtractCStyle(t, "php", "tests/MathTest.php", src)
	tests := findEntries(entries, KindTestFunc)
	if len(tests) == 0 {
		t.Fatal("expected KindTestFunc for PHP function testXxx()")
	}
}

func TestCStyle_PHP_FileHeader_AfterOpenTag(t *testing.T) {
	src := []byte(`<?php
/**
 * My PHP library header.
 * Provides useful utilities.
 */

function doSomething() {}
`)
	entries := mustExtractCStyle(t, "php", "lib/utils.php", src)
	headers := findEntries(entries, KindFileHeader)
	if len(headers) == 0 {
		t.Fatal("expected KindFileHeader from /** ... */ after <?php tag")
	}
	if !strings.Contains(headers[0].Text, "PHP library") {
		t.Errorf("Text = %q, expected to contain 'PHP library'", headers[0].Text)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// CPP
// ──────────────────────────────────────────────────────────────────────────────

func TestCStyle_CPP_FileHeader(t *testing.T) {
	src := []byte(`/**
 * vector_math.cpp — SIMD vector operations.
 * Part of the math library.
 */
#include "vector_math.h"

float dot(float* a, float* b, int n) { return 0.0f; }
`)
	entries := mustExtractCStyle(t, "cpp", "src/vector_math.cpp", src)
	headers := findEntries(entries, KindFileHeader)
	if len(headers) == 0 {
		t.Fatal("expected KindFileHeader from C++ leading /** block")
	}
	if headers[0].Lang != "cpp" {
		t.Errorf("Lang = %q, want cpp", headers[0].Lang)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Registration via init() — verify extensions are registered
// ──────────────────────────────────────────────────────────────────────────────

func TestCStyle_ExtractorRegistration(t *testing.T) {
	expectedExts := []string{
		".rs",
		".c", ".cc", ".h",
		".cpp", ".cxx", ".hpp", ".hh",
		".java",
		".swift",
		".cs",
		".php",
		".kt", ".kts",
		".dart",
	}
	for _, ext := range expectedExts {
		if _, ok := extractors[ext]; !ok {
			t.Errorf("extension %q not registered by C-style init()", ext)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Any-file: leading // block → KindFileHeader
// ──────────────────────────────────────────────────────────────────────────────

func TestCStyle_AnyLang_SlashSlashHeader(t *testing.T) {
	langs := []struct {
		lang    string
		relPath string
	}{
		{"java", "src/Foo.java"},
		{"swift", "Foo.swift"},
		{"csharp", "Foo.cs"},
		{"php", "Foo.php"},
		{"kotlin", "Foo.kt"},
		{"dart", "Foo.dart"},
		{"c", "foo.c"},
		{"cpp", "foo.cpp"},
	}
	src := []byte(`// Copyright 2024 Example Corp.
// This file is part of the project.

int main() { return 0; }
`)
	for _, tc := range langs {
		t.Run(tc.lang, func(t *testing.T) {
			entries := mustExtractCStyle(t, tc.lang, tc.relPath, src)
			headers := findEntries(entries, KindFileHeader)
			if len(headers) == 0 {
				t.Fatalf("lang=%s: expected KindFileHeader from leading // block", tc.lang)
			}
			if headers[0].HeadingPath != "File Header" {
				t.Errorf("lang=%s: HeadingPath = %q, want 'File Header'", tc.lang, headers[0].HeadingPath)
			}
		})
	}
}

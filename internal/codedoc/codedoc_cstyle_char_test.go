package codedoc

// Characterization tests for extractSymbol and detectFileType.
// These tests pin the exact current behavior of both functions so that any
// predicate-widening or regex-slot swap during the gocyclo refactor causes a
// test failure rather than a silent behavior change.
//
// The tests are named TestExtractSymbol_* and TestDetectFileType_* so they can
// be run in isolation with:
//
//	go test -race -count=1 -run 'ExtractSymbol|DetectFileType' ./internal/codedoc/...

import (
	"testing"
)

// ──────────────────────────────────────────────────────────────────────────────
// extractSymbol characterization tests
//
// One positive func-match and one positive class-match per language so that any
// wrong regex assignment in a future config table causes a failure.
// ──────────────────────────────────────────────────────────────────────────────

func TestExtractSymbol_Rust_Func(t *testing.T) {
	if got := extractSymbol("rust", "pub fn my_func(x: i32) -> i32 {"); got != "my_func" {
		t.Errorf("rust func: got %q, want %q", got, "my_func")
	}
}

func TestExtractSymbol_Rust_UnsafeFunc(t *testing.T) {
	if got := extractSymbol("rust", "pub unsafe fn dangerous() {"); got != "dangerous" {
		t.Errorf("rust unsafe fn: got %q, want %q", got, "dangerous")
	}
}

func TestExtractSymbol_Rust_Class(t *testing.T) {
	if got := extractSymbol("rust", "class MyStruct {"); got != "MyStruct" {
		t.Errorf("rust class: got %q, want %q", got, "MyStruct")
	}
}

func TestExtractSymbol_Swift_Func(t *testing.T) {
	if got := extractSymbol("swift", "func testBar() {"); got != "testBar" {
		t.Errorf("swift func: got %q, want %q", got, "testBar")
	}
}

func TestExtractSymbol_Swift_Class(t *testing.T) {
	if got := extractSymbol("swift", "class MyView: UIView {"); got != "MyView" {
		t.Errorf("swift class: got %q, want %q", got, "MyView")
	}
}

func TestExtractSymbol_Kotlin_Func(t *testing.T) {
	if got := extractSymbol("kotlin", "fun testAddition() {"); got != "testAddition" {
		t.Errorf("kotlin fun: got %q, want %q", got, "testAddition")
	}
}

func TestExtractSymbol_Kotlin_Class(t *testing.T) {
	if got := extractSymbol("kotlin", "class CalculatorTest {"); got != "CalculatorTest" {
		t.Errorf("kotlin class: got %q, want %q", got, "CalculatorTest")
	}
}

func TestExtractSymbol_Java_Method(t *testing.T) {
	// Java uses javaMethodRe which requires access modifier + return type + name.
	if got := extractSymbol("java", "public void testAddition() {"); got != "testAddition" {
		t.Errorf("java method: got %q, want %q", got, "testAddition")
	}
}

func TestExtractSymbol_Java_Class(t *testing.T) {
	if got := extractSymbol("java", "class CalculatorTest {"); got != "CalculatorTest" {
		t.Errorf("java class: got %q, want %q", got, "CalculatorTest")
	}
}

func TestExtractSymbol_Java_PlainFunc_NoMatch(t *testing.T) {
	// A bare "func" keyword does not match Java; javaMethodRe needs access modifier.
	// A line with no modifier and no class should return empty.
	if got := extractSymbol("java", "someMethod() {"); got != "" {
		t.Errorf("java plain func without modifier: got %q, want empty", got)
	}
}

func TestExtractSymbol_CSharp_Method(t *testing.T) {
	// C# uses javaMethodRe (same regex as Java).
	if got := extractSymbol("csharp", "public void TestSubtract() {"); got != "TestSubtract" {
		t.Errorf("csharp method: got %q, want %q", got, "TestSubtract")
	}
}

func TestExtractSymbol_CSharp_Class(t *testing.T) {
	if got := extractSymbol("csharp", "class CalculatorTests {"); got != "CalculatorTests" {
		t.Errorf("csharp class: got %q, want %q", got, "CalculatorTests")
	}
}

func TestExtractSymbol_PHP_Func(t *testing.T) {
	if got := extractSymbol("php", "public function testAddition() {"); got != "testAddition" {
		t.Errorf("php func: got %q, want %q", got, "testAddition")
	}
}

func TestExtractSymbol_PHP_Class(t *testing.T) {
	if got := extractSymbol("php", "class MathTest extends TestCase {"); got != "MathTest" {
		t.Errorf("php class: got %q, want %q", got, "MathTest")
	}
}

func TestExtractSymbol_Dart_Func(t *testing.T) {
	// Dart uses funcRe (\bfunc\s+...) — same regex as Swift.
	if got := extractSymbol("dart", "func myWidget() {"); got != "myWidget" {
		t.Errorf("dart func: got %q, want %q", got, "myWidget")
	}
}

func TestExtractSymbol_Dart_Class(t *testing.T) {
	if got := extractSymbol("dart", "class MyWidget extends StatelessWidget {"); got != "MyWidget" {
		t.Errorf("dart class: got %q, want %q", got, "MyWidget")
	}
}

func TestExtractSymbol_C_Func(t *testing.T) {
	// C uses cFuncRe which requires return-type + name + (.
	if got := extractSymbol("c", "int add(int a, int b) {"); got != "add" {
		t.Errorf("c func: got %q, want %q", got, "add")
	}
}

func TestExtractSymbol_C_Class(t *testing.T) {
	if got := extractSymbol("c", "class Foo {"); got != "Foo" {
		t.Errorf("c class: got %q, want %q", got, "Foo")
	}
}

func TestExtractSymbol_CPP_Func(t *testing.T) {
	if got := extractSymbol("cpp", "float dot(float* a, float* b) {"); got != "dot" {
		t.Errorf("cpp func: got %q, want %q", got, "dot")
	}
}

func TestExtractSymbol_CPP_StaticFunc(t *testing.T) {
	if got := extractSymbol("cpp", "static inline int helper() {"); got != "helper" {
		t.Errorf("cpp static inline func: got %q, want %q", got, "helper")
	}
}

func TestExtractSymbol_Unknown_Lang(t *testing.T) {
	// An unknown language returns empty — no panic.
	if got := extractSymbol("haskell", "f x = x + 1"); got != "" {
		t.Errorf("unknown lang: got %q, want empty", got)
	}
}

// Swift/Dart share funcRe (\bfunc\s+); Java/C# share javaMethodRe.
// Verify the regexes are truly the same slot.

func TestExtractSymbol_SwiftDart_SameFuncRe(t *testing.T) {
	line := "func doThing() {}"
	swiftGot := extractSymbol("swift", line)
	dartGot := extractSymbol("dart", line)
	if swiftGot != dartGot {
		t.Errorf("swift and dart should use same funcRe: swift=%q dart=%q", swiftGot, dartGot)
	}
	if swiftGot != "doThing" {
		t.Errorf("expected doThing, got %q", swiftGot)
	}
}

func TestExtractSymbol_JavaCSharp_SameMethodRe(t *testing.T) {
	line := "public void processItem(int x) {"
	javaGot := extractSymbol("java", line)
	csGot := extractSymbol("csharp", line)
	if javaGot != csGot {
		t.Errorf("java and csharp should use same javaMethodRe: java=%q csharp=%q", javaGot, csGot)
	}
	if javaGot != "processItem" {
		t.Errorf("expected processItem, got %q", javaGot)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// detectFileType characterization tests
//
// Cases are designed to fail if any predicate is widened/narrowed. In particular,
// Dart HasPrefix("test/") is tested separately from Contains("/test/") to show
// that both branches are subsumed by the testDirSegments Contains("test/") loop.
// ──────────────────────────────────────────────────────────────────────────────

func TestDetectFileType_Dart_TestFileSuffix(t *testing.T) {
	cfg := langConfigs["dart"]
	// _test.dart suffix — caught by testFileSuffixes loop.
	got := detectFileType("dart", "lib/foo_test.dart", nil, cfg)
	if got != FileTypeTest {
		t.Errorf("dart _test.dart: got %q, want %q", got, FileTypeTest)
	}
}

func TestDetectFileType_Dart_TestDir_Prefix(t *testing.T) {
	cfg := langConfigs["dart"]
	// "test/" prefix — caught by testDirSegments (Contains("test/")), NOT by Dart-branch HasPrefix.
	got := detectFileType("dart", "test/widget_test.dart", nil, cfg)
	if got != FileTypeTest {
		t.Errorf("dart test/ prefix: got %q, want %q", got, FileTypeTest)
	}
}

func TestDetectFileType_Dart_TestDir_MidPath(t *testing.T) {
	cfg := langConfigs["dart"]
	// "/test/" mid-path — caught by testDirSegments Contains("test/").
	got := detectFileType("dart", "packages/myapp/test/widget_test.dart", nil, cfg)
	if got != FileTypeTest {
		t.Errorf("dart /test/ midpath: got %q, want %q", got, FileTypeTest)
	}
}

func TestDetectFileType_Dart_Source(t *testing.T) {
	cfg := langConfigs["dart"]
	// src/mywidget.dart is source, not in test dir.
	got := detectFileType("dart", "lib/mywidget.dart", nil, cfg)
	if got != FileTypeSource {
		t.Errorf("dart source file: got %q, want %q", got, FileTypeSource)
	}
}

func TestDetectFileType_Rust_TestFileSuffix(t *testing.T) {
	cfg := langConfigs["rust"]
	got := detectFileType("rust", "src/foo_test.rs", nil, cfg)
	if got != FileTypeTest {
		t.Errorf("rust _test.rs: got %q, want %q", got, FileTypeTest)
	}
}

func TestDetectFileType_Rust_Source(t *testing.T) {
	cfg := langConfigs["rust"]
	got := detectFileType("rust", "src/lib.rs", nil, cfg)
	if got != FileTypeSource {
		t.Errorf("rust source: got %q, want %q", got, FileTypeSource)
	}
}

func TestDetectFileType_Java_TestDirSegment(t *testing.T) {
	cfg := langConfigs["java"]
	got := detectFileType("java", "src/test/java/FooTest.java", nil, cfg)
	if got != FileTypeTest {
		t.Errorf("java src/test/: got %q, want %q", got, FileTypeTest)
	}
}

func TestDetectFileType_Java_ClassSuffix(t *testing.T) {
	cfg := langConfigs["java"]
	lines := []string{"public class CalculatorTest {", "}"}
	got := detectFileType("java", "src/main/java/CalculatorTest.java", lines, cfg)
	if got != FileTypeTest {
		t.Errorf("java class suffix Test: got %q, want %q", got, FileTypeTest)
	}
}

func TestDetectFileType_Java_Source(t *testing.T) {
	cfg := langConfigs["java"]
	lines := []string{"public class Calculator {", "}"}
	got := detectFileType("java", "src/main/java/Calculator.java", lines, cfg)
	if got != FileTypeSource {
		t.Errorf("java source: got %q, want %q", got, FileTypeSource)
	}
}

func TestDetectFileType_C_FilenameContainsTest(t *testing.T) {
	cfg := langConfigs["c"]
	got := detectFileType("c", "test_utils.c", nil, cfg)
	if got != FileTypeTest {
		t.Errorf("c filename contains test: got %q, want %q", got, FileTypeTest)
	}
}

func TestDetectFileType_C_BasenameSplit(t *testing.T) {
	cfg := langConfigs["c"]
	// "test" appears only in the basename after the last /, not in the dir path.
	got := detectFileType("c", "src/mytest_helper.c", nil, cfg)
	if got != FileTypeTest {
		t.Errorf("c basename contains test: got %q, want %q", got, FileTypeTest)
	}
}

func TestDetectFileType_C_Source(t *testing.T) {
	cfg := langConfigs["c"]
	got := detectFileType("c", "src/utils.c", nil, cfg)
	if got != FileTypeSource {
		t.Errorf("c source: got %q, want %q", got, FileTypeSource)
	}
}

func TestDetectFileType_CSharp_TestFixture(t *testing.T) {
	cfg := langConfigs["csharp"]
	lines := []string{"[TestFixture]", "public class MyTests {", "}"}
	got := detectFileType("csharp", "src/MyTests.cs", lines, cfg)
	if got != FileTypeTest {
		t.Errorf("csharp [TestFixture]: got %q, want %q", got, FileTypeTest)
	}
}

func TestDetectFileType_CSharp_FileSuffix(t *testing.T) {
	cfg := langConfigs["csharp"]
	got := detectFileType("csharp", "src/MyTests.cs", nil, cfg)
	if got != FileTypeTest {
		t.Errorf("csharp tests.cs suffix: got %q, want %q", got, FileTypeTest)
	}
}

func TestDetectFileType_CSharp_Source(t *testing.T) {
	cfg := langConfigs["csharp"]
	lines := []string{"public class Calculator {", "}"}
	got := detectFileType("csharp", "src/Calculator.cs", lines, cfg)
	if got != FileTypeSource {
		t.Errorf("csharp source: got %q, want %q", got, FileTypeSource)
	}
}

func TestDetectFileType_Kotlin_ClassSuffixSpec(t *testing.T) {
	cfg := langConfigs["kotlin"]
	lines := []string{"class MySpec {", "}"}
	got := detectFileType("kotlin", "src/MySpec.kt", lines, cfg)
	if got != FileTypeTest {
		t.Errorf("kotlin class suffix Spec: got %q, want %q", got, FileTypeTest)
	}
}

func TestDetectFileType_Swift_TestFileSuffix(t *testing.T) {
	cfg := langConfigs["swift"]
	got := detectFileType("swift", "FooTests.swift", nil, cfg)
	if got != FileTypeTest {
		t.Errorf("swift tests.swift suffix: got %q, want %q", got, FileTypeTest)
	}
}

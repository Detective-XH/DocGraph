package codedoc

import (
	"strings"
	"testing"
)

// ---- helpers ----------------------------------------------------------------

func findEntry(t *testing.T, entries []CodeDocEntry, kind string) *CodeDocEntry {
	t.Helper()
	for i := range entries {
		if entries[i].CommentKind == kind {
			return &entries[i]
		}
	}
	return nil
}

func findEntryBySymbol(entries []CodeDocEntry, sym string) *CodeDocEntry {
	for i := range entries {
		if entries[i].SymbolName == sym {
			return &entries[i]
		}
	}
	return nil
}

func mustExtract(t *testing.T, relPath string, src string) []CodeDocEntry {
	t.Helper()
	var fn func(string, []byte) ([]CodeDocEntry, error)
	ext := strings.ToLower(relPath[strings.LastIndex(relPath, "."):])
	switch ext {
	case ".js", ".jsx":
		fn = extractJSTS
	case ".ts", ".tsx":
		fn = extractJSTS
	case ".svelte":
		fn = extractSvelte
	case ".vue":
		fn = extractVue
	default:
		t.Fatalf("no extractor for %q", relPath)
	}
	entries, err := fn(relPath, []byte(src))
	if err != nil {
		t.Fatalf("extract %q: %v", relPath, err)
	}
	return entries
}

// ---- JS file header ---------------------------------------------------------

func TestJS_FileHeader(t *testing.T) {
	src := `/**
 * Utility functions for date formatting.
 * @module date-utils
 */

export function formatDate(d) {
  return d.toISOString();
}
`
	entries := mustExtract(t, "src/date-utils.js", src)

	var header *CodeDocEntry
	for i := range entries {
		if entries[i].CommentKind == KindFileHeader {
			header = &entries[i]
			break
		}
	}
	if header == nil {
		t.Fatal("expected KindFileHeader entry, got none")
	}
	if header.HeadingPath != "File Header" {
		t.Errorf("HeadingPath = %q, want %q", header.HeadingPath, "File Header")
	}
	if !strings.Contains(header.Text, "Utility functions") {
		t.Errorf("Text %q does not contain expected content", header.Text)
	}
	if header.Lang != "javascript" {
		t.Errorf("Lang = %q, want javascript", header.Lang)
	}
	if header.FileType != FileTypeSource {
		t.Errorf("FileType = %q, want source", header.FileType)
	}
}

// ---- JS single-line JSDoc file header ---------------------------------------

func TestJS_FileHeader_SingleLine(t *testing.T) {
	src := `/** Module for auth helpers. */

function login() {}
`
	entries := mustExtract(t, "src/auth.js", src)
	h := findEntry(t, entries, KindFileHeader)
	if h == nil {
		t.Fatal("expected KindFileHeader")
	}
	if !strings.Contains(h.Text, "Module for auth helpers") {
		t.Errorf("Text = %q", h.Text)
	}
}

// ---- TS: first JSDoc (no preceding code) becomes KindFileHeader -------------

// TestTS_FirstJSDocBecomesFileHeader verifies that when a JSDoc block is the
// very first item in a TS file (before any code), it is classified as
// KindFileHeader — not KindDocComment — regardless of what follows it.
func TestTS_FirstJSDocBecomesFileHeader(t *testing.T) {
	src := `/**
 * Parses a user object from raw JSON.
 * @param raw - raw JSON string
 */
export function parseUser(raw: string): User {
  return JSON.parse(raw);
}
`
	entries := mustExtract(t, "src/parser.ts", src)

	// The first JSDoc becomes file header since it's at the top of the file.
	h := findEntry(t, entries, KindFileHeader)
	if h == nil {
		t.Fatal("expected KindFileHeader for first JSDoc in file")
	}
	if !strings.Contains(h.Text, "Parses a user") {
		t.Errorf("Text = %q", h.Text)
	}
	if h.Lang != "typescript" {
		t.Errorf("Lang = %q, want typescript", h.Lang)
	}
}

// ---- TS function doc comment (after file header) ----------------------------

func TestTS_DocComment_AfterFileHeader(t *testing.T) {
	src := `/**
 * Auth module.
 */

/**
 * Logs in a user.
 */
export function login(user: string): void {}

/**
 * Logs out a user.
 */
export const logout = (user: string) => {}
`
	entries := mustExtract(t, "src/auth.ts", src)

	loginEntry := findEntryBySymbol(entries, "login")
	if loginEntry == nil {
		t.Fatal("expected DocComment for 'login'")
	}
	if loginEntry.CommentKind != KindDocComment {
		t.Errorf("CommentKind = %q, want doc_comment", loginEntry.CommentKind)
	}
	if loginEntry.HeadingPath != "DocComment > login" {
		t.Errorf("HeadingPath = %q, want 'DocComment > login'", loginEntry.HeadingPath)
	}
	if !strings.Contains(loginEntry.Text, "Logs in") {
		t.Errorf("Text = %q", loginEntry.Text)
	}

	logoutEntry := findEntryBySymbol(entries, "logout")
	if logoutEntry == nil {
		t.Fatal("expected DocComment for 'logout'")
	}
	if logoutEntry.CommentKind != KindDocComment {
		t.Errorf("CommentKind = %q, want doc_comment", logoutEntry.CommentKind)
	}
}

// ---- TS class doc comment ---------------------------------------------------

func TestTS_ClassDocComment(t *testing.T) {
	src := `/**
 * App entry.
 */

/**
 * Manages user sessions.
 */
export class SessionManager {
  constructor() {}
}
`
	entries := mustExtract(t, "src/session.ts", src)
	e := findEntryBySymbol(entries, "SessionManager")
	if e == nil {
		t.Fatal("expected DocComment for 'SessionManager'")
	}
	if e.CommentKind != KindDocComment {
		t.Errorf("CommentKind = %q, want doc_comment", e.CommentKind)
	}
}

// ---- TS interface doc comment -----------------------------------------------

func TestTS_InterfaceDocComment(t *testing.T) {
	src := `/**
 * Types module.
 */

/**
 * Represents a user account.
 */
export interface UserAccount {
  id: string;
  name: string;
}
`
	entries := mustExtract(t, "src/types.ts", src)
	e := findEntryBySymbol(entries, "UserAccount")
	if e == nil {
		t.Fatal("expected DocComment for 'UserAccount'")
	}
	if e.CommentKind != KindDocComment {
		t.Errorf("CommentKind = %q, want doc_comment", e.CommentKind)
	}
}

// ---- TS type alias doc comment ----------------------------------------------

func TestTS_TypeAliasDocComment(t *testing.T) {
	src := `/**
 * Types module.
 */

/**
 * User ID type alias.
 */
type UserId = string;
`
	entries := mustExtract(t, "src/types.ts", src)
	e := findEntryBySymbol(entries, "UserId")
	if e == nil {
		t.Fatal("expected DocComment for 'UserId'")
	}
	if e.CommentKind != KindDocComment {
		t.Errorf("CommentKind = %q, want doc_comment", e.CommentKind)
	}
}

// ---- Test functions: describe -----------------------------------------------

func TestJS_DescribeBlock(t *testing.T) {
	src := `describe('Auth', () => {
  it('should login', () => {});
  it('should logout', () => {});
});
`
	entries := mustExtract(t, "auth.test.js", src)

	authEntry := findEntryBySymbol(entries, "Auth")
	if authEntry == nil {
		t.Fatal("expected KindTestFunc for 'Auth'")
	}
	if authEntry.CommentKind != KindTestFunc {
		t.Errorf("CommentKind = %q, want test_func", authEntry.CommentKind)
	}
	if authEntry.HeadingPath != "Tests > Auth" {
		t.Errorf("HeadingPath = %q, want 'Tests > Auth'", authEntry.HeadingPath)
	}

	loginEntry := findEntryBySymbol(entries, "should login")
	if loginEntry == nil {
		t.Fatal("expected KindTestFunc for 'should login'")
	}
	if loginEntry.HeadingPath != "Tests > should login" {
		t.Errorf("HeadingPath = %q, want 'Tests > should login'", loginEntry.HeadingPath)
	}
}

// ---- Test functions: it -----------------------------------------------------

func TestJS_ItBlock(t *testing.T) {
	src := `it('should login successfully', () => {
  expect(login()).toBe(true);
});
`
	entries := mustExtract(t, "login.test.js", src)
	e := findEntryBySymbol(entries, "should login successfully")
	if e == nil {
		t.Fatal("expected KindTestFunc for 'should login successfully'")
	}
	if e.CommentKind != KindTestFunc {
		t.Errorf("CommentKind = %q, want test_func", e.CommentKind)
	}
}

// ---- Test functions: test ---------------------------------------------------

func TestJS_TestBlock(t *testing.T) {
	src := `test('returns correct value', () => {
  expect(add(1, 2)).toBe(3);
});
`
	entries := mustExtract(t, "math.test.js", src)
	e := findEntryBySymbol(entries, "returns correct value")
	if e == nil {
		t.Fatal("expected KindTestFunc for 'returns correct value'")
	}
}

// ---- Test functions: it.each ------------------------------------------------

func TestJS_ItEach(t *testing.T) {
	src := `it.each([1, 2, 3])('handles value %s', (val) => {
  expect(val).toBeTruthy();
});
`
	entries := mustExtract(t, "values.test.js", src)
	e := findEntryBySymbol(entries, "handles value %s")
	if e == nil {
		t.Fatalf("expected KindTestFunc for 'handles value %%s'")
	}
	if e.CommentKind != KindTestFunc {
		t.Errorf("CommentKind = %q, want test_func", e.CommentKind)
	}
}

// ---- Test functions: test.each ----------------------------------------------

func TestTS_TestEach_SingleLine(t *testing.T) {
	// test.each with inline table — name on the same invocation line.
	src := "test.each([[1, 1, 2]])('adds numbers correctly', (a, b, c) => {\n  expect(add(a, b)).toBe(c);\n});\n"
	entries := mustExtract(t, "math.test.ts", src)
	e := findEntryBySymbol(entries, "adds numbers correctly")
	if e == nil {
		t.Fatal("expected KindTestFunc for 'adds numbers correctly'")
	}
	if e.CommentKind != KindTestFunc {
		t.Errorf("CommentKind = %q, want test_func", e.CommentKind)
	}
}

func TestTS_TestEach_MultiLine(t *testing.T) {
	// Real-world jest/vitest pattern: data table is multi-line, name on continuation.
	src := `test.each([
  [1, 1, 2],
  [2, 3, 5],
])('adds %i + %i = %i', (a, b, c) => {
  expect(add(a, b)).toBe(c);
});
`
	entries := mustExtract(t, "math.test.ts", src)
	e := findEntryBySymbol(entries, "adds %i + %i = %i")
	if e == nil {
		t.Fatal("expected KindTestFunc for multi-line test.each continuation line")
	}
	if e.CommentKind != KindTestFunc {
		t.Errorf("CommentKind = %q, want test_func", e.CommentKind)
	}
}

// ---- Test functions: double-quoted names ------------------------------------

func TestJS_DoubleQuotedTestName(t *testing.T) {
	src := `describe("UserService", () => {
  it("should create user", () => {});
});
`
	entries := mustExtract(t, "user.test.js", src)
	e := findEntryBySymbol(entries, "UserService")
	if e == nil {
		t.Fatal("expected KindTestFunc for 'UserService'")
	}
	e2 := findEntryBySymbol(entries, "should create user")
	if e2 == nil {
		t.Fatal("expected KindTestFunc for 'should create user'")
	}
}

// ---- FileType=test for .test.ts files ---------------------------------------

func TestTS_FileTypeTest(t *testing.T) {
	src := `it('something', () => {});
`
	entries := mustExtract(t, "foo.test.ts", src)
	for _, e := range entries {
		if e.FileType != FileTypeTest {
			t.Errorf("entry %q: FileType = %q, want test", e.SymbolName, e.FileType)
		}
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}
}

// ---- FileType=test for .spec.js files ---------------------------------------

func TestJS_FileTypeSpec(t *testing.T) {
	src := `describe('Spec', () => {});
`
	entries := mustExtract(t, "foo.spec.js", src)
	for _, e := range entries {
		if e.FileType != FileTypeTest {
			t.Errorf("entry %q: FileType = %q, want test", e.SymbolName, e.FileType)
		}
	}
}

// ---- FileType=test for __tests__/ directory ---------------------------------

func TestJS_FileTypeTestsDir(t *testing.T) {
	src := `test('works', () => {});
`
	entries := mustExtract(t, "src/__tests__/utils.js", src)
	for _, e := range entries {
		if e.FileType != FileTypeTest {
			t.Errorf("entry %q: FileType = %q, want test", e.SymbolName, e.FileType)
		}
	}
}

// ---- Svelte file with <script> block ----------------------------------------

func TestSvelte_FileHeader(t *testing.T) {
	src := `<template>
  <div>Hello</div>
</template>

<script>
/**
 * Counter component.
 * Increments a value on click.
 */

export let count = 0;

function increment() {
  count++;
}
</script>

<style>
div { color: red; }
</style>
`
	entries := mustExtract(t, "src/Counter.svelte", src)

	h := findEntry(t, entries, KindFileHeader)
	if h == nil {
		t.Fatal("expected KindFileHeader in Svelte file")
	}
	if !strings.Contains(h.Text, "Counter component") {
		t.Errorf("Text = %q, want it to contain 'Counter component'", h.Text)
	}
	if h.Lang != "svelte" {
		t.Errorf("Lang = %q, want svelte", h.Lang)
	}
	// StartLine must be > 1 (the JSDoc is inside the <script> block, not line 1)
	if h.StartLine <= 1 {
		t.Errorf("StartLine = %d, expected > 1 (inside <script> block)", h.StartLine)
	}
}

// ---- Svelte file without <script> block -------------------------------------

func TestSvelte_NoScript(t *testing.T) {
	src := `<div>Hello World</div>
`
	entries := mustExtract(t, "src/Hello.svelte", src)
	if len(entries) != 0 {
		t.Errorf("expected no entries for Svelte without <script>, got %d", len(entries))
	}
}

// ---- Vue file with <script setup> -------------------------------------------

func TestVue_ScriptSetup_FileHeader(t *testing.T) {
	src := `<template>
  <button @click="count++">{{ count }}</button>
</template>

<script setup>
/**
 * Button counter Vue component.
 */
import { ref } from 'vue';

const count = ref(0);
</script>
`
	entries := mustExtract(t, "src/ButtonCounter.vue", src)

	h := findEntry(t, entries, KindFileHeader)
	if h == nil {
		t.Fatal("expected KindFileHeader in Vue file")
	}
	if !strings.Contains(h.Text, "Button counter Vue component") {
		t.Errorf("Text = %q", h.Text)
	}
	if h.Lang != "vue" {
		t.Errorf("Lang = %q, want vue", h.Lang)
	}
	if h.StartLine <= 1 {
		t.Errorf("StartLine = %d, expected > 1 (inside <script> block)", h.StartLine)
	}
}

// ---- Vue file with <script> (not setup) -------------------------------------

func TestVue_Script_DocComment(t *testing.T) {
	src := `<template>
  <div>{{ message }}</div>
</template>

<script>
/**
 * Greeting component.
 */

/**
 * Gets the greeting message.
 */
export function getGreeting() {
  return 'Hello';
}
</script>
`
	entries := mustExtract(t, "src/Greeting.vue", src)

	h := findEntry(t, entries, KindFileHeader)
	if h == nil {
		t.Fatal("expected KindFileHeader")
	}
	e := findEntryBySymbol(entries, "getGreeting")
	if e == nil {
		t.Fatal("expected DocComment for 'getGreeting'")
	}
	if e.CommentKind != KindDocComment {
		t.Errorf("CommentKind = %q, want doc_comment", e.CommentKind)
	}
}

// ---- Heading path truncation ------------------------------------------------

func TestJS_HeadingPathTruncation(t *testing.T) {
	longName := "this is a very long test name that exceeds sixty characters by quite a bit more"
	src := "it('" + longName + "', () => {});\n"
	entries := mustExtract(t, "long.test.js", src)
	e := findEntryBySymbol(entries, longName)
	if e == nil {
		t.Fatalf("expected entry with SymbolName = %q", longName)
	}
	// SymbolName should be the full name
	if e.SymbolName != longName {
		t.Errorf("SymbolName = %q, want full name", e.SymbolName)
	}
	// HeadingPath should be truncated to "Tests > " + 60 chars
	expectedHeading := "Tests > " + longName[:60]
	if e.HeadingPath != expectedHeading {
		t.Errorf("HeadingPath = %q, want %q", e.HeadingPath, expectedHeading)
	}
}

// ---- JSX file ---------------------------------------------------------------

func TestJSX_DocComment(t *testing.T) {
	src := `/**
 * React component library.
 */

/**
 * A simple button component.
 */
export function Button({ label }) {
  return <button>{label}</button>;
}
`
	entries := mustExtract(t, "src/Button.jsx", src)
	h := findEntry(t, entries, KindFileHeader)
	if h == nil {
		t.Fatal("expected KindFileHeader")
	}
	e := findEntryBySymbol(entries, "Button")
	if e == nil {
		t.Fatal("expected DocComment for 'Button'")
	}
	if h.Lang != "javascript" {
		t.Errorf("Lang = %q, want javascript", h.Lang)
	}
}

// ---- TSX file ---------------------------------------------------------------

func TestTSX_FileType(t *testing.T) {
	src := `/**
 * Component header.
 */
export const MyComp = () => <div />;
`
	entries := mustExtract(t, "src/MyComp.tsx", src)
	for _, e := range entries {
		if e.Lang != "typescript" {
			t.Errorf("entry %q: Lang = %q, want typescript", e.SymbolName, e.Lang)
		}
	}
}

// ---- Empty file -------------------------------------------------------------

func TestJS_EmptyFile(t *testing.T) {
	entries := mustExtract(t, "src/empty.js", "")
	if len(entries) != 0 {
		t.Errorf("expected no entries for empty file, got %d", len(entries))
	}
}

// ---- File with no JSDoc, only tests -----------------------------------------

func TestJS_OnlyTests(t *testing.T) {
	src := `describe('Calculator', () => {
  test('adds numbers', () => {
    expect(1 + 1).toBe(2);
  });
  test('subtracts numbers', () => {
    expect(3 - 1).toBe(2);
  });
});
`
	entries := mustExtract(t, "calc.test.js", src)
	// Should have: describe + 2 test entries = 3
	count := 0
	for _, e := range entries {
		if e.CommentKind == KindTestFunc {
			count++
		}
	}
	if count < 3 {
		t.Errorf("expected at least 3 KindTestFunc entries, got %d", count)
	}
	// No file header
	if findEntry(t, entries, KindFileHeader) != nil {
		t.Error("did not expect KindFileHeader when no JSDoc present")
	}
}

// ---- Backtick test name -----------------------------------------------------

func TestJS_BacktickTestName(t *testing.T) {
	src := "test(`renders correctly`, () => {});\n"
	entries := mustExtract(t, "render.test.js", src)
	e := findEntryBySymbol(entries, "renders correctly")
	if e == nil {
		t.Fatal("expected KindTestFunc for backtick string name")
	}
}

// ---- cleanJSDoc standalone --------------------------------------------------

func TestCleanJSDoc_MultiLine(t *testing.T) {
	lines := []string{
		"  /**",
		"   * First line.",
		"   * Second line.",
		"   */",
	}
	got := cleanJSDoc(lines)
	if !strings.Contains(got, "First line.") {
		t.Errorf("got %q, want it to contain 'First line.'", got)
	}
	if !strings.Contains(got, "Second line.") {
		t.Errorf("got %q, want it to contain 'Second line.'", got)
	}
}

func TestCleanJSDoc_SingleLine(t *testing.T) {
	lines := []string{"/** Module description. */"}
	got := cleanJSDoc(lines)
	if got != "Module description." {
		t.Errorf("got %q, want 'Module description.'", got)
	}
}

// ---- isTestFile standalone --------------------------------------------------

func TestIsTestFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"src/auth.test.js", true},
		{"src/auth.spec.js", true},
		{"src/auth.test.ts", true},
		{"src/auth.spec.ts", true},
		{"src/auth.test.jsx", true},
		{"src/auth.spec.tsx", true},
		{"src/__tests__/auth.js", true},
		{"__tests__/auth.js", true},
		{"src/auth.js", false},
		{"src/auth.ts", false},
		{"src/tests/auth.js", false}, // "tests" is not "__tests__"
	}
	for _, tc := range cases {
		got := isTestFile(tc.path)
		if got != tc.want {
			t.Errorf("isTestFile(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// ---- SupportedExts registration --------------------------------------------

func TestJSTS_Registered(t *testing.T) {
	exts := SupportedExts()
	expected := []string{".js", ".jsx", ".ts", ".tsx", ".svelte", ".vue"}
	extSet := make(map[string]bool)
	for _, e := range exts {
		extSet[e] = true
	}
	for _, want := range expected {
		if !extSet[want] {
			t.Errorf("extension %q not registered; got %v", want, exts)
		}
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Detective-XH/docgraph/internal/store"
)

// ---- patchMDFrontmatter tests ----

func TestPatchMD_NoFrontmatter(t *testing.T) {
	f := healTempMD(t, "# Hello\n\nBody.\n")
	if err := patchMDFrontmatter(f, map[string]string{"status": "shipped"}); err != nil {
		t.Fatal(err)
	}
	got := healReadFile(t, f)
	if !strings.HasPrefix(got, "---\nstatus: shipped\n---\n") {
		t.Errorf("unexpected prefix:\n%s", got)
	}
	if !strings.Contains(got, "# Hello") {
		t.Errorf("body missing:\n%s", got)
	}
}

func TestPatchMD_PartialFrontmatter(t *testing.T) {
	f := healTempMD(t, "---\ntags:\n  - plans\n---\n# Hello\n")
	if err := patchMDFrontmatter(f, map[string]string{"owner": "Alice", "status": "shipped"}); err != nil {
		t.Fatal(err)
	}
	got := healReadFile(t, f)
	if !strings.Contains(got, "tags:") {
		t.Errorf("existing key 'tags' missing:\n%s", got)
	}
	if !strings.Contains(got, "status: shipped") {
		t.Errorf("status not added:\n%s", got)
	}
	if !strings.Contains(got, "owner: Alice") {
		t.Errorf("owner not added:\n%s", got)
	}
	if strings.Count(got, "tags:") != 1 {
		t.Errorf("tags key duplicated:\n%s", got)
	}
}

func TestPatchMD_CompleteFrontmatter(t *testing.T) {
	orig := "---\nstatus: shipped\nowner: Alice\n---\n# Hello\n"
	f := healTempMD(t, orig)
	if err := patchMDFrontmatter(f, map[string]string{"status": "shipped", "owner": "Alice"}); err != nil {
		t.Fatal(err)
	}
	if got := healReadFile(t, f); got != orig {
		t.Errorf("expected no change; got:\n%s", got)
	}
}

func TestPatchMD_HTMLCommentNoFrontmatter(t *testing.T) {
	f := healTempMD(t, "<!-- a comment -->\n# Hello\n")
	if err := patchMDFrontmatter(f, map[string]string{"status": "shipped"}); err != nil {
		t.Fatal(err)
	}
	got := healReadFile(t, f)
	if !strings.HasPrefix(got, "---\nstatus: shipped\n---\n<!-- a comment -->") {
		t.Errorf("unexpected output:\n%s", got)
	}
	if !strings.Contains(got, "# Hello") {
		t.Errorf("body missing:\n%s", got)
	}
}

func TestPatchMD_HTMLCommentWithExistingFrontmatter(t *testing.T) {
	// Comment precedes an existing frontmatter block — the advisor's BLOCKER case.
	f := healTempMD(t, "<!-- a comment -->\n---\nstatus: shipped\n---\nbody\n")
	if err := patchMDFrontmatter(f, map[string]string{"owner": "Alice"}); err != nil {
		t.Fatal(err)
	}
	got := healReadFile(t, f)

	// Exactly one fence pair (2 occurrences of "---" as full lines).
	fenceCount := 0
	for line := range strings.SplitSeq(got, "\n") {
		if strings.TrimSpace(line) == "---" {
			fenceCount++
		}
	}
	if fenceCount != 2 {
		t.Errorf("expected 2 fence lines, got %d:\n%s", fenceCount, got)
	}
	if !strings.Contains(got, "status: shipped") {
		t.Errorf("existing status missing:\n%s", got)
	}
	if !strings.Contains(got, "owner: Alice") {
		t.Errorf("owner not added:\n%s", got)
	}
	// Comment must immediately follow the closing ---.
	if !strings.Contains(got, "---\n<!-- a comment -->") {
		t.Errorf("comment not placed after closing ---:\n%s", got)
	}
}

func TestPatchMD_UTFBOM(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}
	content := append(bom, []byte("---\nstatus: shipped\n---\n# Hello\n")...)
	f := healTempMDBytes(t, content)
	if err := patchMDFrontmatter(f, map[string]string{"owner": "Alice"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) >= 3 && raw[0] == 0xEF {
		t.Error("BOM not stripped")
	}
	if !strings.Contains(string(raw), "owner: Alice") {
		t.Errorf("owner not added:\n%s", raw)
	}
}

func TestPatchMD_CRLF(t *testing.T) {
	f := healTempMDBytes(t, []byte("---\r\nstatus: shipped\r\n---\r\nbody\r\n"))
	if err := patchMDFrontmatter(f, map[string]string{"owner": "Alice"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	lfCount := strings.Count(s, "\n")
	crlfCount := strings.Count(s, "\r\n")
	if lfCount != crlfCount {
		t.Errorf("CRLF not preserved: \\n=%d \\r\\n=%d in:\n%q", lfCount, crlfCount, s)
	}
}

func TestPatchMD_AtomicWriteFailure(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.md")
	if err := os.WriteFile(f, []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig, _ := os.ReadFile(f)

	// Make the directory read-only so os.Create(.heal_tmp) fails.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skip("cannot set directory read-only:", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_ = patchMDFrontmatter(f, map[string]string{"status": "shipped"})

	got, _ := os.ReadFile(f)
	if string(got) != string(orig) {
		t.Errorf("original file was modified on write failure")
	}
}

func TestPatchMD_MultiLineYAMLValue(t *testing.T) {
	content := "---\ndescription: |\n  line one\n  line two\n---\n# Hello\n"
	f := healTempMD(t, content)
	if err := patchMDFrontmatter(f, map[string]string{"status": "shipped"}); err != nil {
		t.Fatal(err)
	}
	got := healReadFile(t, f)
	if !strings.Contains(got, "description: |\n  line one\n  line two\n") {
		t.Errorf("multi-line YAML value corrupted:\n%s", got)
	}
	if !strings.Contains(got, "status: shipped") {
		t.Errorf("status not added:\n%s", got)
	}
}

func TestPatchMD_NonMappingFrontmatter(t *testing.T) {
	// Sequence frontmatter must be rejected, not silently corrupted.
	orig := "---\n- tag\n---\nbody\n"
	f := healTempMD(t, orig)
	err := patchMDFrontmatter(f, map[string]string{"status": "shipped"})
	if err == nil {
		t.Error("expected error for non-mapping frontmatter, got nil")
	}
	if got := healReadFile(t, f); got != orig {
		t.Errorf("file should be unchanged on error:\n%s", got)
	}
}

func TestPatchMD_OwnerSpecialChars(t *testing.T) {
	// Owner values containing YAML-special characters must be properly quoted.
	f := healTempMD(t, "# Hello\n")
	if err := patchMDFrontmatter(f, map[string]string{"owner": "Alice: Team"}); err != nil {
		t.Fatal(err)
	}
	got := healReadFile(t, f)
	if !strings.Contains(got, "owner:") {
		t.Fatalf("owner key missing:\n%s", got)
	}
	// Must not have a raw unquoted colon that would break YAML parsing.
	// Parse the written frontmatter to verify it round-trips cleanly.
	start := strings.Index(got, "---\n")
	end := strings.Index(got[start+4:], "\n---\n")
	if start < 0 || end < 0 {
		t.Fatalf("could not locate frontmatter block:\n%s", got)
	}
	yamlBlock := got[start+4 : start+4+end]
	var m map[string]string
	if err := yaml.Unmarshal([]byte(yamlBlock), &m); err != nil {
		t.Fatalf("frontmatter with special-char owner is not valid YAML: %v\nblock:\n%s", err, yamlBlock)
	}
	if m["owner"] != "Alice: Team" {
		t.Errorf("owner value round-tripped incorrectly: %q", m["owner"])
	}
}

// ---- inferStatus tests ----

func TestInferStatus(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"plans/shipped/foo.md", "shipped"},
		{"plans/decisions/bar.md", "closed"},
		{"docs/api.md", ""},
		{"shipped/something.md", "shipped"},
		{"decisions/x.md", "closed"},
		{"docs/shipped-notes.md", ""},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			if got := inferStatus(c.path); got != c.want {
				t.Errorf("inferStatus(%q) = %q, want %q", c.path, got, c.want)
			}
		})
	}
}

// ---- parseGitOwnerOutput tests ----

func TestParseGitOwner_SingleDominant(t *testing.T) {
	out := strings.Repeat("Alice\n", 9) + "Bob\n"
	v, skip := parseGitOwnerOutput(out)
	if v != "Alice" || skip != "" {
		t.Errorf("got value=%q skip=%q, want Alice/\"\"", v, skip)
	}
}

func TestParseGitOwner_Ambiguous(t *testing.T) {
	out := strings.Repeat("Alice\n", 6) + strings.Repeat("Bob\n", 4)
	v, skip := parseGitOwnerOutput(out)
	if v != "" {
		t.Errorf("expected no value, got %q", v)
	}
	if !strings.Contains(skip, "Alice") || !strings.Contains(skip, "Bob") {
		t.Errorf("skip reason missing author names: %q", skip)
	}
}

func TestParseGitOwner_TooFewCommits(t *testing.T) {
	out := "Alice\nAlice\n"
	v, skip := parseGitOwnerOutput(out)
	if v != "" || skip != "commit-count<3" {
		t.Errorf("got value=%q skip=%q, want \"\"/\"commit-count<3\"", v, skip)
	}
}

func TestParseGitOwner_ExactlyAtThreshold(t *testing.T) {
	// 85/100 = exactly 85% — should infer.
	out := strings.Repeat("Alice\n", 85) + strings.Repeat("Bob\n", 15)
	v, skip := parseGitOwnerOutput(out)
	if v != "Alice" || skip != "" {
		t.Errorf("got value=%q skip=%q, want Alice/\"\"", v, skip)
	}
}

// ---- inferOwner tests ----

func TestInferOwner_Override(t *testing.T) {
	v, skip := inferOwner("/nonexistent/file.md", "Placeholder", 10*time.Second)
	if v != "Placeholder" || skip != "" {
		t.Errorf("got value=%q skip=%q, want Placeholder/\"\"", v, skip)
	}
}

func TestInferOwner_GitTimeout(t *testing.T) {
	f, err := filepath.Abs("main.go")
	if err != nil {
		t.Skip("cannot resolve main.go:", err)
	}
	_, skip := inferOwner(f, "", 1*time.Millisecond)
	// On fast machines the git command may succeed before timeout; we just confirm
	// there is no panic and the result is either "" or "git-timeout".
	if skip != "" && skip != "git-timeout" && !strings.HasPrefix(skip, "top-2") && skip != "commit-count<3" {
		t.Errorf("unexpected skip reason: %q", skip)
	}
}

// ---- runHeal dry-run test ----

func TestRunHeal_DryRun(t *testing.T) {
	dir := t.TempDir()

	// File is in a shipped/ subdirectory so inferStatus returns "shipped".
	shippedDir := filepath.Join(dir, "shipped")
	if err := os.MkdirAll(shippedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mdPath := filepath.Join(shippedDir, "doc.md")
	if err := os.WriteFile(mdPath, []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(dir, "docgraph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	if err := st.InsertNodes([]store.Node{{
		ID:            "shipped/doc.md",
		Kind:          "document",
		Name:          "Hello",
		QualifiedName: "Hello",
		FilePath:      "shipped/doc.md",
		UpdatedAt:     1,
	}}); err != nil {
		t.Fatal(err)
	}

	// Dry-run: fix=false → no file modifications.
	flags := healFlags{fix: false, gitTimeout: 1 * time.Second}
	if err := runHeal(st, dir, flags); err != nil {
		t.Fatal(err)
	}

	got, readErr := os.ReadFile(mdPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "# Hello\n" {
		t.Errorf("dry-run modified file, got: %q", got)
	}
}

// ---- helpers ----

func healTempMD(t *testing.T, content string) string {
	t.Helper()
	return healTempMDBytes(t, []byte(content))
}

func healTempMDBytes(t *testing.T, content []byte) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "test.md")
	if err := os.WriteFile(f, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func healReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
